# Dploy

Launch ephemeral Kubernetes environments on demand — a Kubernetes **operator** plus a thin **API**, built on top of **Flux**.

Dploy turns a Helm chart into a self-service, time-boxed environment: a user picks a template from the catalog, and Dploy spins up an isolated, per-user deployment with its own namespace and URL, then tears it down automatically when its TTL expires.

## Features

- **Operator + API split** — a stateless GoFiber API writes custom resources; a controller-runtime operator reconciles them. The API has **no Flux permissions** (an auditable RBAC boundary — a compromised API can't create arbitrary workloads).
- **Flux-native GitOps** — every environment is a real Flux `HelmRelease` from a Git or Helm/OCI chart source, inspectable with `flux` and `kubectl`.
- **CRD-driven** — `DployTemplate` (the catalog) and `DployInstance` (a single environment), drivable via the API or plain `kubectl`.
- **Warm pools** — pre-provision instances so users claim an environment instantly, with no cold-start wait.
- **OIDC auth** — JWT validation via JWKS; the requester's claims flow into your chart values for per-user customization.
- **Templated values & URLs** — render Helm values and connection URLs with Go templates + [sprig](https://masterminds.github.io/sprig/), using the owner, UUID, params, and claims.
- **TTL, extensions & quotas** — per-template lifetimes, `/extend`, and per-user limits; expired instances clean themselves up via a finalizer.
- **Embedded web UI** — a minimalist interface served directly by the API image.

## How it works

1. A user picks a template (the catalog is the set of enabled `DployTemplate`s) through the API or UI.
2. The API creates a `DployInstance` custom resource — it never touches Flux directly.
3. The operator reconciles it into a Flux source + `HelmRelease`, deployed into a dedicated namespace `<owner>-<template>-<uuid>`.
4. The environment is exposed at `<owner>-<uuid>.<baseDomain>` (or a custom `connectionURLTemplate`).
5. When the TTL elapses, the operator's finalizer removes the `HelmRelease` and the workload namespace.

See the [Architecture](https://dploy.dev/concepts/architecture/) docs for the full design.

## Quick start (local)

```bash
git clone https://github.com/AYDEV-FR/dploy.git
cd dploy

make setup   # Kind cluster + Flux + Dploy (operator + API)
```

Then follow the [Quick Start guide](https://dploy.dev/quick-start/) to fetch a token and launch your first environment. `make port-forward` exposes the API at `http://localhost:8080`.

## Installation (Helm)

**Prerequisites:** a Kubernetes cluster (1.30+), the Flux `source-controller` and `helm-controller`, an OIDC provider, and an Ingress or Gateway API controller.

```bash
# Dploy only needs these two Flux controllers
flux install --components=source-controller,helm-controller

# Install the operator + API + CRDs from the chart
helm install dploy ./charts/dploy \
  --namespace dploy-system --create-namespace \
  --set auth.jwksURL="https://your-oidc.com/keys" \
  --set auth.jwtIssuer="https://your-oidc.com" \
  --set auth.oidcClientID="dploy" \
  --set auth.oidcClientSecret="your-client-secret" \
  --set ingress.enabled=true \
  --set ingress.host="dploy.your-domain.com"
```

Container images are published to GHCR: `ghcr.io/aydev-fr/dploy` (API) and `ghcr.io/aydev-fr/dploy-operator` (operator). Full options are in the [Installation](https://dploy.dev/installation/) docs.

## Define a template

A `DployTemplate` adds an entry to the catalog. This one deploys the public [podinfo](https://github.com/stefanprodan/podinfo) chart on demand:

```yaml
apiVersion: dploy.dev/v1alpha1
kind: DployTemplate
metadata:
  name: podinfo
  namespace: dploy-system
spec:
  displayName: "Podinfo"
  description: "Tiny demo web app"
  enabled: true
  method: on-demand          # or "pool" for a warm pool
  chart:
    type: helm
    repoURL: https://stefanprodan.github.io/podinfo
    chart: podinfo
    targetRevision: "6.7.1"
  ttl:
    seconds: 3600
  valuesTemplate: |
    ui:
      message: "Hello {{ .Owner }} — instance {{ .UUID }}"
```

See [Templates & Instances](https://dploy.dev/concepts/templates/) for git charts, pools, parameters, and ownership.

## API endpoints

`:env` always refers to a **template name**. Protected endpoints require a JWT Bearer token.

| Method | Endpoint | Auth | Description |
|--------|----------|------|-------------|
| GET | `/health` | — | Liveness probe |
| GET | `/ready` | — | Readiness probe (checks cluster connectivity) |
| GET | `/api/environments/available` | — | List visible, enabled templates |
| GET | `/api/environments` | ✓ | List the caller's environments |
| GET | `/run/:env` | ✓ | Create/claim, or return the caller's environment |
| GET | `/run/:env/status` | ✓ | Status of the caller's environment |
| POST | `/run/:env/extend` | ✓ | Extend the TTL |
| DELETE | `/run/:env` | ✓ | Delete the caller's environment |
| GET | `/auth/login` | — | Start the OIDC authorization-code flow |
| GET | `/auth/callback` | — | OIDC provider callback |
| GET | `/auth/logout` | — | Clear client-side auth state |

Full reference: [API Endpoints](https://dploy.dev/api/endpoints/).

## Development

```bash
make build              # build the frontend + Go API binary
make build-operator     # build the operator binary
make manifests generate # regenerate CRDs + deepcopy from kubebuilder markers
make install            # apply the CRDs to the current kube context
make test               # run tests
make docker-build docker-build-operator   # build both images
```

Run `make help` for the full target list.

## Documentation

Full documentation: **https://dploy.dev**

- [Quick Start](https://dploy.dev/quick-start/) — local Kind walkthrough
- [Installation](https://dploy.dev/installation/) — Helm install and values reference
- [Configuration](https://dploy.dev/configuration/) — environment variables and the `OperatorConfig`
- [Architecture](https://dploy.dev/concepts/architecture/) — operator/API split, RBAC boundary, lifecycle
- [Templates & Instances](https://dploy.dev/concepts/templates/) — define your catalog
- [API Reference](https://dploy.dev/api/overview/) — REST API
- Deployment: [OIDC Providers](https://dploy.dev/deployment/oidc-providers/) · [TLS Certificates](https://dploy.dev/deployment/tls-certificates/) · [ExternalDNS](https://dploy.dev/deployment/external-dns/) · [Security Considerations](https://dploy.dev/deployment/security-considerations/)

## License

MIT
