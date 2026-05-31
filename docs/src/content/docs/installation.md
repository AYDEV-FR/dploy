---
title: Installation
description: Deploy the Dploy operator and API to a Kubernetes cluster with Helm.
---

This guide covers deploying Dploy to a Kubernetes cluster.

## Prerequisites

- **Kubernetes cluster** (1.30+)
- **Flux** controllers installed — Dploy uses the `source-controller` and `helm-controller`
  (you do **not** need the full Flux GitOps setup, just those two controllers and their CRDs)
- **OIDC provider** (Authentik, Keycloak, Dex, …) for JWT authentication
- **Ingress controller** or **Gateway API** controller to expose environments
- **kubectl** and **Helm 3** configured against your cluster

:::note
Dploy's Day-One engine is **Flux**. The CRDs keep an `argocd` engine value reserved for the
future, but only the Flux reconciler is implemented today.
:::

### Install Flux controllers

If you don't already run Flux, the source and helm controllers are enough:

```bash
flux install --components=source-controller,helm-controller
```

## Routing: Ingress, Gateway API or both

Dploy itself is one HTTP service that needs to be reachable at a single URL (e.g. `dploy.example.com`). Per-instance environments need a **hostname per instance** (`<name>-<uid>.<baseDomain>`), routed to the workload Service the chart creates.

Either routing technology works for both layers — and you can mix them (e.g. Gateway API for the per-instance traffic, Ingress for the dploy API). Pick what your cluster already runs.

### Option A — Ingress controller (nginx, traefik, cilium-ingress, …)

The chart's `ingress.*` values render an `Ingress` for the dploy API directly:

```yaml
ingress:
  enabled: true
  className: nginx
  host: dploy.example.com
  annotations:
    # If your ingress controller doesn't set per-Ingress addresses (e.g. cilium
    # shared LB), pin external-dns to the shared LB IP yourself.
    external-dns.alpha.kubernetes.io/target: "10.0.0.42"
  tls:
    - secretName: dploy-tls
      hosts: [dploy.example.com]
```

Per-instance routing is set in each `DployTemplate.valuesTemplate` — most community charts expose `ingress.*`:

```yaml
valuesTemplate: |
  ingress:
    enabled: true
    className: nginx
    hosts:
      - host: "{{ .Host }}"
        paths: [{ path: /, pathType: Prefix }]
```

external-dns picks up Ingress out of the box with `--source=ingress`.

### Option B — Gateway API (cilium agentgateway, envoy-gateway, istio, …)

Deploy a `Gateway` once (cluster-wide or per-namespace), then dploy and every instance attaches as an `HTTPRoute`:

```yaml
# Cluster Gateway — owned outside the dploy chart.
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: dploy-gateway
  namespace: gateway-system
spec:
  gatewayClassName: agentgateway      # or envoy, istio, …
  listeners:
    - { name: http, port: 80, protocol: HTTP, allowedRoutes: { namespaces: { from: All } } }
```

Dploy chart doesn't render an HTTPRoute today; if you want the dploy API on Gateway API, drop a route in next to it:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: dploy, namespace: dploy-system }
spec:
  parentRefs: [{ name: dploy-gateway, namespace: gateway-system }]
  hostnames: [dploy.example.com]
  rules:
    - matches: [{ path: { type: PathPrefix, value: / } }]
      backendRefs: [{ name: dploy, port: 80 }]
```

Per-instance routing again lives in `valuesTemplate` — charts in [`AYDEV-FR/dploy-charts`](https://github.com/AYDEV-FR/dploy-charts) expose `httpRoute.*`:

```yaml
valuesTemplate: |
  httpRoute:
    enabled: true
    parentRefs:
      - { name: dploy-gateway, namespace: gateway-system }
    hostnames: ["{{ .Host }}"]
    annotations:
      external-dns.alpha.kubernetes.io/target: "10.0.0.42"
```

#### external-dns + HTTPRoute

external-dns ≥ 0.14 supports the Gateway API but **the source is opt-in** and requires an extra RBAC verb the default chart doesn't grant. Patch both:

```bash
# 1. Enable the source.
kubectl patch deploy -n dns-system external-dns --type=json \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--source=gateway-httproute"}]'

# 2. Grant list/watch on namespaces (the gateway-httproute source needs it
#    for cross-namespace parentRef resolution; without it the pod crash-loops
#    with `failed to sync *v1.Namespace: context deadline exceeded`).
kubectl patch clusterrole external-dns --type=json \
  -p='[{"op":"add","path":"/rules/-","value":{"apiGroups":[""],"resources":["namespaces"],"verbs":["get","list","watch"]}}]'
