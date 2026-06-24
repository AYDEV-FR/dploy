# Test stack for the CTFd → Dploy plugin

A self-contained Docker Compose stack to exercise the plugin end-to-end without
a Kubernetes cluster.

```
┌────────────────────── docker network ──────────────────────┐
│                                                             │
│  Browser ──▶ CTFd (:8000)                                   │
│              ├─ oidc_provider plugin  (CTFd is the IdP)      │
│              └─ ctfd_dploy plugin     (proxy + token broker) │
│                      │  Bearer id_token                      │
│                      ▼                                       │
│              dploy-mock (:8080) ── validates token vs        │
│                                    CTFd's JWKS, just like     │
│                                    real Dploy                 │
└─────────────────────────────────────────────────────────────┘
```

The **mock Dploy** validates the CTFd-issued token exactly as real Dploy does
(RS256 vs CTFd's `/oauth/jwks`, `iss`/`aud` checks, owner from the `name`
claim), so this proves the "no auth problems" wiring. It keeps environment state
in memory — it does not provision anything real.

## Run

```bash
cd integrations/testing
docker compose up --build
```

Then:

1. Open <http://localhost:8000> and complete CTFd's first-run setup (create the
   admin account). The `dploy` OAuth client is already provisioned via
   `oidc_apps.yaml`.
2. **Admin → Challenges → + (Create)**: make any standard challenge and set its
   **Connection Info** to:
   ```
   http://dploy-mock:8080/run/demo
   ```
   Save. (`demo` is the template name; the mock accepts any name.)
3. Visit **Challenges** as a logged-in user and open that challenge. A panel
   appears:
   ```
   Environment
   Not running
   [ Start ] [ Refresh ] [ Extend ] [ Stop ]
   ```
4. Click **Start**. The first action opens a brief CTFd OAuth popup that
   auto-approves (trusted client) and closes; the panel then shows `Healthy`,
   an expiry, and the environment URL. **Extend** pushes the expiry out;
   **Stop** tears it down.

> The environment URL (`http://<owner>-xxxx.dploy.local`) is a placeholder from
> the mock and isn't browsable — the panel behavior is what's under test.

## What this validates

- Detection of a Dploy challenge purely from its Connection Info URL.
- The server-side token broker: trusted auth-code+PKCE bounce → cached
  `id_token` → `Bearer` forwarded to Dploy (token never reaches the browser).
- `aud=dploy` / `iss` / `name`-claim alignment between CTFd and Dploy.
- Start / Status / Extend / Stop mapped onto `/api/run/{template}`.

## Configuration knobs

The compose file sets everything; notable choices:

- `WORKERS=4` on CTFd — the plugin makes server-side HTTP calls to CTFd's *own*
  OIDC endpoints (discovery, token exchange). With a single worker those
  self-calls would deadlock; ≥2 workers avoids it.
- `AUTHLIB_INSECURE_TRANSPORT=1` — allows the OAuth flow over plain HTTP for
  local testing only. Never set this in production.
- `OIDC_PROVIDER_ISSUER=http://localhost:8000` — both the browser and CTFd's own
  in-container loopback reach CTFd at `localhost:8000`, so no split-horizon
  config is needed here; the mock still demonstrates split-horizon by fetching
  JWKS at `http://ctfd:8000` while expecting `iss=http://localhost:8000`.

## Reset

```bash
docker compose down -v     # also drops the CTFd DB / uploads volumes
```

## Pointing at a real Dploy instead of the mock

Set `DPLOY_API_URL` (and the challenge Connection Info host) to your real Dploy
URL, and make sure Dploy's `JWT_ISSUER` / `JWT_AUDIENCE` / `JWT_USERNAME_CLAIM`
match this stack (`http://localhost:8000` / `dploy` / `name`). Remove the
`dploy-mock` service. Real provisioning still requires Dploy's operator + a
cluster — see the top-level README and `make setup`.
