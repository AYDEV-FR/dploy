# Dploy

All-in-one solution for managing ephemeral Kubernetes environments via ArgoCD Applications and Git-based Helm charts.

## Features

- **Embedded Web UI** - Minimalist web interface (vanilla JS)
- **Single Container** - API + Frontend in one image
- **Fast** - Built with GoFiber framework
- **Secure** - JWT/OIDC authentication via JWKS
- **Simple Config** - YAML-based environments, no CRDs
- **Git Charts** - Helm charts from Git repositories
- **GitOps Native** - ArgoCD manages deployments
- **Auto Cleanup** - Built-in TTL enforcement and quotas

## Quick Start

### Local Development (Kind)

```bash
git clone https://github.com/AYDEV-FR/dploy.git
cd dploy
./dev/setup.sh
```

Access the UI at `http://dploy.localhost`

### Production (Helm)

```bash
# Add Helm repository
helm repo add dploy https://aydev-fr.github.io/dploy
helm repo update

# Install
helm install dploy dploy/dploy \
  --namespace dploy-system \
  --create-namespace \
  --set auth.jwksURL="https://your-oidc.com/keys" \
  --set auth.jwtIssuer="https://your-oidc.com" \
  --set ingress.enabled=true \
  --set ingress.host="dploy.your-domain.com"
```

### ArgoCD Application

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: dploy
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://aydev-fr.github.io/dploy
    chart: dploy
    targetRevision: 0.1.0
    helm:
      valuesObject:
        auth:
          jwksURL: "https://your-oidc.com/keys"
          jwtIssuer: "https://your-oidc.com"
        ingress:
          enabled: true
          host: "dploy.your-domain.com"
        environments:
          - name: webterm
            description: "Web Terminal"
            chart: "github.com/AYDEV-FR/dploy-charts/webshell@main"
            enabled: true
  destination:
    server: https://kubernetes.default.svc
    namespace: dploy-system
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
```

## How It Works

Each environment request:

1. Gets a unique 8-character UUID
2. Creates an ArgoCD Application
3. Deploys to namespace: `{user}-{env}-{uuid}`
4. Gets ingress: `https://{user}-{uuid}.{BASE_DOMAIN}`
5. Auto-deletes after TTL expires

## API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/health` | Liveness probe |
| GET | `/ready` | Readiness probe |
| GET | `/api/environments/available` | List available environments |
| GET | `/api/environments` | List user's active environments |
| GET | `/run/:env` | Create or get environment |
| GET | `/run/:env/status` | Get environment status |
| POST | `/run/:env/extend` | Extend TTL |
| DELETE | `/run/:env` | Delete environment |

## Development

```bash
# Build
go build -o dploy ./cmd/api

# Run locally
export JWKS_URL=https://your-oidc.com/keys
export JWT_ISSUER=https://your-oidc.com
go run ./cmd/api/main.go

# Docker
docker build -t dploy:latest .
```

## Documentation

Full documentation available at: **https://aydev-fr.github.io/dploy**

- [Installation](https://aydev-fr.github.io/dploy/docs/installation) - Helm, ArgoCD, manual setup
- [Configuration](https://aydev-fr.github.io/dploy/docs/configuration) - Environment variables
- [Environments](https://aydev-fr.github.io/dploy/docs/environments) - Define available environments
- [API Reference](https://aydev-fr.github.io/dploy/docs/api/overview) - REST API documentation
- [Architecture](https://aydev-fr.github.io/dploy/docs/architecture) - System design

## License

MIT
