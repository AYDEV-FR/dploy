---
sidebar_position: 3
---

# Configuration

Dploy is configured through environment variables and a YAML file for environments.

## Environment Variables

### Required Variables

| Variable | Description |
|----------|-------------|
| `JWKS_URL` | URL to the JWKS endpoint for JWT validation |
| `JWT_ISSUER` | Expected JWT issuer claim |

### Authentication

| Variable | Default | Description |
|----------|---------|-------------|
| `JWT_AUDIENCE` | `dploy` | Expected JWT audience claim |
| `JWT_USERNAME_CLAIM` | `name` | JWT claim to extract username from |

### OAuth2/OIDC

| Variable | Default | Description |
|----------|---------|-------------|
| `OIDC_ISSUER` | `$JWT_ISSUER` | OIDC issuer URL (internal) |
| `OIDC_PUBLIC_ISSUER` | `$OIDC_ISSUER` | OIDC issuer URL (public, for browser redirects) |
| `OIDC_CLIENT_ID` | `dploy` | OAuth2 client ID |
| `OIDC_CLIENT_SECRET` | `dploy-secret` | OAuth2 client secret |
| `OIDC_REDIRECT_URL` | `http://localhost:8080/auth/callback` | OAuth2 callback URL |

### Kubernetes

| Variable | Default | Description |
|----------|---------|-------------|
| `ARGOCD_NAMESPACE` | `argocd` | Namespace where ArgoCD is installed |
| `ARGOCD_PROJECT` | `dploy` | ArgoCD AppProject to use for applications |

### Environment Defaults

| Variable | Default | Description |
|----------|---------|-------------|
| `MAX_ENVIRONMENTS_PER_USER` | `5` | Maximum environments per user |
| `DEFAULT_TTL` | `86400` | Default TTL in seconds (24 hours) |
| `EXTEND_TTL` | `7200` | TTL extension in seconds (2 hours) |
| `CLEANUP_INTERVAL` | `60` | Cleanup check interval in seconds |

### Ingress

| Variable | Default | Description |
|----------|---------|-------------|
| `BASE_DOMAIN` | `env.dploy.dev` | Base domain for environment ingresses |

Generated URLs follow the pattern: `https://{username}-{uuid}.{BASE_DOMAIN}`

### Server

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVER_HOST` | `0.0.0.0` | Server bind address |
| `SERVER_PORT` | `8080` | Server port |

## Configuration Examples

### Production with Keycloak

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: dploy-api-secrets
  namespace: dploy-system
stringData:
  JWKS_URL: "https://keycloak.example.com/realms/dploy/protocol/openid-connect/certs"
  JWT_ISSUER: "https://keycloak.example.com/realms/dploy"
  JWT_USERNAME_CLAIM: "preferred_username"
  OIDC_CLIENT_ID: "dploy-api"
  OIDC_CLIENT_SECRET: "your-keycloak-secret"
  OIDC_REDIRECT_URL: "https://dploy.example.com/auth/callback"
```

### Development with Dex

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: dploy-api-secrets
  namespace: dploy-system
stringData:
  JWKS_URL: "http://dex.dex.svc.cluster.local:5556/keys"
  JWT_ISSUER: "http://auth.dploy.localhost"
  OIDC_ISSUER: "http://dex.dex.svc.cluster.local:5556"
  OIDC_PUBLIC_ISSUER: "http://auth.dploy.localhost"
  OIDC_CLIENT_ID: "dploy"
  OIDC_CLIENT_SECRET: "dploy-secret"
  OIDC_REDIRECT_URL: "http://dploy.localhost/auth/callback"
```

### ConfigMap Settings

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: dploy-api-config
  namespace: dploy-system
data:
  ARGOCD_NAMESPACE: "argocd"
  ARGOCD_PROJECT: "dploy"
  MAX_ENVIRONMENTS_PER_USER: "5"
  DEFAULT_TTL: "86400"
  EXTEND_TTL: "7200"
  BASE_DOMAIN: "env.dploy.dev"
  SERVER_HOST: "0.0.0.0"
  SERVER_PORT: "8080"
  JWT_AUDIENCE: "dploy"
  JWT_USERNAME_CLAIM: "name"
```

## Username Sanitization

Usernames extracted from JWT claims are sanitized for Kubernetes compatibility:

- Converted to lowercase
- Dots (`.`) replaced with hyphens (`-`)
- At signs (`@`) replaced with hyphens (`-`)
- Non-alphanumeric characters (except `-`) removed

Example: `John.Doe@example.com` becomes `john-doe-example-com`

## TTL Behavior

- **DEFAULT_TTL**: Applied when creating new environments. Can be overridden per-environment in `environments.yaml`
- **EXTEND_TTL**: Amount added when user extends TTL via the API
- **CLEANUP_INTERVAL**: How often the built-in cleanup worker checks for expired environments (default: 60 seconds)
- **Cleanup**: The built-in cleanup worker runs periodically, deleting applications where `dploy.dev/expires-at` is in the past

## Resource Naming

Resources are named using the pattern: `{username}-{envName}-{uuid}`

- **Application**: `john-doe-webterm-a1b2c3d4`
- **Namespace**: `john-doe-webterm-a1b2c3d4`
- **Ingress Host**: `john-doe-a1b2c3d4.env.dploy.dev`

The UUID is 8 characters, derived from a full UUID with hyphens removed.
