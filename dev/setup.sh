#!/bin/bash
set -e

echo "🚀 Dploy - Setup complet avec Kind"
echo ""

# === Vérification des prérequis ===
echo "📋 Vérification des prérequis..."
command -v kind >/dev/null 2>&1 || { echo "❌ kind requis: https://kind.sigs.k8s.io"; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo "❌ kubectl requis"; exit 1; }
command -v helm >/dev/null 2>&1 || { echo "❌ helm requis"; exit 1; }

# Détection du runtime
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    RUNTIME="docker"
elif command -v podman >/dev/null 2>&1 && podman info >/dev/null 2>&1; then
    RUNTIME="podman"
    export KIND_EXPERIMENTAL_PROVIDER=podman
else
    echo "❌ Docker ou Podman requis"
    exit 1
fi

echo "✅ Runtime: $RUNTIME"

# === Création du cluster Kind ===
echo ""
echo "📦 Création du cluster Kind..."
if kind get clusters 2>/dev/null | grep -q "dploy-test"; then
    echo "⚠️  Cluster 'dploy-test' existe déjà"
    read -p "Le supprimer et recréer ? (y/N): " RECREATE
    if [ "$RECREATE" = "y" ] || [ "$RECREATE" = "Y" ]; then
        kind delete cluster --name dploy-test
    else
        echo "❌ Annulé"
        exit 1
    fi
fi

kind create cluster --config dev/kind-config.yaml
kubectl wait --for=condition=Ready nodes --all --timeout=300s

# === Installation NGINX Ingress ===
echo ""
echo "🌐 Installation NGINX Ingress..."
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml

# Ensure ingress controller runs on control-plane node (which has port mappings)
kubectl patch deployment ingress-nginx-controller -n ingress-nginx --type=json \
  -p='[{"op": "add", "path": "/spec/template/spec/nodeSelector", "value": {"ingress-ready": "true"}}]' 2>/dev/null || true

kubectl wait --namespace ingress-nginx \
  --for=condition=ready pod \
  --selector=app.kubernetes.io/component=controller \
  --timeout=300s

# === Installation ArgoCD ===
echo ""
echo "🔄 Installation ArgoCD..."
kubectl create namespace argocd --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
kubectl wait --namespace argocd \
  --for=condition=ready pod \
  --selector=app.kubernetes.io/name=argocd-server \
  --timeout=300s

# === Déploiement Infrastructure de Base (Cert-Manager) ===
echo ""
echo "🛡️  Déploiement infrastructure de base..."

# Cert-Manager via ArgoCD
echo "Installing Cert-Manager via ArgoCD..."
kubectl apply -f k8s/base/cert-manager.yaml

# === Déploiement Observabilité (Prometheus, Grafana) ===
echo ""
echo "📊 Installation Prometheus + Grafana..."
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts 2>/dev/null || true
helm repo update prometheus-community
helm upgrade --install kube-prometheus-stack prometheus-community/kube-prometheus-stack \
  --namespace monitoring \
  --create-namespace \
  --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false \
  --set grafana.adminPassword=admin \
  --set grafana.ingress.enabled=false \
  --set prometheus.ingress.enabled=false \
  --wait \
  --timeout 5m

# === Déploiement Dex ===
echo ""
echo "🔐 Déploiement Dex..."
kubectl create namespace dex --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f dev/manifests/dex.yaml

kubectl wait --namespace dex \
  --for=condition=ready pod \
  --selector=app=dex \
  --timeout=300s

# === Build et load de l'image Dploy ===
echo ""
echo "🐳 Build de l'image Dploy..."
cd "$(dirname "$0")/.."
$RUNTIME build -t dploy-api:local .

echo "📤 Chargement de l'image dans Kind..."
if [ "$RUNTIME" = "podman" ]; then
    export KIND_EXPERIMENTAL_PROVIDER=podman
fi
kind load docker-image dploy-api:local --name dploy-test

# === Déploiement Dploy via Helm ===
echo ""
echo "🚀 Déploiement Dploy API via Helm..."

# Apply ArgoCD AppProject (not part of chart)
kubectl apply -f k8s/appproject.yaml

# Install Dploy using Helm chart
helm upgrade --install dploy ./charts/dploy \
  --namespace dploy-system \
  --create-namespace \
  --values dev/values.yaml \
  --wait \
  --timeout 5m

# Apply ingresses for dev services (dex, argocd, grafana, prometheus)
kubectl apply -f dev/manifests/ingresses.yaml

# === Fin ===
ARGOCD_PASSWORD=$(kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath="{.data.password}" | base64 -d)

echo ""
echo "✅ Setup complet !"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "📋 Informations"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "🔐 Dex (OIDC)"
echo "   User: admin@example.com"
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
echo "📊 Observabilité"
echo "   Grafana:    http://grafana.dploy.localhost (admin / admin)"
echo "   Prometheus: http://prometheus.dploy.localhost"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🎯 Utilisation rapide"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "Tous les services sont accessibles via .localhost:"
echo "  http://dploy.localhost"
echo "  http://auth.dploy.localhost"
echo "  https://argocd.dploy.localhost"
echo "  http://grafana.dploy.localhost"
echo "  http://prometheus.dploy.localhost"
echo ""
echo "Test API:"
echo "  make get-token"
echo "  export TOKEN='...'"
echo "  make test-api"
echo ""
