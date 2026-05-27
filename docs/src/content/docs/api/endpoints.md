---
title: API Endpoints
description: Detailed reference for every Dploy API endpoint.
---

`:env` always refers to a **template name**. Protected endpoints require a JWT Bearer token.

## Health

### GET /health

Liveness probe. **Auth:** none.

```json
{ "status": "ok" }
```

### GET /ready

Readiness probe — verifies Kubernetes connectivity by listing templates. **Auth:** none.

```json
{ "status": "ready" }
```

Returns `503` with `{ "error": "Service not ready" }` if the cluster is unreachable.

## Catalog

### GET /api/environments/available

List visible, enabled templates. **Auth:** none.

```json
[
  {
    "name": "webterm",
    "description": "Browser-based shell",
    "icon": "terminal",
    "category": "learning,linux",
    "ttl": 86400,
    "extendTTL": 7200,
    "maxExtends": 5,
    "isUnlimited": false
  }
]
```

### GET /api/environments

List the authenticated user's instances. **Auth:** required.

```json
{
  "environments": [
    {
      "name": "webterm",
      "description": "Browser-based shell",
      "uuid": "a1b2c3d4",
      "status": "Healthy",
      "url": "https://john-doe-a1b2c3d4.env.dploy.dev",
      "expiresAt": "2026-01-15T16:00:00Z",
      "icon": "terminal",
      "extendCount": 1,
      "maxExtends": 5,
      "extendTTL": 7200,
      "isUnlimited": false,
      "owner": "team-a",
      "shared": true
    }
  ],
  "count": 1,
  "limit": 5
}
```

Run and status responses also carry `owner` (the resolved owner key) and `shared` (`true` when
the environment is owned by a team/group rather than you personally).

## Run

### GET /run/:env

Create a new environment (or claim a warm pool member), or return the user's existing one.
**Auth:** required.

Template parameters declared on the `DployTemplate` are read from the **query string**
(e.g. `?size=large`); missing required parameters return `400`.

```json
{
  "uuid": "a1b2c3d4",
  "status": "pending",
  "url": "https://john-doe-a1b2c3d4.env.dploy.dev",
  "expiresAt": "2026-01-15T16:00:00Z",
  "owner": "team-a",
  "shared": true
}
```

**Status values:** `pending`, `Progressing`, `Healthy`, `Degraded`, `Deleting`.

| Code | Error | Cause |
|------|-------|-------|
| 400 | missing required parameter … | a required template parameter was omitted |
| 401 | unauthorized | invalid or missing token |
| 403 | Maximum N environments allowed | per-user quota exceeded |
| 404 | environment … not found | unknown or disabled template |
| 503 | no pooled instance available… | pool template temporarily exhausted |

### GET /run/:env/status

Get the current status of the user's environment. **Auth:** required.

```json
{
  "uuid": "a1b2c3d4",
  "status": "Healthy",
  "url": "https://john-doe-a1b2c3d4.env.dploy.dev",
  "expiresAt": "2026-01-15T16:00:00Z",
  "owner": "team-a",
  "shared": true
}
```

Returns `404` if the user has no instance of that template.

### POST /run/:env/extend

Extend the TTL by the template's `extendSeconds` (or the API default). **Auth:** required.

```json
{ "expiresAt": "2026-01-15T18:00:00Z" }
```

| Code | Cause |
|------|-------|
| 400 | the environment has an unlimited TTL |
| 409 | maximum extensions reached |
| 404 | no such instance |

### DELETE /run/:env

Delete the user's environment. The operator's finalizer tears down the workload. **Auth:** required.

Returns `204 No Content`, or `404` if not found.

## Authentication

### GET /auth/login

Starts the OIDC authorization-code flow. Optional `return_url` query parameter sets where to land
after login. Redirects to the provider.

### GET /auth/callback

Handles the provider callback (`code`, `state`), exchanges the code for tokens, and redirects to
the return URL with the token in the hash fragment: `…#token=<id_token>`.

### GET /auth/logout

Clears client-side auth state.

## Timestamps

All timestamps are ISO 8601 / RFC 3339 in UTC, e.g. `2026-01-15T16:00:00Z`. An empty `expiresAt`
means the instance has an unlimited TTL.
