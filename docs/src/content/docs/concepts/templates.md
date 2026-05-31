---
title: Templates & Instances
description: Define your catalog with DployTemplate and understand the DployInstance lifecycle.
---

The catalog is defined with **`DployTemplate`** resources. When a user launches a template, the
API creates (or claims) a **`DployInstance`**, which the operator reconciles into a running
environment.

## DployTemplate

A `DployTemplate` is a catalog entry: a Helm chart plus how it should be instantiated.

```yaml
apiVersion: dploy.dev/v1alpha1
kind: DployTemplate
metadata:
  name: webterm
  namespace: dploy-system
spec:
  displayName: "Web Terminal"
  description: "Browser-based shell for students"
  icon: terminal
  category: "learning,linux"
  enabled: true
  visible: true                  # hidden templates stay runnable by name

  method: on-demand              # or "pool"

  chart:
    type: git                    # "git" or "helm"
    repoURL: https://github.com/AYDEV-FR/dploy-charts
    path: charts/webterm         # chart path (git) — for helm, use `chart:`
    targetRevision: main         # branch/tag (git) or version (helm)

  valuesTemplate: |
    ingress:
      enabled: true
      host: "{{ .Host }}"
    user: "{{ .Owner }}"

  ttl:
    seconds: 86400               # -1 = unlimited
    extendSeconds: 7200
    maxExtends: 5

  parameters:
    - name: shell
      description: "Login shell"
      default: "/bin/bash"
```

### Fields

