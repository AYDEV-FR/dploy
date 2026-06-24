# CTFd → Dploy plugin

A [CTFd](https://ctfd.io) plugin that lets players **start, stop, extend and
check** an on-demand [Dploy](https://docs.dploy.dev) environment **directly from
the challenge view** — no separate Dploy login, no copy-pasting tokens.

It pairs with the companion
[`ctfd-oidc-provider`](https://github.com/AYDEV-FR/ctfd-oidc-provider) plugin:
CTFd is the **OIDC Provider** of Dploy, so a CTFd-issued token is exactly what
Dploy's API trusts. The plugin obtains one for the logged-in player behind the
scenes and forwards it to Dploy.

## How a challenge becomes a "Dploy challenge"

There is **no custom challenge type**. Any standard challenge becomes a Dploy
challenge when its **Connection Info** is a Dploy *run URL*:

```
https://dploy.example.com/run/<template>
```

The plugin parses this into the Dploy API base (`https://dploy.example.com`) and
the template name (`<template>`), and injects a control panel into that
challenge's modal:

```
Environment
[ Healthy ]  expires in 42 min
https://alice-ab12cd.dploy.example.com
[ Start ] [ Refresh ] [ Extend ] [ Stop ]
```

The detection pattern is configurable (`DPLOY_URL_PATTERN`); the default is

```
^(?<base>https?://[^/]+)/run/(?<template>[^/?#\s]+)
```

(written once in JS-regex syntax and reused on both sides). The connection URL
is only ever parsed **server-side** from the challenge's stored Connection Info,
so a player can never aim their token at an arbitrary host.

## Auth model (BFF)

```
 Browser ──same-origin──▶ CTFd plugin proxy ──Bearer id_token──▶ Dploy API
   │                          │
   │  (no token ever          └─ trusted OIDC auth-code+PKCE bounce
   │   reaches the page)          against CTFd's own provider, cached
   │                              in the server-side Flask session
```

* The challenge panel calls **same-origin** routes
  (`/plugins/ctfd_dploy/challenges/<id>/{start,status,extend,stop}`), gated by
  CTFd's normal session auth and CSRF.
* The proxy needs a Dploy token for the player. It runs a standard
  Authorization-Code + PKCE flow against CTFd's own `/oauth/authorize`. Because
  the OAuth client is registered **trusted**, a logged-in player is
  auto-approved and bounced straight back — a brief, consent-free popup. The
  resulting `id_token` is cached in the **server-side** session and never
  exposed to JavaScript.
* When the token expires (or Dploy rejects it), the next action silently
  re-bounces.

`start`→`GET /api/run/{t}` (create-or-get), `status`→`GET …/status`,
`extend`→`POST …/extend`, `stop`→`DELETE /api/run/{t}` on Dploy.

## Why "no auth problems"

Dploy validates the token's `iss`, `aud` and username claim. Make the plugin's
client match what Dploy expects and everything just works:

| Dploy setting (default)        | Plugin requirement                                  |
|--------------------------------|-----------------------------------------------------|
| `JWT_ISSUER`                   | `DPLOY_OIDC_ISSUER` = same CTFd provider issuer      |
| `JWT_AUDIENCE` (`dploy`)       | `DPLOY_OIDC_CLIENT_ID` = same value (`dploy`)        |
| `JWT_USERNAME_CLAIM` (`name`)  | request the `profile` scope (default) so `name` is present |

Both sides verify against the **same CTFd JWKS** (`/oauth/jwks`), so signatures
always check out.

## Install

1. Copy this folder into your CTFd install as `CTFd/plugins/ctfd_dploy`
   (the directory name must be `ctfd_dploy`), or mount it there. For the OCI /
   Kubernetes pattern, copy it next to the `oidc_provider` plugin.
2. Install the dependency: `pip install -r requirements.txt` (just `requests`).
3. Restart CTFd. The plugin serves its assets and routes under
   `/plugins/ctfd_dploy/…`.

## Register the OAuth client (in the OIDC provider plugin)

In **Admin → Plugins → OIDC Apps → Register application** (or via the provider's
declarative `OIDC_PROVIDER_APPS_FILE`), create/extend the **`dploy`** client:

* **Client type:** Confidential
* **Trusted:** ✅ (so the bounce is auto-approved — no consent screen)
* **Scopes:** `openid profile email`
* **Redirect URIs:** add
  `https://<your-ctfd-host>/plugins/ctfd_dploy/auth/callback`
  (this is *in addition* to Dploy's own `/auth/callback`; reusing the same
  `dploy` client id keeps the token `aud` aligned with Dploy)

Declarative example (`oidc_apps.yaml`):

```yaml
apps:
  - name: Dploy
    client_id: dploy
    client_secret: "<shared-with-dploy-and-this-plugin>"
    type: confidential
    trusted: true
    scopes: [openid, profile, email]
    redirect_uris:
      - https://ctfd.example.com/auth/callback                       # Dploy SPA
      - https://ctfd.example.com/plugins/ctfd_dploy/auth/callback     # this plugin
    grant_types: [authorization_code, refresh_token]
```

## Configuration

Set via environment variables (preferred) or CTFd `app.config`:

| Variable                 | Default                          | Purpose |
|--------------------------|----------------------------------|---------|
| `DPLOY_OIDC_CLIENT_ID`   | `dploy`                          | Must equal Dploy's `JWT_AUDIENCE`. |
| `DPLOY_OIDC_CLIENT_SECRET` | —                              | Secret of the confidential client above. |
| `DPLOY_OIDC_ISSUER`      | provider's `OIDC_PROVIDER_ISSUER`, else request host | OIDC issuer to discover/authorize against. |
| `DPLOY_OIDC_SCOPES`      | `openid profile email`           | Must include `profile` (for the `name` claim). |
| `DPLOY_OIDC_REDIRECT_URI`| derived from the request host    | Override if CTFd is behind a proxy with a different external URL. |
| `DPLOY_URL_PATTERN`      | `^(?<base>https?://[^/]+)/run/(?<template>[^/?#\s]+)` | JS-regex (named groups `base`, `template`) that marks a challenge as Dploy. |
| `DPLOY_API_URL`          | — (derived from each URL)        | If set, acts as an **allowlist**: connection URLs must point at this host, and this host is used as the API base. |

## Authoring a challenge

Create an ordinary challenge and set its **Connection Info** to the Dploy run
URL for the template, e.g. `https://dploy.example.com/run/web-pwn-01`. Save. The
Start/Stop/Extend/Status controls appear automatically in the challenge modal.
The `web-pwn-01` `DployTemplate` must exist and be enabled in Dploy.

## Compatibility & notes

* Targets **CTFd 3.6.x / 3.7.x** with the bundled `core` theme. The panel is
  injected by observing the challenge modal and reading
  `CTFd._internal.challenge.data`; the modal-body selectors are defensive, but a
  heavily customized theme may need a tweak in `assets/dploy.js`
  (`findModalBody`).
* The plugin adds **no database tables** and defines no models — it only reads
  the standard `connection_info` field.
* Serve CTFd over **HTTPS** in production (the OAuth bounce and cookies assume
  it). For local HTTP testing the provider needs `AUTHLIB_INSECURE_TRANSPORT=1`.
```
