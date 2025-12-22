# Dploy API

All-in-one container pour gérer des environnements Kubernetes éphémères via ArgoCD Applications et Helm charts Git.

## Features

- **🎨 Web UI embarquée** - Interface web minimaliste intégrée (vanilla JS)
- **🚀 Un seul container** - API + Frontend dans la même image
- **⚡ GoFiber** - Framework HTTP rapide et performant
- **🔐 JWT authentication** via JWKS
- **📝 Configuration YAML** pour les environnements (pas de CRD)
- **📦 Git-based charts** - Helm charts depuis repository Git
- **🔄 Gestion automatique** des ArgoCD Applications
- **📊 Quotas & TTL** par utilisateur
- **☸️ Client-go** pour Kubernetes

## Stack technique

- Go 1.23+
- GoFiber v2
- client-go (Kubernetes)
- golang-jwt
- gopkg.in/yaml.v3

## Quick Start (Test en local)

### Setup complet avec Kind (recommandé pour tester)

```bash
# Setup complet automatique
./dev/setup.sh

# Tester
make port-forward      # Terminal 1
make get-token         # Terminal 2, puis export TOKEN='...'
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/run/webterm
```

Voir [QUICKSTART.md](QUICKSTART.md) pour plus de détails.

## Prerequisites (Production)

- Kubernetes cluster avec ArgoCD installé
- Dex ou provider OIDC similaire pour JWT

## Installation (Production)

### 1. Déployer les manifestes K8s

```bash
kubectl apply -f k8s/appproject.yaml
kubectl apply -f k8s/rbac.yaml
```

### 2. Configurer les secrets

Éditez `k8s/deployment.yaml` et mettez à jour le Secret avec votre JWKS URL et JWT issuer:

```yaml
stringData:
  JWKS_URL: "https://your-dex-instance.com/keys"
  JWT_ISSUER: "https://your-dex-instance.com"
```

### 3. Configurer les environnements disponibles

Éditez le ConfigMap `dploy-environments` dans `k8s/deployment.yaml` pour définir vos charts Helm.

Les charts utilisent le format `github.com/org/repo/path/to/chart@revision`.

```yaml
data:
  environments.yaml: |
    environments:
      # Git Helm chart examples
      - name: webterm
        description: "Terminal web pour étudiants"
        chart: "github.com/AYDEV-FR/dploy-charts/charts/webterm@main"
        enabled: true
        icon: "terminal"
        ttl: 86400  # 24 hours in seconds

      - name: vscode
        description: "VSCode dans le navigateur"
        chart: "github.com/AYDEV-FR/dploy-charts/charts/vscode@main"
        enabled: true
        icon: "code"
        ttl: 43200  # 12 hours in seconds

      - name: jupyter
        description: "Jupyter Notebook"
        chart: "github.com/bitnami/charts/bitnami/jupyter@main"
        enabled: true
        icon: "book"
        ttl: 86400  # 24 hours in seconds
```

Voir [ENVIRONMENTS.md](ENVIRONMENTS.md) pour la documentation complète des sources.

### 4. Déployer l'API

```bash
kubectl apply -f k8s/deployment.yaml
```

## Configuration

Environment variables:

| Variable                    | Default              | Description                       |
| --------------------------- | -------------------- | --------------------------------- |
| `JWKS_URL`                  | -                    | **Required**: JWKS endpoint URL   |
| `JWT_ISSUER`                | -                    | **Required**: JWT issuer          |
| `JWT_AUDIENCE`              | `dploy`              | JWT audience                      |
| `JWT_USERNAME_CLAIM`        | `preferred_username` | JWT claim for username            |
| `ARGOCD_NAMESPACE`          | `argocd`             | ArgoCD namespace                  |
| `ARGOCD_PROJECT`            | `dploy`              | ArgoCD project name               |
| `MAX_ENVIRONMENTS_PER_USER` | `5`                  | Default max environments per user |
| `DEFAULT_TTL`               | `86400`              | Default TTL in seconds (24h)      |
| `EXTEND_TTL`                | `7200`               | TTL extension in seconds (2h)     |
| `CLEANUP_INTERVAL`          | `60`                 | TTL cleanup check interval (sec)  |
| `BASE_DOMAIN`               | `env.dploy.dev`      | Base domain for ingress           |
| `SERVER_HOST`               | `0.0.0.0`            | Server bind address               |
| `SERVER_PORT`               | `8080`               | Server port                       |

## Web UI

L'interface web est embarquée dans le container et accessible sur `/`.

### Accès

