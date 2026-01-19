#!/bin/bash
set -e

echo "🚀 Dploy - Minimal Setup with Kind"
echo ""

# === Prerequisites check ===
echo "📋 Checking prerequisites..."
command -v kind >/dev/null 2>&1 || { echo "❌ kind required: https://kind.sigs.k8s.io"; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo "❌ kubectl required"; exit 1; }
command -v helm >/dev/null 2>&1 || { echo "❌ helm required"; exit 1; }

# Runtime detection
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    RUNTIME="docker"
elif command -v podman >/dev/null 2>&1 && podman info >/dev/null 2>&1; then
    RUNTIME="podman"
    export KIND_EXPERIMENTAL_PROVIDER=podman
else
    echo "❌ Docker or Podman required"
    exit 1
fi

echo "✅ Runtime: $RUNTIME"

# === Create Kind cluster ===
echo ""
echo "📦 Creating Kind cluster..."
if kind get clusters 2>/dev/null | grep -q "dploy-test"; then
    echo "⚠️  Cluster 'dploy-test' already exists"
    read -p "Delete and recreate? (y/N): " RECREATE
    if [ "$RECREATE" = "y" ] || [ "$RECREATE" = "Y" ]; then
        kind delete cluster --name dploy-test
    else
        echo "❌ Cancelled"
        exit 1
    fi
fi

kind create cluster --config dev/kind-config.yaml
kubectl wait --for=condition=Ready nodes --all --timeout=300s

# === Install NGINX Ingress ===
echo ""
echo "🌐 Installing NGINX Ingress..."
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml

# Ensure ingress controller runs on control-plane node (which has port mappings)
kubectl patch deployment ingress-nginx-controller -n ingress-nginx --type=json \
  -p='[{"op": "add", "path": "/spec/template/spec/nodeSelector", "value": {"ingress-ready": "true"}}]' 2>/dev/null || true

kubectl wait --namespace ingress-nginx \
  --for=condition=ready pod \
  --selector=app.kubernetes.io/component=controller \
  --timeout=300s

# === Install ArgoCD ===
echo ""
echo "🔄 Installing ArgoCD..."
kubectl create namespace argocd --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
kubectl wait --namespace argocd \
  --for=condition=ready pod \
  --selector=app.kubernetes.io/name=argocd-server \
  --timeout=300s

# === Deploy Authentik ===
echo ""
echo "🔐 Deploying Authentik..."
kubectl create namespace authentik --dry-run=client -o yaml | kubectl apply -f -

# Apply blueprints ConfigMap first
kubectl apply -f dev/manifests/authentik-blueprints.yaml

# Add Authentik Helm repo
helm repo add authentik https://charts.goauthentik.io 2>/dev/null || true
helm repo update authentik

# Install Authentik using Helm
helm upgrade --install authentik authentik/authentik \
  --namespace authentik \
  --values dev/authentik-values.yaml \
  --wait \
  --timeout 10m

echo "⏳ Waiting for Authentik to be ready..."
kubectl wait --namespace authentik \
  --for=condition=ready pod \
  --selector=app.kubernetes.io/name=authentik \
  --selector=app.kubernetes.io/component=server \
  --timeout=300s

# === Build and load Dploy image ===
echo ""
echo "🐳 Building Dploy image..."
cd "$(dirname "$0")/.."
$RUNTIME build -t dploy-api:local .

echo "📤 Loading image into Kind..."
if [ "$RUNTIME" = "podman" ]; then
    export KIND_EXPERIMENTAL_PROVIDER=podman
fi
kind load docker-image dploy-api:local --name dploy-test

# === Deploy Dploy via Helm ===
echo ""
echo "🚀 Deploying Dploy API via Helm..."

# Apply ArgoCD AppProject (not part of chart)
kubectl apply -f k8s/appproject.yaml

# Install Dploy using Helm chart
helm upgrade --install dploy ./charts/dploy \
  --namespace dploy-system \
  --create-namespace \
  --values dev/values.yaml \
  --wait \
  --timeout 5m

# Apply ingresses for dev services (authentik, argocd - skip grafana/prometheus)
kubectl apply -f dev/manifests/ingresses.yaml 2>/dev/null || true

# === Done ===
ARGOCD_PASSWORD=$(kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath="{.data.password}" | base64 -d)

echo ""
echo "✅ Minimal setup complete!"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "📋 Information"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "🔐 Authentik (OIDC)"
echo "   URL:  http://auth.dploy.localhost"
echo "   User: akadmin"
echo "   Pass: password"
echo ""
echo "🔄 ArgoCD UI"
echo "   User: admin"
echo "   Pass: $ARGOCD_PASSWORD"
echo "   URL:  https://argocd.dploy.localhost"
echo ""
echo "🚀 Dploy API"
echo "   URL:  http://dploy.localhost"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🎯 Quick usage"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "Services accessible via .localhost:"
echo "  http://dploy.localhost"
echo "  http://auth.dploy.localhost"
echo "  https://argocd.dploy.localhost"
echo ""
echo "Test API:"
echo "  make get-token"
echo "  export TOKEN='...'"
echo "  make test-api"
echo ""
