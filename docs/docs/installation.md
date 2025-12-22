---
sidebar_position: 2
---

# Installation

This guide covers deploying Dploy to a production Kubernetes cluster.

## Prerequisites

Before installing Dploy, ensure you have:

- **Kubernetes cluster** (1.25+)
- **ArgoCD** installed and configured
- **OIDC Provider** (Dex, Keycloak, Okta, etc.) for JWT authentication
- **Ingress Controller** (NGINX recommended)
- **kubectl** configured to access your cluster

## Quick Start with Kind

For local development and testing, use the automated setup script:

```bash
git clone https://github.com/AYDEV-FR/dploy.git
cd dploy
./dev/setup.sh
```

This creates a complete local environment with Kind, ArgoCD, Dex, and Dploy.

## Production Installation

### 1. Create Namespace

```bash
kubectl create namespace dploy-system
```

### 2. Deploy ArgoCD AppProject

The AppProject defines which Git repositories and Kubernetes resources Dploy can manage:

```bash
kubectl apply -f k8s/appproject.yaml
```

Review and customize `k8s/appproject.yaml` to restrict source repositories:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: dploy
  namespace: argocd
spec:
  description: Dploy ephemeral environments
  sourceRepos:
    - 'https://github.com/your-org/helm-charts'  # Restrict to your repos
  destinations:
    - namespace: '*'
      server: https://kubernetes.default.svc
  clusterResourceWhitelist:
    - group: ''
      kind: Namespace
  namespaceResourceWhitelist:
    - group: '*'
      kind: '*'
```

### 3. Deploy RBAC

```bash
kubectl apply -f k8s/rbac.yaml
```

### 4. Configure Secrets

Create the ConfigMap and Secret with your OIDC configuration:

```bash
kubectl apply -f k8s/configmaps.yaml
```

Edit the secrets in `k8s/configmaps.yaml` with your values:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: dploy-api-secrets
  namespace: dploy-system
type: Opaque
stringData:
  JWKS_URL: "https://your-oidc-provider.com/keys"
  JWT_ISSUER: "https://your-oidc-provider.com"
  OIDC_ISSUER: "https://your-oidc-provider.com"
  OIDC_CLIENT_ID: "dploy"
  OIDC_CLIENT_SECRET: "your-client-secret"
  OIDC_REDIRECT_URL: "https://dploy.your-domain.com/auth/callback"
```

### 5. Configure Environments

Edit the environments ConfigMap to define available environments:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: dploy-environments
  namespace: dploy-system
data:
  environments.yaml: |
    environments:
      - name: webterm
        description: "Web Terminal"
        chart: "github.com/your-org/charts/webterm@main"
        enabled: true
        icon: "terminal"
        ttl: 86400
```

See [Environments Configuration](/docs/environments) for detailed options.

### 6. Deploy the API

```bash
kubectl apply -f k8s/deployment.yaml
```

### 7. Configure Ingress

```bash
kubectl apply -f k8s/ingress.yaml
```

Customize the ingress for your domain:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: dploy-api
  namespace: dploy-system
  annotations:
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
spec:
  ingressClassName: nginx
  tls:
    - hosts:
        - dploy.your-domain.com
      secretName: dploy-tls
  rules:
    - host: dploy.your-domain.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: dploy-api
                port:
                  number: 80
```

## Verify Installation

Check that all components are running:

```bash
# Check Dploy API
kubectl get pods -n dploy-system

# Check health endpoint
kubectl port-forward -n dploy-system svc/dploy-api 8080:80
curl http://localhost:8080/health

# Check ArgoCD
kubectl get pods -n argocd
```

## Docker Image

The official Docker image is available at:

```bash
docker pull ghcr.io/aydev-fr/dploy-api:latest
```

Or build from source:

```bash
docker build -t dploy-api:latest .
```

## Next Steps

- [Configuration](/docs/configuration) - Configure environment variables
- [Environments](/docs/environments) - Define available environments
- [API Reference](/docs/api/overview) - Explore the REST API
