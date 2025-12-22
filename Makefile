.PHONY: help build test docker-build docker-load logs port-forward clean

# Detect container runtime
CONTAINER_RUNTIME := $(shell command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 && echo docker || echo podman)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# Development
build: ## Build the Go binary
	go build -o dploy-api ./cmd/api

run: ## Run locally (requires env vars)
	go run ./cmd/api/main.go

test: ## Run tests
	go test -v ./...

# Docker/Podman
docker-build: ## Build container image
	@echo "Building with $(CONTAINER_RUNTIME)..."
	$(CONTAINER_RUNTIME) build -t dploy-api:local .

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
deploy: docker-load ## Build, load and deploy (fast iteration)
	kubectl apply -f k8s/configmaps.yaml
	kubectl apply -f k8s/deployment.yaml
	kubectl rollout restart deployment/dploy-api -n dploy-system
	kubectl rollout status deployment/dploy-api -n dploy-system

deploy-all: docker-load ## Deploy all k8s resources
	kubectl apply -f k8s/

# Helpers
logs: ## Show API logs
	kubectl logs -n dploy-system -l app=dploy-api -f

port-forward: ## Port-forward to API (use with http://localhost:8080)
	@echo "⚠️  With DNS setup, use http://dploy.dev instead"
	kubectl port-forward -n dploy-system svc/dploy-api 8080:80

port-forward-dex: ## Port-forward to Dex (use with http://localhost:8082)
	@echo "⚠️  With DNS setup, use http://auth.dploy.dev instead"
	kubectl port-forward -n dex svc/dex 8082:5556

port-forward-argocd: ## Port-forward to ArgoCD UI
	@echo "ArgoCD admin password:"
	@kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath="{.data.password}" | base64 -d
	@echo ""
	@echo "Opening port-forward on https://localhost:8081"
	kubectl port-forward svc/argocd-server -n argocd 8081:443

get-token: ## Get JWT token from Dex
	./dev/get-token.sh

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
	kubectl rollout restart deployment/dploy-api -n dploy-system
	kubectl rollout status deployment/dploy-api -n dploy-system
