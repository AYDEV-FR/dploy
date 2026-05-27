.PHONY: help build build-go build-operator test docker-build docker-build-operator docker-load logs port-forward clean frontend-install frontend-build frontend-dev manifests generate install uninstall

# Detect container runtime
CONTAINER_RUNTIME := $(shell command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 && echo docker || echo podman)

# Operator codegen (controller-gen is run via `go run` so no global install is required)
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.20.1

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# Frontend
frontend-install: ## Install frontend dependencies
	cd web && npm ci

frontend-build: ## Build frontend
	cd web && npm run build

frontend-dev: ## Run frontend dev server
	cd web && npm run dev

# Development
build: frontend-build ## Build the Go binary (with frontend)
	go build -o dploy-api ./cmd/api

build-go: ## Build only the Go binary (skip frontend)
	go build -o dploy-api ./cmd/api

build-operator: ## Build the operator binary
	go build -o dploy-operator ./cmd/operator

# Operator code generation
manifests: ## Generate CRD manifests and RBAC from kubebuilder markers
	$(CONTROLLER_GEN) crd paths=./api/... output:crd:dir=config/crd/bases
	$(CONTROLLER_GEN) rbac:roleName=dploy-operator paths=./internal/controller/... output:rbac:dir=config/rbac

generate: ## Generate deepcopy (zz_generated.deepcopy.go) from kubebuilder markers
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths=./api/...

install: manifests ## Install CRDs into the current kube context
	kubectl apply -f config/crd/bases

uninstall: ## Remove CRDs from the current kube context
	kubectl delete -f config/crd/bases --ignore-not-found

run: ## Run locally (requires env vars)
	go run ./cmd/api/main.go

test: ## Run tests
	go test -v ./...

# Docker/Podman
docker-build: ## Build API container image
	@echo "Building with $(CONTAINER_RUNTIME)..."
	$(CONTAINER_RUNTIME) build -t dploy-api:local .

docker-build-operator: ## Build operator container image
	@echo "Building operator with $(CONTAINER_RUNTIME)..."
	$(CONTAINER_RUNTIME) build -f Dockerfile.operator -t dploy-operator:local .

docker-load: docker-build ## Build and load image into Kind
	@echo "Loading image into Kind with $(CONTAINER_RUNTIME)..."
	@if [ "$(CONTAINER_RUNTIME)" = "podman" ]; then \
		export KIND_EXPERIMENTAL_PROVIDER=podman; \
	fi && \
	kind load docker-image dploy-api:local --name dploy-test

# Kind cluster
setup: ## Complete setup from scratch (Kind cluster + Dploy)
	./dev/setup.sh

cluster-delete: ## Delete Kind cluster
	kind delete cluster --name dploy-test

cluster-recreate: cluster-delete setup ## Delete and recreate cluster

# DNS Setup
setup-dns: ## Setup local DNS (*.dploy.dev → 127.0.0.1)
	./dev/setup-dns.sh

# Deploy
deploy: docker-load ## Build, load and deploy via Helm (fast iteration)
	helm upgrade --install dploy ./charts/dploy \
		--namespace dploy-system \
		--values dev/values.yaml \
		--wait \
		--timeout 2m

deploy-manifests: docker-load ## Deploy using raw k8s manifests (legacy)
	kubectl apply -f k8s/

# Helpers
logs: ## Show API logs
	kubectl logs -n dploy-system -l app.kubernetes.io/name=dploy -f

port-forward: ## Port-forward to API (use with http://localhost:8080)
	@echo "⚠️  With DNS setup, use http://dploy.dev instead"
	kubectl port-forward -n dploy-system svc/dploy 8080:80

port-forward-authentik: ## Port-forward to Authentik (use with http://localhost:9000)
	@echo "⚠️  With DNS setup, use http://auth.dploy.localhost instead"
	kubectl port-forward -n authentik svc/authentik-server 9000:80

port-forward-argocd: ## Port-forward to ArgoCD UI
	@echo "ArgoCD admin password:"
	@kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath="{.data.password}" | base64 -d
	@echo ""
	@echo "Opening port-forward on https://localhost:8081"
	kubectl port-forward svc/argocd-server -n argocd 8081:443

get-token: ## Get JWT token from Authentik (interactive browser login)
	@echo "To get a token, visit: http://dploy.localhost"
	@echo "Click 'Login' and authenticate with:"
	@echo "  User: akadmin"
	@echo "  Pass: password"
	@echo ""
	@echo "The token will be stored in your browser."
	@echo "To use it with curl, open browser DevTools > Application > Local Storage"
	@echo "and copy the 'token' value."

# Testing
test-health: ## Test health endpoints
	@echo "Testing health endpoint..."
	@curl -s http://localhost:8080/health | jq .
	@echo ""
	@echo "Testing ready endpoint..."
	@curl -s http://localhost:8080/ready | jq .

test-api: ## Test API with token (requires TOKEN env var)
	@if [ -z "$$TOKEN" ]; then echo "❌ TOKEN not set. Run: make get-token"; exit 1; fi
	@echo "Testing /api/environments/available..."
	@curl -s http://localhost:8080/api/environments/available | jq .
	@echo ""
	@echo "Testing /api/environments (auth)..."
	@curl -s -H "Authorization: Bearer $$TOKEN" http://localhost:8080/api/environments | jq .
	@echo ""
	@echo "Creating environment (auth)..."
	@curl -s -H "Authorization: Bearer $$TOKEN" http://localhost:8080/run/webterm | jq .

# Cleanup
clean: ## Clean build artifacts
	rm -f dploy-api

restart: ## Restart API deployment
	kubectl rollout restart deployment/dploy -n dploy-system
	kubectl rollout status deployment/dploy -n dploy-system
