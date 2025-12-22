---
sidebar_position: 2
---

# API Endpoints

Detailed documentation for all Dploy API endpoints.

## Health Endpoints

### GET /health

Liveness probe for Kubernetes.

**Authentication:** None

**Response:**

```json
{
  "status": "ok"
}
```

---

### GET /ready

Readiness probe - verifies environments are loaded.

**Authentication:** None

**Response (200):**

```json
{
  "status": "ready"
}
```

**Response (503):**

```json
{
  "error": "Service not ready"
}
```

---

## Environment Listing

### GET /api/environments/available

List all enabled environment templates.

**Authentication:** None

**Response:**

```json
[
  {
    "name": "webterm",
    "description": "Web Terminal for students",
    "icon": "terminal"
  },
  {
    "name": "vscode",
    "description": "VS Code in the browser",
    "icon": "code"
  }
]
```

---

### GET /api/environments

List the authenticated user's active environments.

**Authentication:** Required

**Response:**

```json
{
  "environments": [
    {
      "name": "webterm",
      "uuid": "a1b2c3d4",
      "status": "healthy",
      "url": "https://john-doe-a1b2c3d4.env.dploy.dev",
      "expiresAt": "2024-01-15T16:00:00Z",
      "icon": "terminal"
    }
  ],
  "count": 1,
  "limit": 5
}
```

**Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `environments` | array | List of user's environments |
| `count` | integer | Number of active environments |
| `limit` | integer | Maximum allowed (quota) |

**Environment Object:**

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Environment type name |
| `uuid` | string | 8-character unique identifier |
| `status` | string | ArgoCD health status |
| `url` | string | Environment access URL |
| `expiresAt` | string | ISO 8601 expiration timestamp |
| `icon` | string | Icon identifier |

---

## Run Endpoints

### GET /run/:env

Create a new environment or return existing one.

**Authentication:** Required

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `env` | string | Environment name (e.g., `webterm`) |

**Response (200):**

```json
{
  "uuid": "a1b2c3d4",
  "status": "pending",
  "url": "https://john-doe-a1b2c3d4.env.dploy.dev",
  "expiresAt": "2024-01-15T16:00:00Z"
}
```

**Status Values:**

| Status | Description |
|--------|-------------|
| `pending` | Environment is being created |
| `progressing` | ArgoCD is syncing resources |
| `healthy` | Environment is ready |
| `degraded` | Some resources have issues |
| `missing` | Resources not found |

**Error Responses:**

| Code | Error | Description |
|------|-------|-------------|
| 401 | Unauthorized | Invalid or missing token |
| 403 | Maximum N environments allowed | Quota exceeded |
| 404 | Environment not found | Unknown environment name |

---

### GET /run/:env/status

Get the current status of a user's environment.

**Authentication:** Required

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `env` | string | Environment name |

**Response (200):**

```json
{
  "uuid": "a1b2c3d4",
  "status": "healthy",
  "url": "https://john-doe-a1b2c3d4.env.dploy.dev",
  "expiresAt": "2024-01-15T16:00:00Z"
}
```

**Error Response (404):**

```json
{
  "error": "Environment webterm not found"
}
```

---

### POST /run/:env/extend

Extend the TTL of an existing environment.

**Authentication:** Required

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `env` | string | Environment name |

**Response (200):**

```json
{
  "expiresAt": "2024-01-15T18:00:00Z"
}
```

The TTL is extended by `EXTEND_TTL` seconds (default: 2 hours).

**Error Response (404):**

```json
{
  "error": "Environment webterm not found"
}
```

---

### DELETE /run/:env

Delete a user's environment.

**Authentication:** Required

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `env` | string | Environment name |

**Response:** `204 No Content`

**Error Response (404):**

```json
{
  "error": "Environment webterm not found"
}
```

---

## Authentication Endpoints

### GET /auth/login

Initiate OIDC authorization code flow.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `return_url` | string | URL to redirect after login (optional) |

**Behavior:**
1. Generates a secure state parameter
2. Redirects to OIDC provider authorization endpoint
3. After successful auth, redirects to callback

---

### GET /auth/callback

Handle OIDC callback from provider.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `code` | string | Authorization code from OIDC provider |
| `state` | string | State parameter for CSRF protection |

**Behavior:**
1. Validates state parameter
2. Exchanges code for tokens
3. Redirects to return URL with token in hash fragment: `[return_url]#token=[id_token]`

---

### GET /auth/logout

Clear authentication state.

**Behavior:**
- Clears any server-side session state
- Client should clear localStorage token

---

## Alternative API Paths

All `/run/*` endpoints are also available under `/api/run/*`:

| Standard | Alternative |
|----------|-------------|
| `GET /run/:env` | `GET /api/run/:env` |
| `GET /run/:env/status` | `GET /api/run/:env/status` |
| `POST /run/:env/extend` | `POST /api/run/:env/extend` |
| `DELETE /run/:env` | `DELETE /api/run/:env` |

## Timestamps

All timestamps are in ISO 8601 format with timezone:

```
2024-01-15T16:00:00Z
```

The Web UI and clients should parse these appropriately for display.
