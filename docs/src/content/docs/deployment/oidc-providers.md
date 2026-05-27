---
title: OIDC Providers
description: Configure Authentik, Keycloak, or Dex as the identity provider for Dploy.
---

Dploy requires an OIDC-compliant identity provider for authentication. This guide covers
**Authentik**, **Keycloak**, and **Dex**.

Dploy uses OIDC for **user authentication** (web UI login) and **JWT validation** (API
authorization). The user's JWT claims are also captured on each `DployInstance` and exposed to
your `valuesTemplate` as `.Claims`.

## Required OIDC settings

All providers must be configured with:

| Setting | Value |
|---------|-------|
| **Client type** | Confidential |
| **Grant types** | Authorization Code |
| **Redirect URI** | `https://dploy.example.com/auth/callback` |
| **Token signing** | RS256 (RSA) |

After configuring your provider, set these Helm values:

```yaml
auth:
  jwksURL: "<provider-jwks-url>"
  jwtIssuer: "<provider-issuer-url>"
  jwtAudience: "dploy"
  jwtUsernameClaim: "preferred_username"   # or "sub", "email"
  oidcClientID: "dploy"
  oidcClientSecret: "<your-client-secret>"
  oidcIssuer: "<provider-issuer-url>"
  oidcRedirectURL: "https://dploy.example.com/auth/callback"
```

## Authentik

[Authentik](https://goauthentik.io/) is an open-source identity provider with a modern UI.

### 1. Create an OAuth2/OIDC provider

**Applications → Providers → Create → OAuth2/OpenID Provider**:

| Field | Value |
|-------|-------|
| Name | `dploy` |
| Authorization flow | `default-provider-authorization-implicit-consent` |
| Client type | Confidential |
| Client ID | `dploy` |
| Client Secret | a secure secret |
| Redirect URIs | `https://dploy.example.com/auth/callback` |
| Signing Key | `authentik Internal JWT Certificate` |
| Subject mode | Based on the user's username |

### 2. Create an application

**Applications → Applications → Create**: name `Dploy`, slug `dploy`, provider = the `dploy`
provider above.

### 3. Endpoints

For a provider with slug `dploy`:

- **Issuer**: `https://authentik.example.com/application/o/dploy/`
- **JWKS**: `https://authentik.example.com/application/o/dploy/jwks/`

```yaml
auth:
  jwksURL: "https://authentik.example.com/application/o/dploy/jwks/"
  jwtIssuer: "https://authentik.example.com/application/o/dploy/"
  jwtAudience: "dploy"
  jwtUsernameClaim: "preferred_username"
  oidcClientID: "dploy"
  oidcClientSecret: "your-authentik-client-secret"
  oidcIssuer: "https://authentik.example.com/application/o/dploy"
  oidcRedirectURL: "https://dploy.example.com/auth/callback"
```

### Blueprint (optional)

```yaml
version: 1
metadata:
  name: Dploy OAuth2 Provider
entries:
  - model: authentik_providers_oauth2.oauth2provider
    id: dploy-provider
    identifiers:
      name: dploy
    attrs:
      name: dploy
      authorization_flow: !Find [authentik_flows.flow, [slug, default-provider-authorization-implicit-consent]]
      invalidation_flow: !Find [authentik_flows.flow, [slug, default-provider-invalidation-flow]]
      client_type: confidential
      client_id: dploy
      client_secret: your-secure-secret
      redirect_uris:
        - matching_mode: strict
          url: "https://dploy.example.com/auth/callback"
      sub_mode: user_username
      include_claims_in_id_token: true
      issuer_mode: per_provider
      signing_key: !Find [authentik_crypto.certificatekeypair, [name, authentik Internal JWT Certificate]]
  - model: authentik_core.application
    id: dploy-app
    identifiers:
      slug: dploy
    attrs:
      name: Dploy
      slug: dploy
      provider: !KeyOf dploy-provider
      policy_engine_mode: any
```

## Keycloak

[Keycloak](https://www.keycloak.org/) is a widely-used IAM solution.

1. **(Optional) Create a realm** named `dploy`.
2. **Clients → Create client**: OpenID Connect, Client ID `dploy`, **Client authentication: ON**,
   Standard flow enabled, redirect URI `https://dploy.example.com/auth/callback`.
3. **Credentials** tab → copy the **Client secret**.

Endpoints for the `dploy` realm:

- **Issuer**: `https://keycloak.example.com/realms/dploy`
- **JWKS**: `https://keycloak.example.com/realms/dploy/protocol/openid-connect/certs`

```yaml
auth:
  jwksURL: "https://keycloak.example.com/realms/dploy/protocol/openid-connect/certs"
  jwtIssuer: "https://keycloak.example.com/realms/dploy"
  jwtAudience: "dploy"
  jwtUsernameClaim: "preferred_username"
  oidcClientID: "dploy"
  oidcClientSecret: "your-keycloak-client-secret"
  oidcIssuer: "https://keycloak.example.com/realms/dploy"
  oidcRedirectURL: "https://dploy.example.com/auth/callback"
```

## Dex

[Dex](https://dexidp.io/) is a lightweight OIDC provider that federates upstream identity
sources (LDAP, SAML, GitHub, …).

```yaml
# dex-config.yaml (excerpt)
issuer: https://dex.example.com
storage:
  type: kubernetes
  config:
    inCluster: true
web:
  http: 0.0.0.0:5556
staticClients:
  - id: dploy
    name: Dploy
    secret: your-dex-client-secret
    redirectURIs:
      - https://dploy.example.com/auth/callback
oauth2:
  skipApprovalScreen: true
```

Endpoints:

- **Issuer**: `https://dex.example.com`
- **JWKS**: `https://dex.example.com/keys`

```yaml
auth:
  jwksURL: "https://dex.example.com/keys"
  jwtIssuer: "https://dex.example.com"
  jwtAudience: "dploy"
  jwtUsernameClaim: "name"   # Dex default
  oidcClientID: "dploy"
  oidcClientSecret: "your-dex-client-secret"
  oidcIssuer: "https://dex.example.com"
  oidcRedirectURL: "https://dploy.example.com/auth/callback"
```

## Internal vs external URLs

When the provider runs in the same cluster, the API can validate/exchange tokens over an internal
service URL while browsers use the public URL. Dploy supports dual issuers:

```yaml
auth:
  # Internal URL (API token exchange + JWKS)
  jwtIssuer: "http://authentik-server.authentik.svc.cluster.local/application/o/dploy/"
  oidcIssuer: "http://authentik-server.authentik.svc.cluster.local/application/o/dploy"
  jwksURL: "http://authentik-server.authentik.svc.cluster.local/application/o/dploy/jwks/"
  # External URL (browser redirects)
  oidcPublicIssuer: "https://auth.example.com/application/o/dploy"
```

## Troubleshooting

**"Invalid issuer"** — `jwtIssuer` must exactly match the token's `iss` (watch for trailing
slashes).

**"Token signature validation failed"** — ensure `jwksURL` is reachable from the API pod and the
provider signs with RS256.

**"Audience mismatch"** — set `jwtAudience` to match the token's `aud` (often the client ID).

Inspect a token's claims:

```bash
echo "$TOKEN" | cut -d. -f2 | base64 -d 2>/dev/null | jq .
```

### Choosing the username claim

| Claim | Use case |
|-------|----------|
| `preferred_username` | Human-readable usernames (Keycloak, Authentik) |
| `sub` | Stable unique identifier |
| `email` | When usernames are email addresses |
| `name` | Full name (Dex default) |

The chosen claim feeds resource naming (namespaces, instance names) after sanitization.
