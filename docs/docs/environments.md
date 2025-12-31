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
    ttl: "86400" # Simple: 24 hours
    # ttl: "300,300,2"        # Extended: 5min, extendable 2 times by 5min
    # ttl: "-1"               # Unlimited: never expires
    category: "learning,linux"
```

## Configuration Fields

| Field         | Required | Description                                                                                           |
| ------------- | -------- | ----------------------------------------------------------------------------------------------------- |
| `name`        | Yes      | Unique identifier (used in URLs: `/run/:name`)                                                        |
| `description` | Yes      | Human-readable description                                                                            |
| `chart`       | Yes      | Helm chart reference (see format below)                                                               |
| `enabled`     | Yes      | Whether this environment is available                                                                 |
| `visible`     | No       | Show in UI/API listings (default: `true`). Hidden environments are still accessible via `/run/{name}` |
| `icon`        | No       | Icon identifier for the Web UI                                                                        |
| `ttl`         | No       | TTL configuration (see TTL format below)                                                              |
| `extraValues` | No       | Additional Helm values (YAML string)                                                                  |
| `valueFiles`  | No       | List of values files paths from the chart repository                                                  |
| `category`    | No       | Category for grouping in UI (format: `category,subcategory`)                                          |

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

## TTL Configuration

The `ttl` field supports multiple formats for flexible time-to-live configuration:

### Simple TTL (seconds)

```yaml
ttl: "300"      # 5 minutes
ttl: "86400"    # 24 hours
```

### Unlimited TTL

Use `-1` for environments that should never expire:

```yaml
ttl: "-1" # Never expires
```

### Extended Format

Format: `ttl,extendTTL,maxExtends`

| Component    | Description                                                                   |
| ------------ | ----------------------------------------------------------------------------- |
| `ttl`        | Initial TTL in seconds (`-1` for unlimited)                                   |
| `extendTTL`  | Seconds to add on each extension (optional, defaults to `EXTEND_TTL` env var) |
| `maxExtends` | Maximum number of extensions allowed (optional, defaults to unlimited)        |

Examples:

```yaml
# 5 minutes, extendable by 5 minutes, max 2 extensions
ttl: "300,300,2"

# 1 hour, extendable by 30 minutes, unlimited extensions
ttl: "3600,1800"

# 24 hours with custom extend (1 hour), max 5 extensions
ttl: "86400,3600,5"

# Unlimited TTL (no expiration, no extend needed)
ttl: "-1"
```

### Behavior

- **No TTL specified**: Uses `DEFAULT_TTL` environment variable (default: 24 hours)
- **TTL = -1**: Environment never expires, cleanup worker skips it
- **No extendTTL**: Uses `EXTEND_TTL` environment variable (default: 2 hours)
- **No maxExtends**: Unlimited extensions allowed
- **maxExtends reached**: Extend API returns error "maximum extensions (N) reached"

### Annotations

Dploy stores TTL configuration in ArgoCD Application annotations:

| Annotation               | Description                                          |
| ------------------------ | ---------------------------------------------------- |
| `dploy.dev/expires-at`   | ISO 8601 expiration timestamp (absent for unlimited) |
| `dploy.dev/extend-count` | Number of times the TTL has been extended            |
| `dploy.dev/extend-ttl`   | Per-environment extend TTL (if specified)            |
| `dploy.dev/max-extends`  | Maximum extensions allowed (if specified)            |

## Extra Values

Use `extraValues` to pass additional Helm values. Supports variable substitution:

| Variable         | Description                   |
| ---------------- | ----------------------------- |
| `${username}`    | Sanitized username from JWT   |
| `${uuid}`        | 8-character unique identifier |
| `${ingressHost}` | Generated ingress hostname    |

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

## Value Files

Use `valueFiles` to specify custom values files from the chart repository. This is useful when:

- Your chart has multiple configuration profiles (e.g., `values-production.yaml`, `values-dev.yaml`)
- You want to use different resource configurations (e.g., `values-large.yaml`, `values-small.yaml`)
- You have environment-specific settings stored in the chart repo

The paths are relative to the chart directory in the repository.

Example:

```yaml
environments:
  - name: vscode-large
    description: "VS Code with large resources"
    chart: "github.com/AYDEV-FR/dploy-charts/charts/vscode@main"
    enabled: true
    icon: "code"
    ttl: 43200
    valueFiles:
      - "values-large.yaml"
      - "values-persistence.yaml"
    extraValues: |
      workspaceName: "${username}-workspace"
