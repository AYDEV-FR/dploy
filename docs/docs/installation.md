---
sidebar_position: 2
---

# Installation

This guide covers deploying Dploy to a production Kubernetes cluster.

## Prerequisites

Before installing Dploy, ensure you have:

- **Kubernetes cluster** (1.25+)
- **ArgoCD** installed and configured
- **OIDC Provider** (Authentik, Keycloak, Okta, etc.) for JWT authentication
- **Ingress Controller** (NGINX recommended)
- **kubectl** and **Helm** configured to access your cluster

## Quick Start with Kind

For local development and testing, use the automated setup script:

```bash
git clone https://github.com/AYDEV-FR/dploy.git
cd dploy
./dev/setup.sh
```

This creates a complete local environment with Kind, ArgoCD, Authentik, and Dploy.

---

## Installation with Helm (Recommended)

Helm is the recommended way to install Dploy in production.

### Option 1: Helm CLI

#### Add the Helm repository

```bash
helm repo add dploy https://aydev-fr.github.io/dploy
helm repo update
```

#### Install with minimal configuration

```bash
helm install dploy dploy/dploy \
  --namespace dploy-system \
  --create-namespace \
  --set auth.jwksURL="https://your-oidc-provider.com/keys" \
  --set auth.jwtIssuer="https://your-oidc-provider.com" \
  --set auth.oidcClientID="dploy" \
  --set auth.oidcClientSecret="your-client-secret"
```

#### Install with custom values file

Create a `values.yaml` file:

```yaml
# values.yaml
auth:
  jwksURL: "https://your-oidc-provider.com/keys"
  jwtIssuer: "https://your-oidc-provider.com"
  oidcClientID: "dploy"
  oidcClientSecret: "your-client-secret"
  oidcRedirectURL: "https://dploy.your-domain.com/auth/callback"

config:
  baseDomain: "env.your-domain.com"
  defaultTTL: 86400        # 24 hours
  maxEnvironmentsPerUser: 5

argocd:
  namespace: argocd
  project: dploy

ingress:
  enabled: true
  className: nginx
  host: dploy.your-domain.com
  tls:
    - secretName: dploy-tls
      hosts:
        - dploy.your-domain.com

environments:
  - name: webterm
    description: "Web Terminal for students"
    chart: "github.com/AYDEV-FR/dploy-charts/webshell@main"
    enabled: true
    icon: "terminal"
    ttl: 86400

  - name: vscode
    description: "VSCode in the browser"
    chart: "github.com/AYDEV-FR/dploy-charts/vscode@main"
    enabled: true
    icon: "code"
    ttl: 43200

  - name: jupyter
    description: "Jupyter Notebook"
    chart: "github.com/AYDEV-FR/dploy-charts/jupyter@main"
    enabled: true
    icon: "book"
    ttl: 86400
```

Then install:

```bash
helm install dploy dploy/dploy \
  --namespace dploy-system \
  --create-namespace \
  -f values.yaml
```

#### Upgrade an existing installation

```bash
helm upgrade dploy dploy/dploy \
  --namespace dploy-system \
  -f values.yaml
```

---

### Option 2: ArgoCD Application

Deploy Dploy using GitOps with an ArgoCD Application manifest.

#### Create the Application manifest

