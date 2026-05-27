---
title: Development
description: Set up a local environment to develop the Dploy operator and API.
---

## Prerequisites

- Go 1.26+
- Docker or Podman
- Kind (Kubernetes in Docker)
- kubectl and Helm 3
- Flux controllers (`source-controller`, `helm-controller`) in your dev cluster

## Quick start

```bash
git clone https://github.com/AYDEV-FR/dploy.git
cd dploy
./dev/setup.sh          # provisions a local Kind cluster with the supporting stack
```

Then install the Flux controllers and the chart:

```bash
flux install --components=source-controller,helm-controller
make install            # apply the CRDs
make docker-build docker-build-operator
# load images into Kind, then helm install ./charts/dploy ...
```

## Make targets

```bash
# Build
make build              # API binary (+ frontend)
make build-go           # API binary only
make build-operator     # operator binary

# Code generation (run after editing api/v1alpha1 types)
make manifests          # regenerate CRDs + RBAC role
make generate           # regenerate deepcopy

# CRDs
make install            # kubectl apply -f config/crd/bases
make uninstall

# Containers
make docker-build           # API image (dploy-api:local)
make docker-build-operator  # operator image (dploy-operator:local)

# Test
make test               # go test ./...
```

## Running locally

The API and operator both fall back to your kubeconfig when not running in-cluster.

```bash
# Operator (watches the cluster in your current context)
go run ./cmd/operator

# API (needs the auth env vars set)
export JWKS_URL=... JWT_ISSUER=... DPLOY_NAMESPACE=dploy-system
go run ./cmd/api
```

## Project structure

```text
dploy/
├── cmd/
│   ├── api/                 # API server entrypoint
│   └── operator/            # operator (manager) entrypoint
├── api/v1alpha1/            # CRD types: DployTemplate, DployInstance, OperatorConfig
├── internal/
│   ├── auth/                # JWT/OIDC validation + middleware
│   ├── config/              # API env-var config
│   ├── handlers/            # HTTP handlers (run, environments, health)
│   ├── kube/                # API's CR client (no Flux)
│   ├── controller/          # reconcilers + Flux builders
│   ├── operatorconfig/      # OperatorConfig resolver
│   ├── templating/          # Go text/template + sprig renderer
│   ├── logger/
│   └── models/              # API response types
├── charts/dploy/            # Helm chart (operator + API + CRDs)
├── config/{crd,rbac}/       # generated CRDs and operator RBAC
├── web/                     # React + TypeScript frontend
├── Dockerfile               # API image
├── Dockerfile.operator      # operator image
└── Makefile
```

## Code overview

### Operator (`cmd/operator`, `internal/controller`)

- `dployinstance_controller.go` — renders templates, ensures the Flux source + `HelmRelease`,
  projects status, enforces TTL, finalizer teardown.
- `dploytemplate_controller.go` — warm-pool maintenance + occupancy status.
- `operatorconfig_controller.go` — observes the cluster-scoped config singleton.
- `flux.go` — builds `GitRepository`/`HelmRepository`/`HelmRelease`, translates readiness.

### API (`cmd/api`, `internal/handlers`, `internal/kube`)

- `handlers/run.go` — create/claim, status, extend, delete.
- `handlers/environments.go` — catalog + the user's instances.
- `kube/client.go` — typed controller-runtime client over the dploy CRs (no Flux access).

## Tests

```bash
go test ./...
```

Unit tests cover the pure logic: templating, the `OperatorConfig` resolver, naming/sanitization,
status/phase mapping, and TTL helpers.

## Version coupling

`controller-runtime` is pinned to the `k8s.io/*` minor version (currently `0.24 ↔ k8s 0.36`). If
you bump the Kubernetes libraries in `go.mod`, bump controller-runtime's minor to match, and the
Go version in the Dockerfiles and CI workflows alongside `go.mod`.