```

**Order of precedence** (later overrides earlier):

1. Default `values.yaml` from the chart
2. Files listed in `valueFiles` (in order)
3. Values from `extraValues`
4. Dploy-injected values (`username`, `uuid`, `ingressHost`)

## Categories

Use `category` to group environments in the catalog UI. The format is `category,subcategory`:

- **Category only**: `"databases"` - Environment appears directly under the category
- **Category with subcategory**: `"learning,linux"` - Environment appears under the subcategory within the category
- **No category**: Environments without a category appear in an "Other" section

Examples:

```yaml
environments:
  # Will appear under "Learning" > "Linux"
  - name: webterm
    category: "learning,linux"
    # ...

  # Will appear under "Learning" > "Python"
  - name: jupyter
    category: "learning,python"
    # ...

  # Will appear directly under "Databases"
  - name: postgres
    category: "databases"
    # ...

  # Will appear under "Other" section
  - name: custom-app
    # no category specified
    # ...
```

The catalog UI displays:

1. Categories sorted alphabetically (with "Other" at the end)
2. Subcategories within each category
3. Environment cards within each group

## Icons

Available icon identifiers for the Web UI:

| Icon ID    | Emoji | Use Case                     |
| ---------- | ----- | ---------------------------- |
| `terminal` | 💻    | Terminal, shell environments |
| `desktop`  | 🖥️    | Desktop, VNC environments    |
| `code`     | 📝    | IDEs, code editors           |
| `book`     | 📚    | Notebooks, documentation     |
| `database` | 🗄️    | Databases                    |
| `box`      | 📦    | Generic containers           |
| `web`      | 🌍    | Web applications             |
| `default`  | 🚀    | Fallback                     |

## Complete Example

```yaml
environments:
  # Web terminal for students (simple TTL)
  - name: webterm
    description: "Web Terminal for students"
    chart: "github.com/AYDEV-FR/dploy-charts/charts/webterm@main"
    enabled: true
    icon: "terminal"
    ttl: "86400" # 24 hours
    category: "learning,linux"

  # VS Code with persistence (extendable TTL)
  - name: vscode
    description: "VS Code in the browser"
    chart: "github.com/AYDEV-FR/dploy-charts/charts/vscode@main"
    enabled: true
    icon: "code"
    ttl: "43200,3600,5" # 12h, extendable by 1h, max 5 times
    category: "development,ide"
    valueFiles:
      - "values-persistence.yaml"
    extraValues: |
      resources:
        limits:
          memory: 2Gi

  # Jupyter Notebook (limited extensions)
  - name: jupyter
    description: "Jupyter Notebook"
    chart: "github.com/AYDEV-FR/dploy-charts/charts/jupyter@main"
    enabled: true
    icon: "book"
    ttl: "300,300,2" # 5min, extendable by 5min, max 2 times
    category: "learning,python"

  # PostgreSQL database (unlimited TTL)
  - name: postgres
    description: "PostgreSQL database"
    chart: "github.com/AYDEV-FR/dploy-charts/charts/postgresql@v1.2.3"
    enabled: true
    icon: "database"
    ttl: "-1" # Never expires
    category: "databases"

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
    ttl: 7200 # 2 hours
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
  visible: false # Not shown in listings
  icon: "terminal"
  ttl: 3600
```

Users can access it directly at `/run/ctf-challenge` but won't see it in the environment list.

## Helm Chart Requirements

Charts deployed by Dploy receive these values automatically:

```yaml
username: "john-doe" # Sanitized username
uuid: "a1b2c3d4" # 8-character UUID
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
  name: { { include "chart.fullname" . } }
spec:
  rules:
    - host: { { .Values.ingressHost } }
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: { { include "chart.fullname" . } }
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
