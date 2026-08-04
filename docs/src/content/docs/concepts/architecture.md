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
- creates/extends/deletes `DployInstanceClaim` resources — one per (owner, template),
- serves the embedded web UI.

That is the whole list. The API decides *who* is asking and *what* they asked for, then writes it
down; every decision about *what happens next* — which instance, for how long, whether the quota
allows it — belongs to the operator. Its RBAC says as much: within a single namespace, it reads
`DployTemplate`s, does CRUD on `DployInstanceClaim`s, and *reads* `DployInstance`s for the Manager
view. It cannot write an instance, and it cannot create a `HelmRelease`.

Only the claims the deployment lists in `FORWARDED_CLAIMS` are copied out of the token into
`spec.params`; the rest never leaves the API server.

### Dploy operator

Three controllers built with controller-runtime:

- **DployInstanceClaim controller** — turns a request into a running environment. It binds a warm
  pool member (or provisions one on demand), takes ownership of the instance, anchors the TTL at
  the binding, enforces the per-owner quota, and projects the instance's status back onto the
  claim so callers watch one object instead of two.
- **DployInstance controller** — the core. For each instance it generates an immutable UUID,
  creates the workload namespace, renders the connection URL and Helm values, ensures a Flux
  source + `HelmRelease`, projects the release's status back, and enforces the TTL.
- **DployTemplate controller** — maintains the **warm pool** for pool-method templates (creating
  unclaimed instances up to `pool.size`) and reports occupancy in the template status.

#### Binding a warm instance

Several users can want the last warm instance at the same moment, so the binding is decided by the
API server, not by the operator's own bookkeeping. The claim controller writes one label —
`dploy.dev/claim-uid` — onto a candidate instance through a full object update. Optimistic
concurrency does the rest: a claimer holding a stale copy is rejected with a conflict and moves on
to the next candidate. Only once that write lands does the winner apply the rest of the binding
(ownership, owner key, params, TTL), off the contention path.

The instance is then owned by the claim, which is what makes `kubectl delete dployinstanceclaim`
tear the environment down.

#### The whole cycle, without the API

Because every decision lives in the operator, the API is optional. A claim is a complete request:

```yaml
apiVersion: dploy.dev/v1alpha1
kind: DployInstanceClaim
metadata:
  name: john-doe-webterm
  namespace: dploy-system
spec:
  templateRef: webterm
  owner: john-doe
  waitForPool: true          # park on an empty pool instead of provisioning around it
  params:
    email: john@example.com  # visible to templates as .Params.email
```

```bash
kubectl apply -f claim.yaml
kubectl get dclaim john-doe-webterm -w
# NAME               TEMPLATE   OWNER      PHASE     INSTANCE          URL
# john-doe-webterm   webterm    john-doe   Pending
# john-doe-webterm   webterm    john-doe   Bound     webterm-pool-x9   https://webterm-a1b2c3d4.env.dploy.dev

# Extend: raise the requested lifetime, the operator moves the expiry
kubectl patch dclaim john-doe-webterm --type=merge -p '{"spec":{"ttlSeconds":7200}}'

# Tear down: the claim owns the instance, which owns the HelmRelease
kubectl delete dclaim john-doe-webterm
```

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
| **API** (namespaced `Role`) | read `dploytemplates` and `dployinstances`; CRUD `dployinstanceclaims` — nothing else |
| **Operator** (`ClusterRole`) | CRUD dploy CRs + status/finalizers, `helmreleases`, Flux sources, namespaces, events |

Only the operator can reach Flux, and only the operator can *write* a `DployInstance`. This makes
the trust boundary auditable: a compromised API can at most file environment requests under
identities it can already authenticate — never arbitrary workloads, and never a longer TTL or an
extra environment than the operator grants.

## Instance lifecycle

![DployInstance lifecycle: Pending to Provisioning; then Ready for on-demand instances, or Available then Claimed for pooled instances, or Failed on error; Ready and Claimed move to Expiring when the TTL elapses and are then Deleted.](/diagrams/dploy-lifecycle.svg)

### TTL anchoring

- The clock starts **when the claim binds**, not when it is created: the operator records
  `status.boundAt` and derives `status.expiresAt` from it. A request that waits ten minutes for a
  warm instance does not lose ten minutes of its lifetime.
- **Extending is a patch**, not a verb: raise `spec.ttlSeconds` and the expiry moves with it. The
  operator clamps the result to `status.maxTTLSeconds` — the template's base TTL plus its full
  extend budget (`ttl.maxExtends × ttl.extendSeconds`) — so there is no counter to keep in sync and
  no way to talk the API into a longer life than the catalog allows.
- `ttlSeconds: -1` means **unlimited**, and is only honored where the template is itself unlimited.
- An **unclaimed pool member never expires** — its clock starts when a claim binds it.
- On expiry the operator deletes the instance and leaves the claim behind as an `Expired`
  tombstone: the owner can see what happened, and it no longer counts against their quota.

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
  dploy.dev/claim: "john-doe-webterm"                       # bound claim (informational)
  dploy.dev/claim-uid: "3f2b1c9a-..."                       # the binding itself
```

`dploy.dev/claim-uid` is the one that matters: writing it is how a claim wins an instance, and
reading it back is how the operator recovers a binding after a restart. It keys on the UID rather
than the name because names can be recycled and UIDs cannot.

## Value & URL templating

Both `valuesTemplate` and `connectionURLTemplate` are rendered with Go `text/template` + [sprig](https://masterminds.github.io/sprig/).
The data context exposes:

| Field | Description |
|-------|-------------|
| `.Owner` | sanitized owner (empty for unclaimed pool members) |
| `.UUID` | immutable short UUID |
| `.BaseDomain` | `OperatorConfig.baseDomain` |
| `.Host` | `<name>-<uuid>.<baseDomain>` |
| `.URL` | resolved connection URL (available to `valuesTemplate`) |
| `.Namespace` | workload namespace |
| `.Template` | the `DployTemplate` object |
| `.Params` | request parameters plus the forwarded JWT claims — the only requester-supplied data a template sees |
| `.Config.Values` | `OperatorConfig.spec.values` |

The rendered values YAML is converted to JSON and set as the `HelmRelease`'s `spec.values`.

## Authentication flow

![OIDC authentication flow: the browser requests /auth/login; the API redirects to the OIDC provider; the user logs in; the provider redirects back with a code; the browser calls /auth/callback; the API exchanges the code for tokens; the API redirects back with the token, which the browser stores in localStorage.](/diagrams/dploy-auth-flow.svg)

## Scalability & HA

- The **API** is stateless — scale it horizontally behind a load balancer.
- The **operator** runs a single active replica by default; set `operator.leaderElection=true`
  to run more than one safely.
- All deployment heavy-lifting (sync, health, retries) is handled by Flux's controllers.
