---
title: Templates, Claims & Instances
description: Define your catalog with DployTemplate, request environments with DployInstanceClaim, and understand the DployInstance lifecycle.
---

Three resources, one per job:

| Resource | Who writes it | What it is |
|---|---|---|
| **`DployTemplate`** | you | a catalog entry: which chart, how it is configured, how long it lives |
| **`DployInstanceClaim`** | the API, or you with `kubectl` | a *request* for one environment of a template |
| **`DployInstance`** | the operator | the environment itself, reconciled into Flux resources |

Launching an environment means writing a claim. The operator binds it — handing over a warm pool
member or provisioning an instance on demand — and projects the instance's state back onto the
claim, so a caller only ever watches the object it created.

## DployTemplate

A `DployTemplate` is a catalog entry: a Helm chart plus how it should be instantiated.

```yaml
apiVersion: dploy.dev/v1alpha1
kind: DployTemplate
metadata:
  name: webterm
  namespace: dploy-system
spec:
  displayName: "Web Terminal"
  description: "Browser-based shell for students"
  icon: terminal
  category: "learning,linux"
  enabled: true
  visible: true                  # hidden templates stay runnable by name

  method: on-demand              # or "pool"

  chart:
    type: git                    # "git" or "helm"
    repoURL: https://github.com/AYDEV-FR/dploy-charts
    path: charts/webterm         # chart path (git) — for helm, use `chart:`
    targetRevision: main         # branch/tag (git) or version (helm)

  valuesTemplate: |
    ingress:
      enabled: true
      host: "{{ .Host }}"
    user: "{{ .Owner }}"

  ttl:
    seconds: 86400               # -1 = unlimited
    extendSeconds: 7200
    maxExtends: 5

  parameters:
    - name: shell
      description: "Login shell"
      default: "/bin/bash"
```

### Fields