| Field | Description |
|-------|-------------|
| `displayName` / `description` / `icon` / `category` | UI metadata (`category` format: `"category,subcategory"`) |
| `enabled` | Gates whether instances may be created (default `true`) |
| `visible` | Show in listings (default `true`). Hidden templates remain runnable via `/run/{name}` |
| `ownerClaim` | JWT claim that identifies the owner (primary key) — see [Ownership](#ownership). Empty = the API's username claim |
| `method` | `on-demand` (one fresh instance per request) or `pool` (pre-warmed) |
| `pool` | Pool settings — see below (required for `method: pool`) |
| `engine` | Override `OperatorConfig.defaultEngine` (currently `flux`) |
| `chart` | Chart source — see below |
| `valuesTemplate` | Go template rendered to Helm values YAML |
| `valueFiles` | Values file paths within the chart repository |
| `connectionURLTemplate` | Override the cluster-wide URL template |
| `ttl` | `seconds` (-1 = unlimited), `extendSeconds`, `maxExtends` |
| `maxInstancesPerUser` | Per-template quota override |
| `parameters` | Request-supplied params (exposed as `.Params`) |

### Ownership

`ownerClaim` chooses which JWT claim is the **owner** — the primary key Dploy uses for the owner
label, per-owner quota, instance naming, and listing. This makes a template either personal or
team-shared:

```yaml
# Per-user: each user gets their own instance
spec:
  ownerClaim: preferred_username
```

```yaml
# Per-team: everyone in the group shares one instance and one quota
spec:
  ownerClaim: groups
```

- The claim value is sanitized (lowercased, `[a-z0-9-]`, ≤63 chars). Multi-valued claims (e.g.
  `groups`) use their **first** value when an instance is created.
- The **quota** applies **per owner key** — a team has its own allowance, independent of each
  member's personal quota.
- `/run/{env}` returns the owner's existing instance, so a teammate launching the same
  group-owned template **reuses** the running environment instead of creating a new one.
- `GET /api/environments` lists everything you own across all your identities — your username
  plus every group you belong to.
- An empty `ownerClaim` falls back to the API's configured username claim (`JWT_USERNAME_CLAIM`).

### Chart sources

Dploy is engine-neutral about where charts come from, and maps them onto Flux:

```yaml
# Git repository (the chart lives at a path in the repo)
chart:
  type: git
  repoURL: https://github.com/AYDEV-FR/dploy-charts
  path: charts/vscode
  targetRevision: main           # branch or tag
```

```yaml
# Helm repository
chart:
  type: helm
  repoURL: https://charts.bitnami.com/bitnami
  chart: postgresql
  targetRevision: "15.5.0"       # chart version
```

```yaml
# OCI Helm registry (a HelmRepository of type "oci")
chart:
  type: helm
  repoURL: oci://ghcr.io/aydev-fr/charts
  chart: jupyter
  targetRevision: "1.2.3"
```

### Warm pools

With `method: pool`, the DployTemplate controller keeps a set of pre-deployed, **unclaimed**
instances so users get an environment instantly:

```yaml
spec:
  method: pool
  pool:
    size: 3          # warm, idle instances to keep available
    maxSize: 10      # cap on total instances (0 = unlimited)
    recycle: true    # replace an instance after it is released
```

When a user launches a pool template, the API **claims** an `Available` instance (stamping the
owner and starting its TTL). The controller then provisions a replacement to refill the pool.

### Connection URLs

The instance URL is resolved with this precedence:

1. `DployTemplate.spec.connectionURLTemplate`
2. `OperatorConfig.spec.connectionURLTemplate`
3. fallback `https://<owner>-<uuid>.<baseDomain>`

```yaml
spec:
  connectionURLTemplate: "https://{{ .Owner }}-{{ .UUID }}.lab.example.com"
```

### Values templating

`valuesTemplate` is rendered with Go `text/template` + [sprig](https://masterminds.github.io/sprig/) against the instance context
(`.Owner`, `.UUID`, `.Host`, `.URL`, `.Params`, `.Claims`, `.Config.Values`, …). The
result is parsed as YAML and handed to the `HelmRelease`.

```yaml
valuesTemplate: |
  workspaceName: "{{ .Owner }}-workspace"
  sessionId: "{{ .UUID }}"
  email: "{{ .Claims.email }}"
  ingress:
    host: "{{ .Host }}"
  {{- if eq .Params.size "large" }}
  resources:
    limits: { cpu: "2", memory: 4Gi }
  {{- end }}
```

## DployInstance

A `DployInstance` is one deployed (or pooled) environment. The API creates it; you rarely write
one by hand. Its **status** is owned by the operator.

```yaml
apiVersion: dploy.dev/v1alpha1
kind: DployInstance
metadata:
  name: john-doe-webterm
  namespace: dploy-system
  labels:
    dploy.dev/owner: john-doe
    dploy.dev/template: webterm
spec:
  templateRef: webterm
  owner: john-doe
  params:
    shell: /bin/zsh
  ttlSeconds: 86400
  expiresAt: "2026-01-15T16:00:00Z"
status:
  phase: Ready
  uuid: a1b2c3d4
  namespace: john-doe-webterm-a1b2c3d4
  url: https://john-doe-a1b2c3d4.env.dploy.dev
  engine: flux
  engineRef: john-doe-webterm
  health: Healthy
  expiresAt: "2026-01-15T16:00:00Z"
```

### Phases

| Phase | Meaning |
|-------|---------|
| `Pending` | Accepted, not yet reconciled |
| `Provisioning` | Flux is installing the release |
| `Ready` | On-demand instance healthy and reachable |
| `Available` | Warm pool member, unclaimed |
| `Claimed` | Pool member handed to a user |
| `Expiring` | Past TTL, being torn down |
| `Failed` | Provisioning or reconciliation failed |

## Icons

Suggested icon identifiers for the web UI:

| Icon ID | Use case |
|---------|----------|
| `terminal` | Terminals, shells |
| `desktop` | VNC / desktop |
| `code` | IDEs, editors |
| `book` | Notebooks, docs |
| `database` | Databases |
| `box` | Generic containers |
| `web` | Web apps |
| `default` | Fallback |

## Chart requirements

Your chart receives whatever your `valuesTemplate` renders — there are no implicitly-injected
values. Expose a configurable ingress host (or whichever fields your template sets) so each
instance gets its unique URL, for example:

```yaml
# templates/ingress.yaml (in your chart)
spec:
  rules:
    - host: {{ .Values.ingress.host | quote }}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: {{ include "chart.fullname" . }}
                port: { number: 80 }
```

See [TLS Certificates](/deployment/tls-certificates/) for wildcard-certificate strategies that
keep per-environment hostnames out of Certificate Transparency logs.
