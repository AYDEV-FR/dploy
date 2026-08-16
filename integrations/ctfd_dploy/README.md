# Dploy CTFd plugin

Gives CTFd players a **Run Instance** button on any challenge tagged
`dploy:<template>`, and gives admins a live view of every claim and template.

The plugin talks to the Kubernetes API **directly**. It writes a
`DployInstanceClaim` and reads its status — that is the entire integration.
There is no call to the dploy API, no OIDC round trip and no shared secret:
CTFd's own session is the identity, and the claim CRD is the request interface.

```
CTFd session ──► plugin ──► DployInstanceClaim ──► dploy operator ──► Flux ──► workload
                             (create/patch/delete)   (bind, quota, TTL)
                    ▲                │
                    └── status ──────┘   the claim projects the whole instance:
                                         phase, URL, connection type, expiry
```

## Wiring a challenge

Put JSON in the challenge's **Connection Information** field. Any challenge type
works — the field already exists on all of them.

```json
{ "template": "kali", "ttlSeconds": 1800, "waitForPool": false }
```

| Key | Meaning |
| --- | --- |
| `template` | **required** — the `DployTemplate` to claim |
| `ttlSeconds` | requested lifetime in seconds (`-1` = unlimited). Clamped to the template's ceiling |
| `waitForPool` | queue for a warm pool member instead of provisioning on demand when the pool is empty |

The keys are named after `DployInstanceClaimSpec` because that is what they
are — the claim, minus the identity, which comes from the CTFd session.

Connection Information is the natural home for this: it is the per-challenge
answer to "how do I reach this", and for a deployed challenge that answer is
"dploy hands you an environment" rather than a fixed host and port. Players
never see the JSON — the plugin hides the theme's connection-info block and puts
the environment panel there instead.

A challenge whose Connection Information is anything else (the usual
`nc host 1337`, or empty) is not a dploy challenge and is left completely
alone — no panel, no requests.

## Who owns the environment

The competitor is the owner: **the team in teams mode, the user in users
mode**. One team therefore shares one environment and one quota, which is what
`ownerClaim: groups` buys on the OIDC side. (`spec.ownerClaim` itself is an
OIDC concept — it selects a JWT claim, and there is no JWT here — so it is
ignored by this plugin.)

Claims are named `<template>-t<team-id>` / `<template>-u<user-id>`, so renaming
a team never orphans its running environment. `spec.owner` gets the prettier
`t12-blue-team` form, sanitised the same way `internal/kube/owner.go` does it,
because that is the value the operator labels instances and renders
`connectionURLTemplate` with.

The plugin fills `spec.params` with the same keys the API forwards by default
(`sub`, `preferred_username`, `email`, `name`, `groups`), so a `valuesTemplate`
written against `.Params` renders identically whether the request came from the
dploy API or from CTFd.

## Install

CTFd does not ship the `kubernetes` client, so the plugin is distributed as an
image with it vendored in. Build from the repository root:

```bash
# image-volume artifact (default target)
docker build -f integrations/ctfd_dploy/Dockerfile -t ghcr.io/aydev-fr/ctfd-dploy-plugin .

# or a runnable CTFd with the plugin baked in, for clusters older than 1.33
docker build -f integrations/ctfd_dploy/Dockerfile --target ctfd-baked -t ctfd-dploy .
```

The [`ctfd` chart in dploy-charts](https://github.com/AYDEV-FR/dploy-charts)
mounts plugins straight from an OCI image, so the first artifact drops in with
no rebuilt CTFd image:

```yaml
plugins:
  - name: ctfd_dploy # -> /opt/CTFd/CTFd/plugins/ctfd_dploy
    image: ghcr.io/aydev-fr/ctfd-dploy-plugin:0.2.0
    pullPolicy: IfNotPresent

env:
  - name: DPLOY_NAMESPACE
    value: dploy-system
```

Then grant CTFd's service account access to dploy's namespace. Edit the two
namespaces and the service-account name first — **the chart declares no service
account of its own**, so unless you set one the CTFd pod runs as `default` in
its namespace, and that is the subject to bind:

```bash
kubectl apply -f integrations/ctfd_dploy/rbac.yaml
```

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `DPLOY_NAMESPACE` | `dploy-system` | namespace holding the `DployTemplate` and `DployInstanceClaim` objects |
| `DPLOY_EXTEND_SECONDS` | `3600` | how much one **Extend** click asks for when the template doesn't set `spec.ttl.extendSeconds`. The operator clamps to the real ceiling either way |

## Endpoints

Player routes are session-authenticated and act only on the caller's own
claim — the owner comes from the session, never from request input.

| Route | Purpose |
| --- | --- |
| `GET /plugins/ctfd_dploy/info` | is this challenge wired to a live template (one call per modal open) |
| `GET /plugins/ctfd_dploy/status` | the caller's claim state; **read-only**, never files a claim |
| `POST /plugins/ctfd_dploy/run` | file the claim (idempotent) — the **Run Instance** button |
| `POST /plugins/ctfd_dploy/extend` | raise `spec.ttlSeconds` |
| `POST /plugins/ctfd_dploy/stop` | delete the claim; the instance follows |
| `GET /plugins/ctfd_dploy/mine.json` | every environment the caller holds |
| `GET /admin/plugins/ctfd_dploy` | admin page (claims + templates) |

## Behaviour worth knowing

- **Running past a dead claim replaces it.** `Expired` is never revived by
  the operator and `Rejected` only re-opens when the spec changes, so an
  explicit Run Instance click deletes the tombstone and files a fresh claim. Polling
  never does this — only a click.
- **Quota rejections are surfaced, not retried.** The operator re-ranks an
  owner's environments for 60s after binding, so a claim can go `Bound` →
  `Rejected` moments later; the panel shows the operator's own message and
  offers *Try again*.
- **Pool exhaustion is a `Pending` claim, not an error.** With `waitForPool`
  the player queues; without it the operator provisions on demand instead.