```yaml
# dploy-application.yaml
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
          jwksURL: "https://your-oidc-provider.com/keys"
          jwtIssuer: "https://your-oidc-provider.com"
          oidcClientID: "dploy"
          oidcClientSecret: "your-client-secret"
          oidcRedirectURL: "https://dploy.your-domain.com/auth/callback"

        config:
          baseDomain: "env.your-domain.com"
          defaultTTL: 86400
          maxEnvironmentsPerUser: 5

        argocd:
          namespace: argocd
          project: dploy

        ingress:
          enabled: true
          className: nginx
          host: dploy.your-domain.com
          tls:
            - secretName: dploy-tls
              hosts:
                - dploy.your-domain.com

        environments:
          - name: webterm
            description: "Web Terminal"
            chart: "github.com/AYDEV-FR/dploy-charts/webshell@main"
            enabled: true
            icon: "terminal"
            ttl: 86400

          - name: vscode
            description: "VSCode in browser"
            chart: "github.com/AYDEV-FR/dploy-charts/vscode@main"
            enabled: true
            icon: "code"
            ttl: 43200

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

#### Apply the Application

```bash
kubectl apply -f dploy-application.yaml
```

ArgoCD will automatically sync and deploy Dploy with the configured values.

#### Using a Git repository for values

For better GitOps practices, store your values in a Git repository:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: dploy
  namespace: argocd
spec:
  project: default
  sources:
    - repoURL: https://aydev-fr.github.io/dploy
      chart: dploy
      targetRevision: 0.1.0
      helm:
        valueFiles:
          - $values/dploy/values.yaml
    - repoURL: https://github.com/your-org/gitops-config
      targetRevision: main
      ref: values
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

---

## Helm Values Reference

### Authentication (`auth`)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `auth.jwksURL` | JWKS endpoint for JWT validation | - |
| `auth.jwtIssuer` | JWT issuer claim | - |
| `auth.jwtAudience` | JWT audience claim | `dploy` |
| `auth.jwtUsernameClaim` | JWT claim for username | `preferred_username` |
| `auth.oidcClientID` | OIDC client ID | `dploy` |
| `auth.oidcClientSecret` | OIDC client secret | - |
| `auth.oidcIssuer` | OIDC issuer URL | - |
| `auth.oidcRedirectURL` | OIDC redirect URL | - |

### Configuration (`config`)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `config.baseDomain` | Base domain for environment ingresses | `env.dploy.dev` |
| `config.defaultTTL` | Default TTL in seconds | `86400` (24h) |
| `config.extendTTL` | TTL extension in seconds | `7200` (2h) |
| `config.maxEnvironmentsPerUser` | Max environments per user | `5` |
| `config.serverHost` | Server bind address | `0.0.0.0` |
| `config.serverPort` | Server port | `8080` |

### ArgoCD (`argocd`)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `argocd.namespace` | ArgoCD namespace | `argocd` |
| `argocd.project` | ArgoCD project name | `dploy` |

### Ingress (`ingress`)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `ingress.enabled` | Enable ingress | `false` |
| `ingress.className` | Ingress class | `nginx` |
| `ingress.host` | Ingress hostname | `dploy.example.com` |
| `ingress.annotations` | Ingress annotations | `{}` |
| `ingress.tls` | TLS configuration | `[]` |

### Environments (`environments`)

| Parameter | Description | Required |
|-----------|-------------|----------|
| `environments[].name` | Environment name | Yes |
| `environments[].description` | Human-readable description | Yes |
| `environments[].chart` | Helm chart reference | Yes |
| `environments[].enabled` | Enable/disable environment | Yes |
| `environments[].icon` | Icon name for UI | No |
| `environments[].ttl` | TTL in seconds | No |
| `environments[].extraValues` | Additional Helm values | No |

See [Environments Configuration](/docs/environments) for detailed chart format.

---

## Manual Installation (Alternative)

If you prefer not to use Helm, you can deploy Dploy manually.

### 1. Create Namespace

```bash
kubectl create namespace dploy-system
```

### 2. Deploy ArgoCD AppProject

```bash
kubectl apply -f k8s/appproject.yaml
```

### 3. Deploy RBAC

```bash
kubectl apply -f k8s/rbac.yaml
```

### 4. Configure and Deploy

Edit `k8s/deployment.yaml` with your configuration, then:

```bash
kubectl apply -f k8s/deployment.yaml
```

### 5. Configure Ingress

```bash
kubectl apply -f k8s/ingress.yaml
```

---

## Verify Installation

Check that all components are running:

```bash
# Check Dploy API pods
kubectl get pods -n dploy-system

# Check health endpoint
kubectl port-forward -n dploy-system svc/dploy-api 8080:80
curl http://localhost:8080/health

# Check ArgoCD
kubectl get pods -n argocd
kubectl get applications -n argocd
```

## Docker Image

The official Docker image is available at:

```bash
docker pull ghcr.io/aydev-fr/dploy:latest
```

Or build from source:

```bash
docker build -t dploy:latest .
```

## Next Steps

- [Configuration](/docs/configuration) - Environment variables reference
- [Environments](/docs/environments) - Define available environments
- [API Reference](/docs/api/overview) - Explore the REST API
