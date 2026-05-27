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

The operator reads a single cluster-scoped `OperatorConfig` named **`default`**. Any other name
is ignored. It provides cluster-wide defaults that individual templates can override.

```yaml
apiVersion: dploy.dev/v1alpha1
kind: OperatorConfig
metadata:
  name: default
spec:
  # GitOps engine used when a template does not override it.
  defaultEngine: flux

  flux:
    namespace: flux-system
    interval: 5m

  # Used to build per-instance hostnames: <uuid>.<baseDomain>
  baseDomain: env.dploy.dev

  # Default Go template for an instance's public URL. A DployTemplate may override it.
  # Variables: .Owner .UUID .BaseDomain .Template .Params .Claims
  connectionURLTemplate: "https://{{ .Owner }}-{{ .UUID }}.{{ .BaseDomain }}"

  # Applied to instances when their template omits the value.
  defaults:
    ttlSeconds: 86400
    extendSeconds: 7200
    maxExtends: 0            # 0 = unlimited
    maxInstancesPerUser: 5

  # Free-form values exposed to templates as `.Config.Values`.
  values:
    registry: ghcr.io/aydev-fr
```

| Field | Description |
|-------|-------------|
| `defaultEngine` | `flux` (default) — `argocd` is reserved for the future |
| `flux.namespace` / `flux.interval` | Namespace and reconcile interval for Flux resources |
| `baseDomain` | Base domain for the fallback `<owner>-<uuid>.<baseDomain>` host |
| `connectionURLTemplate` | Cluster-wide default URL template (overridable per template) |
| `defaults.*` | TTL, extension and quota defaults |
| `values` | Arbitrary map exposed to value templates as `.Config.Values` |

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

| Resource | Pattern | Example |
|----------|---------|---------|
| Workload namespace | `<owner>-<template>-<uuid>` | `john-doe-webterm-a1b2c3d4` |
| Fallback host | `<owner>-<uuid>.<baseDomain>` | `john-doe-a1b2c3d4.env.dploy.dev` |
| `DployInstance` (on-demand) | `<owner>-<template>` | `john-doe-webterm` |

The UUID is 8 hex characters, generated once by the operator and stored immutably in
`status.uuid`. For unclaimed pool members the owner segment is `pool`.
