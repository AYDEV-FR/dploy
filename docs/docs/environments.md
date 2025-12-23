---
sidebar_position: 4
---

# Environments Configuration

Environments are defined in `config/environments.yaml` or via a ConfigMap in Kubernetes.

## Basic Structure

```yaml
environments:
  - name: webterm
    description: "Web Terminal for students"
    chart: "github.com/your-org/charts/webterm@main"
    enabled: true
    icon: "terminal"
    ttl: 86400
```

## Configuration Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Unique identifier (used in URLs: `/run/:name`) |
| `description` | Yes | Human-readable description |
| `chart` | Yes | Helm chart reference (see format below) |
| `enabled` | Yes | Whether this environment is available |
| `visible` | No | Show in UI/API listings (default: `true`). Hidden environments are still accessible via `/run/{name}` |
| `icon` | No | Icon identifier for the Web UI |
| `ttl` | No | TTL in seconds (overrides `DEFAULT_TTL`) |
| `extraValues` | No | Additional Helm values (YAML string) |

## Chart Reference Format

Charts are referenced using the format:

```
github.com/{org}/{repo}/{path}@{revision}
```

Examples:

```yaml
# From a dedicated charts repo
chart: "github.com/AYDEV-FR/dploy-charts/charts/webterm@main"

# From a monorepo
chart: "github.com/your-org/monorepo/deploy/charts/app@v1.2.3"

# Using a specific tag
chart: "github.com/bitnami/charts/bitnami/nginx@main"
```

The chart reference is parsed into:
- **repoURL**: `https://github.com/{org}/{repo}`
- **path**: `{path}` (everything after the repo)
- **targetRevision**: `{revision}` (defaults to `main`)

## Extra Values

Use `extraValues` to pass additional Helm values. Supports variable substitution:

| Variable | Description |
|----------|-------------|
| `${username}` | Sanitized username from JWT |
| `${uuid}` | 8-character unique identifier |
| `${ingressHost}` | Generated ingress hostname |

Example:

```yaml
environments:
  - name: vscode
    description: "VS Code in the browser"
    chart: "github.com/AYDEV-FR/dploy-charts/charts/vscode@main"
    enabled: true
    icon: "code"
    ttl: 43200
    extraValues: |
      persistence:
        enabled: true
        size: 10Gi
      resources:
        limits:
          memory: 2Gi
          cpu: "2"
        requests:
          memory: 512Mi
          cpu: "500m"
      workspaceName: "${username}-workspace"
      sessionId: "${uuid}"
```

## Icons

Available icon identifiers for the Web UI:

| Icon ID | Emoji | Use Case |
|---------|-------|----------|
| `terminal` | 💻 | Terminal, shell environments |
| `desktop` | 🖥️ | Desktop, VNC environments |
| `code` | 📝 | IDEs, code editors |
| `book` | 📚 | Notebooks, documentation |
| `database` | 🗄️ | Databases |
| `box` | 📦 | Generic containers |
| `web` | 🌍 | Web applications |
| `default` | 🚀 | Fallback |

## Complete Example

```yaml
environments:
  # Web terminal for students
  - name: webterm
    description: "Web Terminal for students"
    chart: "github.com/AYDEV-FR/dploy-charts/charts/webterm@main"
    enabled: true
    icon: "terminal"
    ttl: 86400  # 24 hours

  # VS Code with persistence
  - name: vscode
    description: "VS Code in the browser"
    chart: "github.com/AYDEV-FR/dploy-charts/charts/vscode@main"
    enabled: true
    icon: "code"
    ttl: 43200  # 12 hours
    extraValues: |
      persistence:
        enabled: true
        size: 10Gi
      resources:
        limits:
          memory: 2Gi

  # Jupyter Notebook
  - name: jupyter
    description: "Jupyter Notebook"
    chart: "github.com/AYDEV-FR/dploy-charts/charts/jupyter@main"
    enabled: true
    icon: "book"
    ttl: 86400

  # PostgreSQL database
  - name: postgres
    description: "PostgreSQL database"
    chart: "github.com/AYDEV-FR/dploy-charts/charts/postgresql@v1.2.3"
    enabled: true
    icon: "database"
    ttl: 172800  # 48 hours

  # Disabled environment (completely unavailable)
  - name: experimental
    description: "Experimental feature"
    chart: "github.com/AYDEV-FR/dploy-charts/charts/experimental@dev"
    enabled: false
    icon: "box"

  # Hidden environment (not in listings, but accessible via /run/secret-lab)
  - name: secret-lab
    description: "Secret Lab Environment"
    chart: "github.com/AYDEV-FR/dploy-charts/charts/kali@main"
    enabled: true
    visible: false
    icon: "terminal"
    ttl: 7200  # 2 hours
```

## Hidden Environments

Use `visible: false` to create environments that are:
- **Not listed** in the UI or `/api/environments/available` endpoint
- **Still accessible** via direct URL `/run/{name}` if the user knows the name

This is useful for:
- **Beta testing**: Share new environments with specific users
- **Special access**: Provide environments to users who have the direct link
- **Caching environments**: Pre-configured environments for specific use cases

```yaml
# Hidden environment example
- name: ctf-challenge
  description: "CTF Challenge Environment"
  chart: "github.com/your-org/charts/ctf@main"
  enabled: true
  visible: false  # Not shown in listings
  icon: "terminal"
  ttl: 3600
```

Users can access it directly at `/run/ctf-challenge` but won't see it in the environment list.

## Helm Chart Requirements

Charts deployed by Dploy receive these values automatically:

```yaml
username: "john-doe"        # Sanitized username
uuid: "a1b2c3d4"           # 8-character UUID
ingressHost: "john-doe-a1b2c3d4.env.dploy.dev"
```

Your Helm chart **must** accept these values. Example `values.yaml`:

```yaml
# Required by Dploy
username: ""
uuid: ""
ingressHost: ""

# Your chart's values
replicas: 1
image:
  repository: your-image
  tag: latest
```

Use the `ingressHost` in your ingress template:

```yaml
# templates/ingress.yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ include "chart.fullname" . }}
spec:
  rules:
    - host: {{ .Values.ingressHost }}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: {{ include "chart.fullname" . }}
                port:
                  number: 80
```

## Kubernetes ConfigMap

Deploy environments via ConfigMap in Kubernetes:

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
        chart: "github.com/AYDEV-FR/dploy-charts/charts/webterm@main"
        enabled: true
        icon: "terminal"
        ttl: 86400
```

The ConfigMap is mounted at `/app/config/environments.yaml` in the container.
