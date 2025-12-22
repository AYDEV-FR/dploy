---
sidebar_position: 7
---

# Development Guide

This guide covers setting up a local development environment for Dploy.

## Prerequisites

- Go 1.23+
- Docker or Podman
- Kind (Kubernetes in Docker)
- kubectl
- Helm 3

## Quick Setup

The fastest way to get started:

```bash
git clone https://github.com/AYDEV-FR/dploy.git
cd dploy
./dev/setup.sh
```

This script automatically:

1. Creates a Kind cluster with 3 nodes
2. Installs NGINX Ingress Controller
3. Installs ArgoCD
4. Installs Cert-Manager
5. Deploys Prometheus + Grafana
6. Deploys Dex (OIDC provider)
7. Builds and loads the Dploy image
8. Deploys all Dploy manifests

## Manual Setup

### 1. Create Kind Cluster

```bash
kind create cluster --config dev/kind-config.yaml
```

The config creates a cluster named `dploy-test` with port mappings for HTTP/HTTPS.

### 2. Install Dependencies

```bash
# NGINX Ingress
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml

# ArgoCD
kubectl create namespace argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
```

### 3. Deploy Dex

```bash
kubectl create namespace dex
kubectl apply -f dev/manifests/dex.yaml
```

Default credentials:
- Email: `admin@example.com`
- Password: `password`

### 4. Build and Load Image

```bash
# Build
docker build -t dploy-api:local .

# Load into Kind
kind load docker-image dploy-api:local --name dploy-test
```

### 5. Deploy Dploy

```bash
kubectl apply -f k8s/rbac.yaml
kubectl apply -f k8s/configmaps.yaml
kubectl apply -f k8s/deployment.yaml
kubectl apply -f k8s/appproject.yaml
kubectl apply -f k8s/ingress.yaml
```

## Development Cycle

### Make Commands

```bash
# Build Go binary
make build

# Run locally (requires exported env vars)
make run

# Build Docker image
make docker-build

# Load image into Kind
make docker-load

# Restart API pods (picks up new image)
make restart

# View logs
make logs

# Port-forward to API
make port-forward

# Port-forward to ArgoCD UI
make port-forward-argocd
```

### Testing

```bash
# Get a JWT token from Dex
make get-token
export TOKEN='...'

# Test health endpoints
make test-health

# Test all API endpoints
make test-api
```

### Manual API Testing

```bash
# Health check
curl http://localhost:8080/health

# List environments (no auth)
curl http://localhost:8080/api/environments/available

# Create environment
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/run/webterm

# Check status
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/run/webterm/status

# Extend TTL
curl -X POST -H "Authorization: Bearer $TOKEN" http://localhost:8080/run/webterm/extend

# Delete
curl -X DELETE -H "Authorization: Bearer $TOKEN" http://localhost:8080/run/webterm
```

## Project Structure

```
dploy/
├── cmd/api/              # Application entry point
│   └── main.go
├── internal/             # Core business logic
│   ├── auth/            # JWT/OIDC handlers
│   ├── config/          # Configuration loading
│   ├── handlers/        # HTTP request handlers
│   ├── kube/            # Kubernetes client
│   └── models/          # Data structures
├── config/              # Environment definitions
│   └── environments.yaml
├── web/                 # Frontend (vanilla JS)
│   ├── index.html
│   ├── run.html
│   ├── app.js
│   └── style.css
├── k8s/                 # Kubernetes manifests
├── dev/                 # Development utilities
│   ├── setup.sh
│   ├── kind-config.yaml
│   └── manifests/
├── docs/                # Documentation (Docusaurus)
├── Dockerfile
├── Makefile
├── go.mod
└── go.sum
```

## Code Overview

### Entry Point (`cmd/api/main.go`)

- Initializes Fiber web framework
- Loads configuration
- Sets up routes and middleware
- Starts server

### Handlers (`internal/handlers/`)

- `run.go`: Create, status, extend, delete environments
- `environments.go`: List available and user environments
- `health.go`: Health and readiness probes

### Kubernetes Client (`internal/kube/client.go`)

- Dynamic client for ArgoCD Application CRDs
- Creates applications with labels/annotations
- Generates unique names and URLs

### Authentication (`internal/auth/`)

- `jwt.go`: JWKS fetching and JWT validation
- `middleware.go`: Fiber middleware for auth
- `oidc.go`: OIDC authorization code flow

## Running Tests

```bash
go test ./...
```

## Building for Production

```bash
# Build optimized binary
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o dploy-api ./cmd/api

# Build multi-arch Docker image
docker buildx build --platform linux/amd64,linux/arm64 -t dploy-api:latest .
```

## Debugging

### View ArgoCD Applications

```bash
kubectl get applications -n argocd
kubectl describe application <name> -n argocd
```

### Check Dploy Logs

```bash
kubectl logs -n dploy-system -l app=dploy-api -f
```

## Cleanup

```bash
# Delete Kind cluster
make cluster-delete

# Or manually
kind delete cluster --name dploy-test
```

## URLs (Local Development)

| Service | URL |
|---------|-----|
| Dploy UI | http://dploy.localhost |
| Dploy API | http://dploy.localhost/api |
| Dex Auth | http://auth.dploy.localhost |
| ArgoCD | https://argocd.dploy.localhost |
| Grafana | http://grafana.dploy.localhost |
| Prometheus | http://prometheus.dploy.localhost |

ArgoCD password:
```bash
kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath="{.data.password}" | base64 -d
```
