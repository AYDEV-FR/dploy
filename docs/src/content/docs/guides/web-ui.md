---
title: Web UI
description: Use the embedded Dploy web interface to launch and manage environments.
---

Dploy includes an embedded web interface for managing environments without using the API directly.

## Accessing the UI

Open your browser at your Dploy host:

```text
https://dploy.your-domain.com/
```

Or for local development: `http://localhost:8080/`.

## Authentication

1. Click **Login with SSO**.
2. You're redirected to your OIDC provider (Authentik, Keycloak, …) to sign in.
3. After login you're redirected back; the JWT is stored in `localStorage` and sent as a Bearer
   token on API calls.

Click **Logout** to clear your session.

## Dashboard

### Active environments

Your running environments, each showing:

- **Name** — the template it was launched from
- **Status** — derived from the instance phase / Helm health:
  - 🟢 `Healthy` — ready to use
  - 🟡 `Progressing` — still deploying
  - 🟠 `Degraded` — something is wrong
  - ⚪ `pending` — just created
- **URL** — direct link to the environment
- **Time left** — TTL countdown

Actions: **Open**, **Extend** (add time), **Delete**.

### Available templates

A grid of catalog entries (visible, enabled `DployTemplate`s). Click **Launch** to create — or,
for pool templates, instantly **claim** — an environment.

## Quota counter

The header shows your usage, e.g. `2 / 5 environments`. It turns orange as you approach the limit.

## Direct URLs

You can launch a specific template by URL:

```text
https://dploy.your-domain.com/run/webterm
```

This checks authentication, creates the environment if needed, shows progress, and redirects to
the environment when it's ready. Handy for bookmarks, sharing links, or embedding in an LMS.

## Status polling

While launching, the UI polls `/api/run/:env/status` every couple of seconds and redirects to the
environment URL once the status becomes `Healthy`. A `Degraded` status or timeout shows an error.

## Troubleshooting

**"Authentication failed — please login"** — your token expired; log in again.

**Environment stuck in `pending`/`Progressing`** — inspect the underlying Flux release:

```bash
kubectl get helmrelease -n dploy-system
flux get helmrelease -n dploy-system
kubectl describe dployinstance <name> -n dploy-system
```

**"Maximum N environments allowed"** — you've hit your quota; delete an environment or ask an
admin to raise the limit.

**"No pooled instance available, try again shortly"** — a pool template's warm instances are all
claimed; the operator is refilling the pool — retry in a moment.
