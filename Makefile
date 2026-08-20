.PHONY: help build build-go build-operator test envtest docker-build docker-build-operator docker-load logs port-forward clean frontend-install frontend-build frontend-dev manifests generate install uninstall

# Detect container runtime
CONTAINER_RUNTIME := $(shell command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 && echo docker || echo podman)

# Operator codegen (controller-gen is run via `go run` so no global install is required)
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.20.1

# envtest control-plane binaries for the controller suite. The version tracks the
# k8s.io/* libraries in go.mod; setup-envtest tracks controller-runtime's branch.
ENVTEST_K8S_VERSION ?= 1.34.x
SETUP_ENVTEST ?= go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.24
ENVTEST_DIR := $(CURDIR)/bin/envtest

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

envtest: ## Download the envtest control-plane binaries into bin/envtest
	$(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(ENVTEST_DIR) -p path

test: envtest ## Run tests (the controller suite needs the envtest binaries)
	go test ./...

# Docker/Podman
docker-build: ## Build API container image
	@echo "Building with $(CONTAINER_RUNTIME)..."
	$(CONTAINER_RUNTIME) build -t dploy-api:local .

docker-build-operator: ## Build operator container image
	@echo "Building operator with $(CONTAINER_RUNTIME)..."
	$(CONTAINER_RUNTIME) build -f Dockerfile.operator -t dploy-operator:local .

docker-load: docker-build docker-build-operator ## Build and load both images into Kind
	@echo "Loading images into Kind with $(CONTAINER_RUNTIME)..."
	@if [ "$(CONTAINER_RUNTIME)" = "podman" ]; then \
		export KIND_EXPERIMENTAL_PROVIDER=podman; \
	fi && \
	kind load docker-image dploy-api:local --name dploy-test && \
	kind load docker-image dploy-operator:local --name dploy-test

# Kind cluster
setup: ## Complete setup from scratch (Kind cluster + Dploy)
	./dev/setup.sh

cluster-delete: ## Delete Kind cluster
	kind delete cluster --name dploy-test

cluster-recreate: cluster-delete setup ## Delete and recreate cluster

# Deploy
deploy: docker-load ## Build, load and deploy via Helm (fast iteration)
	helm upgrade --install dploy ./charts/dploy \
		--namespace dploy-system \
		--values dev/values.yaml \
		--wait \
		--timeout 2m

# Helpers
logs: ## Show API logs
	kubectl logs -n dploy-system -l app.kubernetes.io/name=dploy -f

port-forward: ## Port-forward to API (use with http://localhost:8080)
	@echo "⚠️  With the dev cluster up, use http://dploy.localhost instead"
	kubectl port-forward -n dploy-system svc/dploy 8080:80

port-forward-dex: ## Port-forward to Dex (use with http://localhost:5556)
	@echo "⚠️  The dev issuer is http://auth.dploy.localhost/dex — a port-forward bypasses it,"
	@echo "   so tokens fetched this way carry a different 'iss' and the API will reject them."
	kubectl port-forward -n dex svc/dex 5556:5556

get-token: ## Print an id_token from the dev Dex (EMAIL=user@dploy.localhost for the non-admin)
	@EMAIL=$${EMAIL:-admin@dploy.localhost}; \
	TOKEN=$$(curl -s http://auth.dploy.localhost/dex/token \
	  -d grant_type=password \
	  -d client_id=dploy -d client_secret=dploy-secret \
	  -d username="$$EMAIL" -d password=password \
	  -d scope="openid profile email" | jq -r '.id_token // empty'); \
	if [ -z "$$TOKEN" ]; then echo "❌ no token — is the cluster up? (make setup)"; exit 1; fi; \
	echo "$$TOKEN"

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
	@curl -s -H "Authorization: Bearer $$TOKEN" http://localhost:8080/run/podinfo | jq .

# Cleanup
clean: ## Clean build artifacts
	rm -f dploy-api

restart: ## Restart API deployment
	kubectl rollout restart deployment/dploy -n dploy-system
	kubectl rollout status deployment/dploy -n dploy-system
