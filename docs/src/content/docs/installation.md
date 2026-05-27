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
