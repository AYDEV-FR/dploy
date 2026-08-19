---
title: CTFd Integration
description: Give CTFd players a Run Instance button that files a DployInstanceClaim directly, with no API in between.
---

The CTFd plugin in `integrations/ctfd_dploy/` adds a **Run Instance** button to any
CTFd challenge tagged `dploy:<template>`, and an admin page listing every claim
and template.

It does not call the Dploy API. The plugin writes a `DployInstanceClaim`
straight to the cluster and reads its status back — the claim CRD *is* the
request interface, so CTFd's session is the identity and there is nothing to
broker between the two systems.

```text
CTFd session ──► plugin ──► DployInstanceClaim ──► operator ──► Flux ──► workload
                            create / patch / delete   bind, quota, TTL
                    ▲               │
                    └─── status ────┘  phase · URL · connection type · expiry
```

Because the claim projects the whole instance onto itself, the plugin polls
exactly one object to answer "is it up, where is it, when does it die".

## Wiring a challenge

Put JSON in the challenge's **Connection Information** field. Any challenge type
works, and nothing is duplicated between CTFd's database and the cluster — the
field *is* the mapping.

```json
{ "template": "kali", "ttlSeconds": 1800, "waitForPool": false }
```

| Key | Meaning |
| --- | --- |
| `template` | **required** — the `DployTemplate` to claim |
| `ttlSeconds` | requested lifetime in seconds (`-1` = unlimited), clamped to the template ceiling |
| `waitForPool` | queue for a warm pool member instead of provisioning on demand when the pool is empty |

The keys are named after `DployInstanceClaimSpec` because that is what they are:
the claim, minus the identity, which comes from the CTFd session.

Connection Information is the natural home for it — the per-challenge answer to
"how do I reach this", which for a deployed challenge is "Dploy hands you an
environment" rather than a fixed host and port. Players never see the JSON: the
plugin hides the theme's connection-info block and puts the environment panel
there instead.

Any other Connection Information (the usual `nc host 1337`, or none) means the
challenge is not a Dploy challenge, and it is left alone.

## Ownership and quota

The competitor is the owner: **the team in teams mode, the user in users
mode**. A team therefore shares one environment and one quota — the same thing
`ownerClaim: groups` buys on the OIDC side. `spec.ownerClaim` selects a JWT
claim and there is no JWT here, so the plugin ignores it.

Claims are named `<template>-t<team-id>` / `<template>-u<user-id>` so renaming
a team never orphans its environment, while `spec.owner` carries the readable
`t12-blue-team` form that the operator labels instances and renders
`connectionURLTemplate` with.

`spec.params` is filled with the same keys the API forwards by default
(`sub`, `preferred_username`, `email`, `name`, `groups`), so a `valuesTemplate`
written against `.Params` renders the same from either caller.

## Install

CTFd does not ship the Kubernetes Python client, so the plugin is built as an
image with it vendored in. From the repository root:

```bash
# image-volume artifact (default target)
docker build -f integrations/ctfd_dploy/Dockerfile -t ghcr.io/aydev-fr/ctfd-dploy-plugin .

# or a runnable CTFd with the plugin baked in, for clusters older than 1.33
docker build -f integrations/ctfd_dploy/Dockerfile --target ctfd-baked -t ctfd-dploy .
```

The [`ctfd` chart in dploy-charts](https://github.com/AYDEV-FR/dploy-charts)
mounts plugins straight from an OCI image (a Kubernetes Image Volume), so the
first artifact drops in with no rebuilt CTFd image — see
`ctfd/examples/dploy-values.yaml` there:

```yaml
plugins:
  - name: ctfd_dploy
    image: ghcr.io/aydev-fr/ctfd-dploy-plugin:0.2.0
env:
  - name: DPLOY_NAMESPACE
    value: dploy-system
```

## RBAC

The plugin acts as CTFd's service account, so that account — not the API's —
needs the grant. `integrations/ctfd_dploy/rbac.yaml` files a `Role` in Dploy's
namespace and a `RoleBinding` naming CTFd's service account as its subject —
check which account that is, since the chart declares none and the pod then
runs as `default` in its own namespace:

| Resource | Verbs | Why |
| --- | --- | --- |
| `dploytemplates` | `get`, `list` | resolve `dploy:<template>` and refuse early when it is missing or disabled |
| `dployinstanceclaims` | `get`, `list`, `create`, `patch`, `delete` | create = run, patch = extend, delete = stop |

Nothing else: no `dployinstances`, no namespaces, no secrets, no Flux objects.
The operator does the rest under its own identity.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `DPLOY_NAMESPACE` | `dploy-system` | namespace holding the templates and claims |
| `DPLOY_EXTEND_SECONDS` | `3600` | what one **Extend** click asks for when the template sets no `spec.ttl.extendSeconds`; the operator clamps to the real ceiling either way |

## What players see

| Claim phase | Panel |
| --- | --- |
| no claim | **Run Instance** button |
| `Pending` | spinner with the operator's reason (typically waiting on a warm pool member) |
| `Bound`, no URL yet | spinner — the instance exists and Flux is converging |
| `Bound` | connection link, or the rendered instructions for `connectionType: instructions`, plus time left, **Extend** and **Stop** |
| `Rejected` | the operator's message (quota, disabled template) and **Try again** |
| `Expired` | **Run Instance again** |

Two behaviours worth knowing:

- **A Run Instance click past a dead claim replaces it.** `Expired` is never revived
  and `Rejected` re-opens only when the spec changes, so an explicit click
  deletes the tombstone and files a fresh claim. Polling never does this.
- **A claim can go `Bound` → `Rejected` seconds later.** The operator re-ranks
  an owner's environments for 60s after binding to settle concurrent claims
  against the quota; the panel surfaces the message rather than retrying.
