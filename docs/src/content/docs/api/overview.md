---
title: API Overview
description: The Dploy REST API for managing ephemeral environments.
---

Dploy exposes a small REST API for managing ephemeral environments. The API authenticates users,
serves the catalog, and creates/claims/extends/deletes `DployInstance` resources.

## Base URL

```text
https://dploy.your-domain.com
```

## Authentication

Protected endpoints require a JWT Bearer token:

```bash
curl -H "Authorization: Bearer $TOKEN" https://dploy.your-domain.com/api/environments
```

Obtain a token by sending users through `/auth/login` (the token is returned in the redirect URL
hash), or directly from your OIDC provider.

## Response format

Successful responses return JSON directly; errors include an `error` field:

```json
{ "error": "environment \"webterm\" not found" }
```

## HTTP status codes

| Code | Meaning |
|------|---------|
| `200` | Success |
| `204` | Success, no content (delete) |
| `400` | Bad request (e.g. missing required parameter) |
| `401` | Unauthorized — invalid or missing token |
| `403` | Forbidden — quota exceeded |
| `404` | Template or instance not found |
| `409` | Conflict — maximum TTL extensions reached |
| `503` | Pool exhausted — no warm instance to claim, retry shortly |

## Endpoints

### Health

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/health` | No | Liveness probe |
| GET | `/ready` | No | Readiness probe (checks Kubernetes connectivity) |

### Catalog & instances

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/api/environments/available` | No | List visible, enabled templates |
| GET | `/api/environments` | Yes | List the user's instances |

### Run

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/run/:env` | Yes | Create (or claim) an environment, or return the existing one |
| GET | `/run/:env/status` | Yes | Get an environment's status |
| POST | `/run/:env/extend` | Yes | Extend the TTL |
| DELETE | `/run/:env` | Yes | Delete the environment |

The `/run/*` routes are also available under `/api/run/*`. `:env` is the **template name**.

### Authentication

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/auth/login` | No | Start the OIDC login flow |
| GET | `/auth/callback` | No | OIDC callback handler |
| GET | `/auth/logout` | No | Clear client state |

## Examples

```bash
# Health
curl https://dploy.your-domain.com/health

# Catalog (no auth)
curl https://dploy.your-domain.com/api/environments/available

# Create / claim an environment
curl -H "Authorization: Bearer $TOKEN" https://dploy.your-domain.com/run/webterm

# With template parameters (passed via query string)
curl -H "Authorization: Bearer $TOKEN" \
  "https://dploy.your-domain.com/run/vscode?size=large"

# Status, extend, delete
curl -H "Authorization: Bearer $TOKEN" https://dploy.your-domain.com/run/webterm/status
curl -X POST -H "Authorization: Bearer $TOKEN" https://dploy.your-domain.com/run/webterm/extend
curl -X DELETE -H "Authorization: Bearer $TOKEN" https://dploy.your-domain.com/run/webterm
```
