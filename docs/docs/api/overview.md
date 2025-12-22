---
sidebar_position: 1
---

# API Overview

Dploy provides a REST API for managing ephemeral Kubernetes environments.

## Base URL

```
https://dploy.your-domain.com
```

## Authentication

Most endpoints require JWT authentication via Bearer token:

```bash
curl -H "Authorization: Bearer $TOKEN" https://dploy.your-domain.com/api/environments
```

### Obtaining a Token

#### Via OIDC Flow

1. Redirect users to `/auth/login`
2. After authentication, users are redirected back with token in URL hash
3. Extract token from `#token=...` fragment

#### Via Direct OIDC

Obtain a token directly from your OIDC provider and include it in requests.

## Response Format

All responses are JSON. Successful responses return data directly:

```json
{
  "uuid": "a1b2c3d4",
  "status": "healthy",
  "url": "https://john-doe-a1b2c3d4.env.dploy.dev",
  "expiresAt": "2024-01-15T16:00:00Z"
}
```

Error responses include an `error` field:

```json
{
  "error": "Environment not found"
}
```

## HTTP Status Codes

| Code | Description |
|------|-------------|
| `200` | Success |
| `204` | Success (no content) |
| `401` | Unauthorized - invalid or missing token |
| `403` | Forbidden - quota exceeded or access denied |
| `404` | Resource not found |
| `500` | Internal server error |

## Rate Limiting

No rate limiting is currently implemented. Consider adding rate limiting at the ingress level for production deployments.

## API Endpoints

### Health

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/health` | No | Liveness probe |
| GET | `/ready` | No | Readiness probe |

### Environments

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/api/environments/available` | No | List available environments |
| GET | `/api/environments` | Yes | List user's active environments |

### Run (Environment Management)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/run/:env` | Yes | Create or get environment |
| GET | `/run/:env/status` | Yes | Get environment status |
| POST | `/run/:env/extend` | Yes | Extend environment TTL |
| DELETE | `/run/:env` | Yes | Delete environment |

Alternative paths with `/api` prefix:

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/api/run/:env` | Yes | Create or get environment |
| GET | `/api/run/:env/status` | Yes | Get environment status |
| POST | `/api/run/:env/extend` | Yes | Extend environment TTL |
| DELETE | `/api/run/:env` | Yes | Delete environment |

### Authentication

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/auth/login` | No | Initiate OIDC login flow |
| GET | `/auth/callback` | No | OIDC callback handler |
| GET | `/auth/logout` | No | Logout (clears client state) |

## Quick Examples

```bash
# Health check
curl https://dploy.your-domain.com/health

# List available environments (no auth)
curl https://dploy.your-domain.com/api/environments/available

# Create/get an environment
curl -H "Authorization: Bearer $TOKEN" \
  https://dploy.your-domain.com/run/webterm

# Check status
curl -H "Authorization: Bearer $TOKEN" \
  https://dploy.your-domain.com/run/webterm/status

# Extend TTL
curl -X POST -H "Authorization: Bearer $TOKEN" \
  https://dploy.your-domain.com/run/webterm/extend

# Delete environment
curl -X DELETE -H "Authorization: Bearer $TOKEN" \
  https://dploy.your-domain.com/run/webterm
```
