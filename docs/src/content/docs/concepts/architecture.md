---
title: Architecture
description: How the Dploy operator and API cooperate to deploy environments via Flux.
---

Dploy is built as a **Kubernetes operator** plus a **thin API**. The two communicate only
through custom resources — a deliberate separation that keeps the API stateless and prevents it
from touching the deployment engine directly.

## Overview

![Dploy architecture: the browser talks to the Dploy API, which writes DployTemplate and DployInstance custom resources; the operator reconciles them into Flux GitRepository/HelmRepository and HelmRelease resources that install the environment into a per-instance workload namespace.](/diagrams/dploy-architecture.svg)

## Components

### Dploy API

A stateless GoFiber server that:

- authenticates requests via **JWT/OIDC** (JWKS, cached 15 min),
- serves the catalog by listing `DployTemplate` resources,
- creates/claims/extends/deletes `DployInstance` resources,
- serves the embedded web UI.

It has **no Flux permissions** — its RBAC only allows reading `DployTemplate`s and CRUD on
`DployInstance`s in a single namespace. It physically cannot create a `HelmRelease`.

### Dploy operator

Two controllers built with controller-runtime:

- **DployInstance controller** — the core. For each instance it generates an immutable UUID,
  creates the workload namespace, renders the connection URL and Helm values, ensures a Flux
  source + `HelmRelease`, projects the release's status back, and enforces the TTL.
- **DployTemplate controller** — maintains the **warm pool** for pool-method templates (creating
  unclaimed instances up to `pool.size`) and reports occupancy in the template status.

### Flux

Dploy delegates all deployment to Flux. Because Dploy only exposes `git`/`helm` chart sources,
the operator always builds a `HelmRelease` whose chart references a **`GitRepository`** or
**`HelmRepository`** (OCI Helm registries use a `HelmRepository` of type `oci`). The
`HelmRelease` and its source live in the instance's own namespace (so owner references are
valid) and install into the per-instance workload namespace via `targetNamespace`.

### OIDC provider

External identity provider (Authentik, Keycloak, Dex, …) that issues JWTs and exposes a JWKS
endpoint. See [OIDC Providers](/deployment/oidc-providers/).

## The RBAC boundary

The operator/API split is enforced by two service accounts:

| Identity | Can do |
|----------|--------|
| **API** (namespaced `Role`) | read `dploytemplates`; CRUD `dployinstances` — nothing else |
| **Operator** (`ClusterRole`) | CRUD dploy CRs + status/finalizers, `helmreleases`, Flux sources, namespaces, events |

Only the operator can reach Flux. This makes the trust boundary auditable: a compromised API can
at most create instance requests, never arbitrary workloads.

## Instance lifecycle

![DployInstance lifecycle: Pending to Provisioning; then Ready for on-demand instances, or Available then Claimed for pooled instances, or Failed on error; Ready and Claimed move to Expiring when the TTL elapses and are then Deleted.](/diagrams/dploy-lifecycle.svg)

### TTL anchoring

- `spec.expiresAt` set by the API is **authoritative** (used at creation and on `/extend`).
- Otherwise, when an instance first becomes active the operator anchors expiry at
  `now + ttlSeconds`.
- `ttlSeconds: -1` means **unlimited**.
- An **unclaimed pool member never expires** — its clock starts when a user claims it.

### Teardown

`DployInstance` carries a finalizer (`dploy.dev/instance-cleanup`). On deletion the operator
removes the `HelmRelease` (waiting for Flux to finish the Helm uninstall), then deletes the
workload namespace. The Flux source is owner-referenced and garbage-collected.

## Labels & annotations

```yaml
labels:
  dploy.dev/managed: "true"
  dploy.dev/owner: "john-doe"      # sanitized owner
  dploy.dev/template: "webterm"    # source template
  dploy.dev/instance: "a1b2c3d4"   # short UUID
  dploy.dev/pooled: "true"         # warm-pool members only
annotations:
  dploy.dev/extend-count: "2"      # API-managed TTL extension counter
```

## Value & URL templating

Both `valuesTemplate` and `connectionURLTemplate` are rendered with Go `text/template` + [sprig](https://masterminds.github.io/sprig/).
The data context exposes:

| Field | Description |
|-------|-------------|
| `.Owner` | sanitized owner (empty for unclaimed pool members) |
| `.UUID` | immutable short UUID |
| `.BaseDomain` | `OperatorConfig.baseDomain` |
| `.IngressHost` | `<owner>-<uuid>.<baseDomain>` |
| `.URL` | resolved connection URL (available to `valuesTemplate`) |
| `.Namespace` | workload namespace |
| `.Template` | the `DployTemplate` object |
| `.Params` | request parameters |
| `.Claims` | the requester's JWT claims |
| `.Config.Values` | `OperatorConfig.spec.values` |

The rendered values YAML is converted to JSON and set as the `HelmRelease`'s `spec.values`.

## Authentication flow

![OIDC authentication flow: the browser requests /auth/login; the API redirects to the OIDC provider; the user logs in; the provider redirects back with a code; the browser calls /auth/callback; the API exchanges the code for tokens; the API redirects back with the token, which the browser stores in localStorage.](/diagrams/dploy-auth-flow.svg)

## Scalability & HA

- The **API** is stateless — scale it horizontally behind a load balancer.
- The **operator** runs a single active replica by default; set `operator.leaderElection=true`
  to run more than one safely.
- All deployment heavy-lifting (sync, health, retries) is handled by Flux's controllers.