| Field | Description |
|-------|-------------|
| `displayName` / `description` / `icon` / `category` | UI metadata (`category` format: `"category,subcategory"`) |
| `enabled` | Gates whether instances may be created (default `true`) |
| `visible` | Show in listings (default `true`). Hidden templates remain runnable via `/run/{name}` |
| `ownerClaim` | JWT claim that identifies the owner (primary key) — see [Ownership](#ownership). Empty = the API's username claim |
| `method` | `on-demand` (one fresh instance per request) or `pool` (pre-warmed) |
| `pool` | Pool settings — see below (required for `method: pool`) |
| `engine` | Override `OperatorConfig.defaultEngine` (currently `flux`) |
| `chart` | Chart source — see below |
| `valuesTemplate` | Go template rendered to Helm values YAML |
| `valueFiles` | Values file paths within the chart repository |
| `connectionURLTemplate` | Override the cluster-wide URL template |
| `ttl` | `seconds` (-1 = unlimited), `extendSeconds`, `maxExtends` |
| `maxInstancesPerUser` | Per-template quota override |
| `parameters` | Request-supplied params (exposed as `.Params`) |

### Ownership

`ownerClaim` chooses which JWT claim is the **owner** — the primary key Dploy uses for the owner
label, per-owner quota, instance naming, and listing. This makes a template either personal or
team-shared:

```yaml
# Per-user: each user gets their own instance
spec:
  ownerClaim: preferred_username
```

```yaml
# Per-team: everyone in the group shares one instance and one quota
spec:
  ownerClaim: groups
```

- The claim value is sanitized (lowercased, `[a-z0-9-]`, ≤63 chars). Multi-valued claims (e.g.
  `groups`) use their **first** value when an instance is created.
- The **quota** applies **per owner key** — a team has its own allowance, independent of each
  member's personal quota.
- `/run/{env}` returns the owner's existing instance, so a teammate launching the same
  group-owned template **reuses** the running environment instead of creating a new one.
- `GET /api/environments` lists everything you own across all your identities — your username
  plus every group you belong to.
- An empty `ownerClaim` falls back to the API's configured username claim (`JWT_USERNAME_CLAIM`).

### Chart sources

Dploy is engine-neutral about where charts come from, and maps them onto Flux:

```yaml
# Git repository (the chart lives at a path in the repo)
chart:
  type: git
  repoURL: https://github.com/AYDEV-FR/dploy-charts
  path: charts/vscode
  targetRevision: main           # branch or tag
```

```yaml
# Helm repository
chart:
  type: helm
  repoURL: https://charts.bitnami.com/bitnami
  chart: postgresql
  targetRevision: "15.5.0"       # chart version
```

```yaml
# OCI Helm registry (a HelmRepository of type "oci")
chart:
  type: helm
  repoURL: oci://ghcr.io/aydev-fr/charts
  chart: jupyter
  targetRevision: "1.2.3"
```

### Warm pools

With `method: pool`, the DployTemplate controller keeps a set of pre-deployed, **unclaimed**
instances so users get an environment instantly:

```yaml
spec:
  method: pool
  pool:
    size: 3          # warm, idle instances to keep available
    maxSize: 10      # cap on total instances (0 = unlimited)
    recycle: true    # replace an instance after it is released
```

When a claim arrives for a pool template, the operator binds one `Available` instance to it — the
owner and the TTL clock are stamped at that moment. The DployTemplate controller then provisions a
replacement to refill the pool.

If no warm instance is free, `spec.waitForPool` decides: `true` (the API's default) parks the claim
as `Pending` until one frees up, `false` provisions a dedicated instance on demand instead.

`size` is a target, not a floor: lowering it reclaims the members that are now surplus, and
`size: 0` drains the warm set entirely. The purge only ever takes from **unclaimed** members — a
claimed environment is never destroyed to satisfy a size change, and a claim that lands while the
purge is running wins the race. Surplus members are picked least-useful-first (failed, then still
provisioning, then `Available`), keeping the oldest warm instances since those have been healthy
longest.

Deleting a template takes its environments with it, claimed ones included: a claim whose template
disappears becomes `Rejected`, and rejecting releases the instance it was holding. Without that the
instance — owner-referenced to the claim rather than the template — would sit `Failed` with its
workload namespace and pods still running until someone deleted it by hand.

Two cases behave differently on purpose:

- **A disabled template keeps its warm set.** Disabling is usually temporary, and draining would
  make re-enabling pay the full provisioning cost again.
- **Switching `method` away from `pool` drains it.** Warm members are only ever handed out for
  `method: pool`, so members left behind by a method switch could never be claimed by anyone.

### Connection URLs

The instance URL is resolved with this precedence:

1. `DployTemplate.spec.connectionURLTemplate`
2. `OperatorConfig.spec.connectionURLTemplate`
3. fallback `https://<name>-<uuid>.<baseDomain>` (the bare `.Host`)

```yaml
spec:
  connectionURLTemplate: "https://{{ .Owner }}-{{ .UUID }}.lab.example.com"
```

### Values templating

`valuesTemplate` is rendered with Go `text/template` + [sprig](https://masterminds.github.io/sprig/) against the instance context
(`.Owner`, `.UUID`, `.Host`, `.URL`, `.Params`, `.Config.Values`, …). The
result is parsed as YAML and handed to the `HelmRelease`.

```yaml
valuesTemplate: |
  workspaceName: "{{ .Owner }}-workspace"
  sessionId: "{{ .UUID }}"
  email: "{{ .Params.email }}"
  ingress:
    host: "{{ .Host }}"
  {{- if eq .Params.size "large" }}
  resources:
    limits: { cpu: "2", memory: 4Gi }
  {{- end }}
```

## DployInstanceClaim

A claim is a request for one environment. It is the only dploy resource the API writes, and the
only one you need to write by hand to drive the operator.

```yaml
apiVersion: dploy.dev/v1alpha1
kind: DployInstanceClaim
metadata:
  name: john-doe-webterm       # conventionally <owner>-<template>: one claim per pair
  namespace: dploy-system
spec:
  templateRef: webterm
  owner: john-doe              # the identity key: quota, listing and naming all use it
  waitForPool: true            # empty pool → wait, rather than provision on demand
  params:                      # request params + the forwarded JWT claims, seen as .Params
    shell: /bin/zsh
    email: john@example.com
  ttlSeconds: 0                # 0 = the template's default; raise it to extend
status:
  phase: Bound
  instanceRef: webterm-pool-x9k2
  uuid: a1b2c3d4
  connectionURL: https://webterm-a1b2c3d4.env.dploy.dev
  connectionType: web
  instancePhase: Claimed
  health: Healthy
  boundAt: "2026-01-14T16:00:00Z"     # the TTL anchor
  expiresAt: "2026-01-15T16:00:00Z"   # boundAt + ttlSeconds
  ttlSeconds: 86400                   # granted, after clamping
  maxTTLSeconds: 100800               # ceiling spec.ttlSeconds may be raised to
```

### Claim phases

![DployInstanceClaim lifecycle: a claim is Pending until the operator binds an instance to it, then Bound; it is Rejected when it cannot be satisfied, and Expired once its TTL elapses.](/diagrams/dploy-claim-lifecycle.svg)

| Phase | Meaning |
|-------|---------|
| `Pending` | Accepted, not holding an environment yet — typically waiting for a warm instance |
| `Bound` | An instance is bound and owned by the claim; `status` mirrors it |
| `Rejected` | Unsatisfiable as written (quota exceeded, unknown or disabled template). Terminal until the spec changes |
| `Expired` | The environment outlived its TTL and was torn down. The claim survives as a tombstone and no longer counts against the quota |

The `Bound` condition carries the reason and a human-readable message — why a claim is waiting, or
why it was refused. The `Ready` condition mirrors the bound instance's readiness.

### TTL and extensions

The clock starts **when the claim binds**, not when it is created, so time spent queueing for a
warm instance costs nothing. Extending is a patch, not a verb:

```bash
kubectl patch dclaim john-doe-webterm --type=merge -p '{"spec":{"ttlSeconds":100800}}'
```

The operator clamps the result to `status.maxTTLSeconds` — the template's base TTL plus its full
extend budget (`ttl.maxExtends × ttl.extendSeconds`) — and reports what it actually granted in
`status.ttlSeconds`. `-1` means unlimited, and is only honored where the template is itself
unlimited.

### Teardown

The claim **owns** the instance it binds, so deleting the claim cascades:

```bash
kubectl delete dclaim john-doe-webterm
```

The instance's finalizer then removes the `HelmRelease` and the workload namespace. For a pool
template, the DployTemplate controller provisions a fresh warm member to take its place.

## DployInstance

A `DployInstance` is one deployed (or pooled) environment. The operator creates it — from a claim,
or as a warm pool member — and owns its **status**. You rarely write one by hand.

```yaml
apiVersion: dploy.dev/v1alpha1
kind: DployInstance
metadata:
  name: john-doe-webterm
  namespace: dploy-system
  labels:
    dploy.dev/owner: john-doe
    dploy.dev/template: webterm
    dploy.dev/claim: john-doe-webterm      # the claim holding it
    dploy.dev/claim-uid: 3f2b1c9a-…        # the binding itself
  annotations:
    dploy.dev/bound-at: "2026-01-14T16:00:00Z"
  ownerReferences:                          # the claim owns it → delete cascades
    - apiVersion: dploy.dev/v1alpha1
      kind: DployInstanceClaim
      name: john-doe-webterm
      controller: true
spec:
  templateRef: webterm
  owner: john-doe
  params:
    shell: /bin/zsh
  ttlSeconds: 86400
  expiresAt: "2026-01-15T16:00:00Z"
status:
  phase: Ready
  uuid: a1b2c3d4
  namespace: john-doe-webterm-a1b2c3d4
  url: https://john-doe-a1b2c3d4.env.dploy.dev
  engine: flux
  engineRef: john-doe-webterm
  health: Healthy
  expiresAt: "2026-01-15T16:00:00Z"
```

### Instance phases

| Phase | Meaning |
|-------|---------|
| `Pending` | Accepted, not yet reconciled |
| `Provisioning` | Flux is installing the release |
| `Ready` | On-demand instance healthy and reachable |
| `Available` | Warm pool member, unclaimed |
| `Claimed` | Pool member handed to a user |
| `Expiring` | Past TTL, being torn down |
| `Failed` | Provisioning or reconciliation failed |

## Icons

Suggested icon identifiers for the web UI:

| Icon ID | Use case |
|---------|----------|
| `terminal` | Terminals, shells |
| `desktop` | VNC / desktop |
| `code` | IDEs, editors |
| `book` | Notebooks, docs |
| `database` | Databases |
| `box` | Generic containers |
| `web` | Web apps |
| `default` | Fallback |

## Chart requirements

Your chart receives whatever your `valuesTemplate` renders — there are no implicitly-injected
values. Expose a configurable ingress host (or whichever fields your template sets) so each
instance gets its unique URL, for example:

```yaml
# templates/ingress.yaml (in your chart)
spec:
  rules:
    - host: {{ .Values.ingress.host | quote }}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: {{ include "chart.fullname" . }}
                port: { number: 80 }
```

See [TLS Certificates](/deployment/tls-certificates/) for wildcard-certificate strategies that
keep per-environment hostnames out of Certificate Transparency logs.
