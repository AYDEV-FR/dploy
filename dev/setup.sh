#!/bin/bash
# Local development cluster: Kind + NGINX + Flux + Dex + Dploy (operator + API).
#
# The dev stack mirrors the supported production topology rather than a
# shortcut of it: Flux is the engine, the operator owns the environments, and
# the API only files claims. What is dev-specific is the identity provider
# (Dex with static users) and the images (built locally, loaded into Kind).
set -euo pipefail

CLUSTER_NAME="dploy-test"
DEX_HOST="auth.dploy.localhost"
INGRESS_SVC="ingress-nginx-controller.ingress-nginx.svc.cluster.local"

cd "$(dirname "$0")/.."

echo "🚀 Dploy — local Kind setup"
echo ""

# === Prerequisites ===
echo "📋 Checking prerequisites..."
command -v kind >/dev/null 2>&1    || { echo "❌ kind required: https://kind.sigs.k8s.io"; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo "❌ kubectl required"; exit 1; }
command -v helm >/dev/null 2>&1    || { echo "❌ helm required"; exit 1; }
command -v flux >/dev/null 2>&1    || { echo "❌ flux CLI required: https://fluxcd.io/flux/installation/"; exit 1; }

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

# === Kind cluster ===
echo ""
echo "📦 Creating Kind cluster..."
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
    echo "⚠️  Cluster '${CLUSTER_NAME}' already exists"
    read -r -p "Delete and recreate? (y/N): " RECREATE
    if [ "$RECREATE" = "y" ] || [ "$RECREATE" = "Y" ]; then
        kind delete cluster --name "$CLUSTER_NAME"
    else
        echo "❌ Cancelled"
        exit 1
    fi
fi

kind create cluster --config dev/kind-config.yaml
kubectl wait --for=condition=Ready nodes --all --timeout=300s

# === NGINX Ingress ===
echo ""
echo "🌐 Installing NGINX Ingress..."
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml

# The control-plane node is the one carrying the host port mappings.
kubectl patch deployment ingress-nginx-controller -n ingress-nginx --type=json \
  -p='[{"op": "add", "path": "/spec/template/spec/nodeSelector", "value": {"ingress-ready": "true"}}]' 2>/dev/null || true

kubectl wait --namespace ingress-nginx \
  --for=condition=ready pod \
  --selector=app.kubernetes.io/component=controller \
  --timeout=300s

# === Flux ===
# Dploy drives Flux; it does not need Flux's own GitOps machinery, so only the
# two controllers that materialize a HelmRelease are installed.
echo ""
echo "🔄 Installing Flux (source-controller + helm-controller)..."
flux install --components=source-controller,helm-controller

# === Dex (dev OIDC provider) ===
echo ""
echo "🔐 Deploying Dex..."
helm repo add dex https://charts.dexidp.io 2>/dev/null || true
helm repo update dex
helm upgrade --install dex dex/dex \
  --namespace dex \
  --create-namespace \
  --values dev/dex-values.yaml \
  --wait \
  --timeout 5m

kubectl apply -f dev/manifests/ingresses.yaml

# Dex is configured with a single issuer — the public ingress URL — so the token
# `iss` is the same whether the browser or the API is talking. That only works
# if the in-cluster resolver sends that hostname to the ingress controller too,
# which is what this rewrite does; without it the API cannot reach the issuer it
# is configured to trust, and the split-horizon fallback would be needed instead.
echo ""
echo "🧭 Pointing in-cluster DNS for ${DEX_HOST} at the ingress controller..."
if kubectl -n kube-system get configmap coredns -o jsonpath='{.data.Corefile}' | grep -q "rewrite name ${DEX_HOST} "; then
    echo "   rewrite already present, leaving CoreDNS alone"
else
    COREFILE=$(mktemp)
    kubectl -n kube-system get configmap coredns -o jsonpath='{.data.Corefile}' \
      | awk -v rule="    rewrite name ${DEX_HOST} ${INGRESS_SVC}" '
          { print }
          !done && /^[[:space:]]*\.:53[[:space:]]*\{/ { print rule; done = 1 }
        ' > "$COREFILE"
    kubectl -n kube-system create configmap coredns \
      --from-file=Corefile="$COREFILE" --dry-run=client -o yaml \
      | kubectl -n kube-system apply -f -
    rm -f "$COREFILE"
    kubectl -n kube-system rollout restart deployment coredns
    kubectl -n kube-system rollout status deployment coredns --timeout=120s
fi

# === Images ===
echo ""
echo "🐳 Building the API and operator images..."
$RUNTIME build -t dploy-api:local -f Dockerfile .
$RUNTIME build -t dploy-operator:local -f Dockerfile.operator .

echo "📤 Loading images into Kind..."
kind load docker-image dploy-api:local --name "$CLUSTER_NAME"
kind load docker-image dploy-operator:local --name "$CLUSTER_NAME"

# === Dploy ===
# CRDs ship in the chart's crds/ directory, so this installs them too.
echo ""
echo "🚀 Installing Dploy (operator + API)..."
helm upgrade --install dploy ./charts/dploy \
  --namespace dploy-system \
  --create-namespace \
  --values dev/values.yaml \
  --wait \
  --timeout 5m

# The catalog is CRs now, not chart values: an OperatorConfig singleton holding
# the cluster defaults, plus one DployTemplate per catalog entry.
echo ""
echo "📚 Applying the dev OperatorConfig and catalog..."
kubectl apply -f dev/manifests/dploy.yaml

# === Done ===
echo ""
echo "✅ Setup complete!"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🔐 Dex (OIDC)          http://${DEX_HOST}/dex"
echo "   admin@dploy.localhost / password   (admin)"
echo "   user@dploy.localhost  / password"
echo ""
echo "🚀 Dploy               http://dploy.localhost"
echo "   environments are exposed at *.env.dploy.localhost"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "Catalog:      kubectl get dploytemplate -n dploy-system"
echo "Claims:       kubectl get dclaim -n dploy-system -w"
echo "Environments: kubectl get dployinstance -n dploy-system"
echo ""
echo "Token for curl:  make get-token"
echo "Then:            export TOKEN='...' && make test-api"
echo ""