```

The DNS target comes from the parent `Gateway`'s `status.addresses` by default. Override with `external-dns.alpha.kubernetes.io/target` on the `HTTPRoute` itself when the gateway has no address (e.g. it's behind a separate LB), as shown in the `valuesTemplate` snippet above.

### Option C — Mixed

Common pattern: dploy itself on a stable Ingress (often pre-existing), per-instance environments on the Gateway API. Both work concurrently; external-dns just needs both `--source=ingress` (default) and `--source=gateway-httproute` (opt-in).

## Install with Helm

The chart deploys **both** components (operator + API) and installs the CRDs.

```bash
helm install dploy ./charts/dploy \
  --namespace dploy-system \
  --create-namespace \
  --set auth.jwksURL="https://your-oidc-provider.com/keys" \
  --set auth.jwtIssuer="https://your-oidc-provider.com" \
  --set auth.oidcClientID="dploy" \
  --set auth.oidcClientSecret="your-client-secret"
```

### With a values file

```yaml
# values.yaml
config:
  namespace: dploy-system          # where DployTemplate/DployInstance CRs live
  defaultTTL: 86400                # fallback TTL when a template omits it
  maxEnvironmentsPerUser: 5

auth:
  jwksURL: "https://your-oidc-provider.com/keys"
  jwtIssuer: "https://your-oidc-provider.com"
  jwtUsernameClaim: preferred_username
  oidcClientID: "dploy"
  oidcClientSecret: "your-client-secret"
  oidcRedirectURL: "https://dploy.your-domain.com/auth/callback"

ingress:
  enabled: true
  className: nginx
  host: dploy.your-domain.com
  tls:
    - secretName: dploy-tls
      hosts:
        - dploy.your-domain.com

operator:
  enabled: true
  # Publish/override the operator image as needed
  image:
    repository: ghcr.io/aydev-fr/dploy-operator
    tag: main
```

```bash
helm install dploy ./charts/dploy \
  --namespace dploy-system --create-namespace \
  -f values.yaml
```

### Upgrade

```bash
helm upgrade dploy ./charts/dploy --namespace dploy-system -f values.yaml
```

:::caution
Helm installs CRDs from the chart's `crds/` directory on **install** but does not upgrade them
automatically. After bumping the chart, re-apply the CRDs:

```bash
kubectl apply -f config/crd/bases   # or: make install
```
:::

## Container images

Two images are built and pushed by CI to GHCR:

| Component | Image |
|-----------|-------|
| API | `ghcr.io/aydev-fr/dploy` |
| Operator | `ghcr.io/aydev-fr/dploy-operator` |

Build them locally with:

```bash
make docker-build            # API image -> dploy-api:local
make docker-build-operator   # operator image -> dploy-operator:local
```

## Helm values reference

### API (`config`, `auth`, `service`, `ingress`)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `config.namespace` | Namespace holding the CRs (empty = release namespace) | `""` |
| `config.maxEnvironmentsPerUser` | Per-user quota (fallback) | `5` |
| `config.defaultTTL` | Default initial TTL in seconds | `86400` |
| `config.extendTTL` | Default extension granted per `/extend` | `7200` |
| `auth.jwksURL` | JWKS endpoint for JWT validation | — (required) |
| `auth.jwtIssuer` | Expected JWT issuer | — (required) |
| `auth.jwtAudience` | Expected JWT audience | `dploy` |
| `auth.jwtUsernameClaim` | Claim used as the username | `preferred_username` |
| `auth.oidcClientID` / `oidcClientSecret` | OIDC client credentials | `dploy` / — |
| `auth.oidcRedirectURL` | OIDC callback URL | — |
| `ingress.enabled` / `className` / `host` / `tls` | Ingress for the API/UI | `false` / `nginx` / … |

### Operator (`operator`)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `operator.enabled` | Deploy the operator | `true` |
| `operator.replicaCount` | Replicas | `1` |
| `operator.leaderElection` | Enable leader election (needed for >1 replica) | `false` |
| `operator.image.repository` / `tag` | Operator image | `ghcr.io/aydev-fr/dploy-operator` / `main` |

## Verify

```bash
kubectl get pods -n dploy-system
kubectl get crds | grep dploy.dev
kubectl get dploytemplates,dployinstances -A
```

## Next steps

- [Configuration](/configuration/) — environment variables and the `OperatorConfig`
- [Templates & Instances](/concepts/templates/) — define your catalog
- [OIDC Providers](/deployment/oidc-providers/) — configure authentication
