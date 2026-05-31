---
title: Configuration
description: Configure the Dploy API via environment variables and the operator via the OperatorConfig resource.
---

Dploy has two configuration surfaces:

- The **API** is configured with **environment variables** (set via the Helm chart's ConfigMap and Secret).
- The **operator** reads a cluster-scoped **`OperatorConfig`** custom resource for cluster-wide defaults.

## API environment variables

### Required

| Variable | Description |
|----------|-------------|
| `JWKS_URL` | JWKS endpoint used to validate JWT signatures |
| `JWT_ISSUER` | Expected JWT `iss` claim |

### Authentication

| Variable | Default | Description |
|----------|---------|-------------|
| `JWT_AUDIENCE` | `dploy` | Expected JWT `aud` claim |
| `JWT_USERNAME_CLAIM` | `name` | Claim used as the username |
| `OIDC_ISSUER` | `$JWT_ISSUER` | OIDC issuer (internal, for token exchange) |
| `OIDC_PUBLIC_ISSUER` | `$OIDC_ISSUER` | OIDC issuer used for browser redirects |
| `OIDC_CLIENT_ID` | `dploy` | OIDC client ID |
| `OIDC_CLIENT_SECRET` | `dploy-secret` | OIDC client secret |
| `OIDC_REDIRECT_URL` | `http://localhost:8080/auth/callback` | OIDC callback URL |

### Kubernetes & defaults

| Variable | Default | Description |
|----------|---------|-------------|
| `DPLOY_NAMESPACE` | `dploy-system` | Namespace where `DployTemplate`/`DployInstance` CRs live |
| `MAX_ENVIRONMENTS_PER_USER` | `5` | Per-user quota (a template may override it) |
| `DEFAULT_TTL` | `86400` | Fallback initial TTL in seconds |
| `EXTEND_TTL` | `7200` | Fallback extension granted per `/extend` |

### Server

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVER_HOST` | `0.0.0.0` | Bind address |
| `SERVER_PORT` | `8080` | Port |
| `DEBUG` | `false` | Verbose logging |

:::note
The API no longer reads an `environments.yaml` file or talks to ArgoCD. The catalog lives in
`DployTemplate` resources, and TTL cleanup is handled by the operator (there is no in-process
cleanup worker anymore).
:::

## OperatorConfig

The operator reads a single cluster-scoped `OperatorConfig` named **`default`**. Any other name is ignored. It carries cluster-wide defaults that individual `DployTemplate`s can override per-template.

```yaml
apiVersion: dploy.dev/v1alpha1
kind: OperatorConfig
metadata:
  name: default
spec:
  # GitOps engine used when a template does not override it.
  defaultEngine: flux                   # only "flux" is implemented today

  # Defaults for the Flux engine.
  flux:
    namespace: flux-system              # where HelmRelease + Sources are reconciled
    serviceAccountName: ""              # optional: SA the helm-controller impersonates
    interval: 5m                        # GitRepository / HelmChart poll interval

  # Used to build the default per-instance hostname: <name>-<uid>.<baseDomain>.
  baseDomain: env.dploy.dev

  # Cluster-wide default Go (text/template + sprig) for an instance's public URL.
  # Overridable per template via DployTemplate.spec.connectionURLTemplate.
  # Empty falls back to "https://<Host>".
  connectionURLTemplate: "http://{{ .Host }}"

  # Cluster-wide default presentation: "web" (clickable URL, redirect on ready)
  # or "instructions" (copyable command, no redirect — see connectionMessageTemplate).
  defaultConnectionType: web

  # Cluster-wide default instructions template. Rendered with the same context as
  # connectionURLTemplate plus .URL / .ConnectionURL (the resolved target).
  # Only used when connectionType is "instructions".
  connectionMessageTemplate: ""         # e.g. "ssh root@{{ .ConnectionURL }} -p 22000"

  # Applied to instances when their template omits the value.
  defaults:
    ttlSeconds: 86400                   # initial TTL (24 h)
    extendSeconds: 7200                 # TTL bonus per /extend call (2 h)
    maxExtends: 0                       # 0 = unlimited extensions
    maxInstancesPerUser: 5              # per-owner quota

  # Free-form map exposed to value templates as `.Config.Values`. Anything you
  # want common across templates (image registries, organisation toggles, …).
  values:
    registry: ghcr.io/your-org
    ingressClass: nginx
```

### Field reference

| Field | Description | Default |
|---|---|---|
| `defaultEngine` | GitOps engine: `flux` only today (`argocd` reserved) | `flux` |
| `flux.namespace` | Namespace where the helm-controller reconciles `HelmRelease`s | `flux-system` |
| `flux.serviceAccountName` | SA the helm-controller impersonates (optional) | — |
| `flux.interval` | Poll interval for `GitRepository` / `HelmChart` sources | `5m` |
| `baseDomain` | Base domain for the fallback `<name>-<uid>.<baseDomain>` host | — |
| `connectionURLTemplate` | Cluster default URL template (overridable per template) | `https://<Host>` |
| `defaultConnectionType` | `web` (clickable) or `instructions` (copyable command) | `web` |
| `connectionMessageTemplate` | Cluster default instructions template (when `instructions`) | — |
| `defaults.ttlSeconds` | Default TTL for a new instance | `86400` |
| `defaults.extendSeconds` | Seconds added per `/extend` call | `7200` |
| `defaults.maxExtends` | Maximum extensions allowed (`0` = unlimited) | `0` |
| `defaults.maxInstancesPerUser` | Per-owner quota (sanitized owner key) | `5` |
| `values` | Free-form map exposed as `.Config.Values` | `{}` |

### Templating context

`connectionURLTemplate`, `connectionMessageTemplate` (in `OperatorConfig` or per template) and `valuesTemplate` (per `DployTemplate`) are all rendered with [Go `text/template`](https://pkg.go.dev/text/template) + [sprig](https://masterminds.github.io/sprig/) against this data:

| Variable | Type | Notes |
|---|---|---|
| `.Owner` | `string` | Sanitized owner key — empty for unclaimed pool members and forbidden in pool `valuesTemplate` (CEL-enforced anonymity) |
| `.UUID` | `string` | 8 hex chars, generated once by the operator, immutable in `status.uuid` |
| `.BaseDomain` | `string` | Cluster `baseDomain` |
| `.Host` | `string` | Precomputed `<name>-<uid>.<baseDomain>` — routing-neutral hostname |
| `.URL` / `.ConnectionURL` | `string` | Set after `connectionURLTemplate` renders; only available in `valuesTemplate` and `connectionMessageTemplate` |
| `.Namespace` | `string` | The instance's workload namespace |
| `.Template` | `*DployTemplate` | Full template spec, useful for `{{ .Template.Name }}` / `{{ .Template.Spec.X }}` |
| `.Params` | `map[string]string` | Request-supplied params (nil in pool — anonymity) |
| `.Claims` | `map[string]any` | Requester's JWT claims (empty in pool — anonymity) |
| `.Config.Values` | `map[string]any` | `OperatorConfig.spec.values` |

### Overriding `connectionURLTemplate` per template

A `DployTemplate.spec.connectionURLTemplate` wins over the cluster default. Examples:

```yaml
# Owner-based URL (leaks identity but useful for "your own session" UIs).
connectionURLTemplate: "https://{{ .Owner }}-{{ .UUID }}.{{ .BaseDomain }}"

# Path-based instead of subdomain (single ingress hostname, multiple instances).
connectionURLTemplate: "https://shared.example.com/{{ .UUID }}/"

# Custom port + scheme.
connectionURLTemplate: "https://{{ .Host }}:8443"
```

### `connectionType: instructions`

When a template (or the operator default) sets `connectionType: instructions`, the API doesn't redirect on ready — it surfaces `status.connectionMessage` so the UI can show a copyable command:

```yaml
# OperatorConfig (or DployTemplate.spec):
defaultConnectionType: instructions
connectionMessageTemplate: "ssh root@{{ .ConnectionURL }} -p 22000"
# … combined with a connectionURLTemplate that returns the bare host:
connectionURLTemplate: "{{ .Host }}"
```

Result: `status.connectionMessage = "ssh root@vscode-abc123.example.com -p 22000"`, displayed by the UI without redirecting.

## Username sanitization

Usernames extracted from the JWT are sanitized for Kubernetes compatibility:

- lowercased
- `.` and `@` replaced with `-`
- any remaining non-`[a-z0-9-]` characters removed
- leading/trailing `-` trimmed

```text
John.Doe@example.com → john-doe-example-com
```

## Resource naming

| Resource | Pattern | Example (on-demand) | Example (pool) |
|---|---|---|---|
| Workload namespace | `<owner>-<name>-<uid>` | `john-doe-vscode-a1b2c3d4` | `pool-webshell-c7218ff8` |
| Default `Host` | `<name>-<uid>.<baseDomain>` | `vscode-a1b2c3d4.env.dploy.dev` | `webshell-c7218ff8.env.dploy.dev` |
| `DployInstance` (on-demand) | `<owner>-<template>` | `john-doe-vscode` | — (pool members get random suffixes: `webshell-pool-XXXXX`) |

The UUID is 8 hex characters, generated once by the operator at the first reconcile and stored immutably in `status.uuid`. The workload namespace uses `pool` as its owner segment for unclaimed pool members; the `Host` template segment always reflects the `DployTemplate` name regardless of mode, so `webshell-<uid>` and `kasm-<uid>` are visibly distinct even before they're claimed.