1. Ouvrir `http://localhost:8080` (ou votre domaine)
2. Coller votre JWT token
3. Voir et gérer vos environnements

### Fonctionnalités

- 📋 Liste des environnements disponibles
- ▶️ Launch d'environnements en un clic
- 📊 Vue des environnements actifs (status, URL, TTL)
- 🔗 Accès direct aux environnements
- ⏱️ Extension du TTL
- 🗑️ Suppression d'environnements
- 🔄 Polling automatique du status

Voir [web/README.md](web/README.md) pour plus de détails.

## API Endpoints

### Health

- `GET /health` - Liveness probe
- `GET /ready` - Readiness probe

### Environments

- `GET /api/environments/available` - List available environments
- `GET /api/environments` - List user's active environments (requires auth)

### Run (Routes principales)

**URI centrale : `https://dploy.dev/run/{env}`**

- `GET /run/{env}` - Create environment if not exists, or return existing one (requires auth)
- `GET /run/{env}/status` - Get environment status only (requires auth)
- `POST /run/{env}/extend` - Extend TTL by configured hours (requires auth)
- `DELETE /run/{env}` - Delete environment (requires auth)

Routes alternatives (même comportement) :

- `GET /api/run/{env}`
- `GET /api/run/{env}/status`
- `POST /api/run/{env}/extend`
- `DELETE /api/run/{env}`

## Development

### Build

```bash
go build -o dploy-api ./cmd/api
```

### Run locally

```bash
export JWKS_URL=https://your-dex.com/keys
export JWT_ISSUER=https://your-dex.com
go run ./cmd/api/main.go
```

### Build Docker image

```bash
docker build -t dploy-api:latest .
```

### Download dependencies

```bash
go mod tidy
```

## Usage Examples

### Créer/récupérer un environnement

```bash
# Simple GET sur /run/{env} crée l'environnement ou retourne l'existant
curl https://dploy.dev/run/webterm \
  -H "Authorization: Bearer $TOKEN"

# Response:
# {
#   "uuid": "a1b2c3d4",
#   "status": "pending",
#   "url": "https://john-doe-a1b2c3d4.env.dploy.dev",
#   "expiresAt": "2024-01-15T16:00:00Z"
# }
```

### Vérifier le status

```bash
curl https://dploy.dev/run/webterm/status \
  -H "Authorization: Bearer $TOKEN"

# Response:
# {
#   "uuid": "a1b2c3d4",
#   "status": "healthy",
#   "url": "https://john-doe-a1b2c3d4.env.dploy.dev",
#   "expiresAt": "2024-01-15T16:00:00Z"
# }
```

### Prolonger le TTL

```bash
curl -X POST https://dploy.dev/run/webterm/extend \
  -H "Authorization: Bearer $TOKEN"

# Response:
# {
#   "expiresAt": "2024-01-15T18:00:00Z"
# }
```

### Supprimer l'environnement

```bash
curl -X DELETE https://dploy.dev/run/webterm \
  -H "Authorization: Bearer $TOKEN"
```

## Utilisation

### URLs directes

Vous pouvez accéder directement aux environnements via des URLs simples:

```
http://localhost:8080/run/webterm
http://localhost:8080/run/vscode
```

Ces URLs :
- ✅ Fonctionnent directement dans le navigateur
- ✅ Gèrent automatiquement l'authentification (localStorage ou OAuth)
- ✅ Créent l'environnement si nécessaire
- ✅ Redirigent automatiquement vers l'environnement une fois prêt
- ✅ Peuvent être partagées et bookmarkées

### Interface Web

Accédez à l'interface complète sur `http://localhost:8080/` pour:
- Voir tous vos environnements actifs
- Gérer les TTL
- Supprimer des environnements

## Architecture

The API manages ArgoCD Applications as a proxy for deploying Helm charts. Each user environment:

1. Gets a unique UUID (8 characters)
2. Creates an ArgoCD Application with labels and annotations
3. Deploys to a dedicated namespace: `{username}-{env}-{uuid}`
4. Gets an ingress: `https://{username}-{uuid}.{BASE_DOMAIN}`
5. Auto-deletes after TTL expires (via built-in cleanup worker)

## Documentation

- [QUICKSTART.md](QUICKSTART.md) - Guide rapide pour démarrer avec Kind
- [ENVIRONMENTS.md](ENVIRONMENTS.md) - Configuration des sources Helm Git
- [ROUTES.md](ROUTES.md) - Documentation complète des routes API
- [web/README.md](web/README.md) - Documentation de l'interface web
- [dev/README.md](dev/README.md) - Guide de développement local

## License

MIT
