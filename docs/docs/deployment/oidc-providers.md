---
sidebar_position: 2
title: OIDC Configuration
---

# OIDC Provider Configuration

Dploy requires an OIDC-compliant identity provider for authentication. This guide covers configuration for popular providers: **Authentik**, **Keycloak**, and **Dex**.

## Overview

Dploy uses OIDC for:
- **User Authentication** - Login via the web UI
- **JWT Validation** - API authorization using access tokens

### Required OIDC Settings

All providers must be configured with:

| Setting | Value |
|---------|-------|
| **Client Type** | Confidential |
| **Grant Types** | Authorization Code |
| **Redirect URI** | `https://dploy.example.com/auth/callback` |
| **Token Signing** | RS256 (RSA) |

### Dploy Helm Values

After configuring your provider, set these values in your Helm installation:

```yaml
auth:
  jwksURL: "<provider-jwks-url>"
  jwtIssuer: "<provider-issuer-url>"
  jwtAudience: "dploy"
  jwtUsernameClaim: "preferred_username"  # or "sub", "email"
  oidcClientID: "dploy"
  oidcClientSecret: "<your-client-secret>"
  oidcIssuer: "<provider-issuer-url>"
  oidcRedirectURL: "https://dploy.example.com/auth/callback"
```

---

## Authentik

[Authentik](https://goauthentik.io/) is an open-source identity provider with a modern UI and powerful policy engine.

### Step 1: Create an OAuth2/OIDC Provider

1. Navigate to **Applications → Providers**
2. Click **Create** and select **OAuth2/OpenID Provider**
3. Configure the provider:

| Field | Value |
|-------|-------|
| **Name** | `dploy` |
| **Authorization flow** | `default-provider-authorization-implicit-consent` |
| **Client type** | Confidential |
| **Client ID** | `dploy` |
| **Client Secret** | Generate or set a secure secret |
| **Redirect URIs** | `https://dploy.example.com/auth/callback` |
| **Signing Key** | Select `authentik Internal JWT Certificate` |
| **Subject mode** | Based on User's username |

### Step 2: Create an Application

1. Navigate to **Applications → Applications**
2. Click **Create**
3. Configure:

| Field | Value |
|-------|-------|
| **Name** | `Dploy` |
| **Slug** | `dploy` |
| **Provider** | Select the `dploy` provider created above |

### Step 3: Get OIDC Endpoints

The OIDC discovery URL for Authentik follows this pattern:
```
https://authentik.example.com/application/o/<slug>/
```

For a provider with slug `dploy`:
- **Issuer**: `https://authentik.example.com/application/o/dploy/`
- **JWKS URL**: `https://authentik.example.com/application/o/dploy/jwks/`

### Helm Values for Authentik

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

### Authentik Blueprint (Optional)

For automated setup, use an Authentik Blueprint:

```yaml
version: 1
metadata:
  name: Dploy OAuth2 Provider
entries:
  # OAuth2 Provider
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

  # Application
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

---

## Keycloak

[Keycloak](https://www.keycloak.org/) is a widely-used open-source identity and access management solution.

### Step 1: Create a Realm (Optional)

If you want to isolate Dploy users, create a dedicated realm:

1. Click the realm dropdown (top-left) → **Create realm**
2. Name it `dploy` and click **Create**

### Step 2: Create a Client

1. Navigate to **Clients** → **Create client**
2. Configure the client:

**General Settings:**

| Field | Value |
|-------|-------|
| **Client type** | OpenID Connect |
| **Client ID** | `dploy` |

**Capability config:**

| Field | Value |
|-------|-------|
| **Client authentication** | ON |
| **Authorization** | OFF |
| **Authentication flow** | Standard flow (checked) |

**Access settings:**

| Field | Value |
|-------|-------|
| **Root URL** | `https://dploy.example.com` |
| **Valid redirect URIs** | `https://dploy.example.com/auth/callback` |
| **Web origins** | `https://dploy.example.com` |

3. Click **Save**

### Step 3: Get Client Secret

1. Go to the **Credentials** tab
2. Copy the **Client secret**

### Step 4: Get OIDC Endpoints

Keycloak's OIDC discovery URL:
```
https://keycloak.example.com/realms/<realm>/.well-known/openid-configuration
```

For the `dploy` realm:
- **Issuer**: `https://keycloak.example.com/realms/dploy`
- **JWKS URL**: `https://keycloak.example.com/realms/dploy/protocol/openid-connect/certs`

### Helm Values for Keycloak

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

### Optional: Configure User Attributes

To include additional claims in the token:

1. Navigate to **Client scopes** → **dploy-dedicated** (or create a new scope)
2. Add mappers for custom claims:
   - **Mapper type**: User Attribute
   - **Token Claim Name**: Your desired claim name
   - **Add to ID token**: ON
   - **Add to access token**: ON

---

## Dex

[Dex](https://dexidp.io/) is a lightweight OIDC provider that can federate identity from multiple upstream providers (LDAP, SAML, GitHub, etc.).

### Step 1: Configure Dex

Add a static client configuration to your Dex config:

```yaml
# dex-config.yaml
issuer: https://dex.example.com

storage:
  type: kubernetes
  config:
    inCluster: true

web:
  http: 0.0.0.0:5556

connectors:
  # Example: LDAP connector
  - type: ldap
    id: ldap
    name: LDAP
    config:
      host: ldap.example.com:636
      insecureNoSSL: false
      bindDN: cn=admin,dc=example,dc=com
      bindPW: admin-password
      userSearch:
        baseDN: ou=users,dc=example,dc=com
        filter: "(objectClass=person)"
        username: uid
        idAttr: uid
        emailAttr: mail
        nameAttr: cn

  # Example: GitHub connector
  - type: github
    id: github
    name: GitHub
    config:
      clientID: your-github-client-id
      clientSecret: your-github-client-secret
      redirectURI: https://dex.example.com/callback
      orgs:
        - name: your-org

staticClients:
  - id: dploy
    name: Dploy
    secret: your-dex-client-secret
    redirectURIs:
      - https://dploy.example.com/auth/callback

oauth2:
  skipApprovalScreen: true
```

### Step 2: Deploy Dex

Using Helm:

```bash
helm repo add dex https://charts.dexidp.io
helm repo update

helm install dex dex/dex \
  --namespace dex \
  --create-namespace \
  -f dex-config.yaml
```

### Step 3: Get OIDC Endpoints

- **Issuer**: `https://dex.example.com` (matches the `issuer` in config)
- **JWKS URL**: `https://dex.example.com/keys`
- **Discovery**: `https://dex.example.com/.well-known/openid-configuration`

### Helm Values for Dex

```yaml
auth:
  jwksURL: "https://dex.example.com/keys"
  jwtIssuer: "https://dex.example.com"
  jwtAudience: "dploy"
  jwtUsernameClaim: "name"  # Dex uses "name" by default
  oidcClientID: "dploy"
  oidcClientSecret: "your-dex-client-secret"
  oidcIssuer: "https://dex.example.com"
  oidcRedirectURL: "https://dploy.example.com/auth/callback"
```

### Dex with Multiple Connectors

Dex can federate multiple identity sources. Users select their provider at login:

```yaml
connectors:
  - type: ldap
    id: corporate-ldap
    name: Corporate Login
    config:
      # LDAP config...

  - type: oidc
    id: google
    name: Google
    config:
      issuer: https://accounts.google.com
      clientID: your-google-client-id
      clientSecret: your-google-client-secret
      redirectURI: https://dex.example.com/callback

  - type: github
    id: github
    name: GitHub
    config:
      clientID: your-github-client-id
      clientSecret: your-github-client-secret
      redirectURI: https://dex.example.com/callback
```

---

## Internal vs External URLs

When running in Kubernetes, the API server may need to communicate with the OIDC provider using internal cluster URLs, while browsers use external URLs.

### Dual Issuer Configuration

Dploy supports separate internal and external issuer URLs:

```yaml
auth:
  # Internal URL (used by the API for token exchange)
  jwtIssuer: "http://authentik-server.authentik.svc.cluster.local/application/o/dploy/"
  oidcIssuer: "http://authentik-server.authentik.svc.cluster.local/application/o/dploy"

  # External URL (used by browser for redirects)
  oidcPublicIssuer: "https://auth.example.com/application/o/dploy"

  # JWKS URL (internal, for token validation)
  jwksURL: "http://authentik-server.authentik.svc.cluster.local/application/o/dploy/jwks/"
```

This is useful when:
- The OIDC provider is in the same cluster as Dploy
- You want to avoid external network round-trips for token validation
- The provider's external URL differs from its internal service URL

---

## Troubleshooting

### Common Issues

**"Invalid issuer" error:**
- Ensure `jwtIssuer` exactly matches the `iss` claim in the JWT
- Check for trailing slashes (some providers include them, some don't)

**"Token signature validation failed":**
- Verify `jwksURL` is accessible from the Dploy pod
- Ensure the provider is using RS256 signing (not HS256)
- Check that the signing key is configured in the provider

**"Audience mismatch":**
- Set `jwtAudience` to match the client ID
- Some providers use the client ID as the audience, others use a custom value

### Debugging

Check what claims are in your token:

```bash
# Get a token from your provider
TOKEN="eyJhbG..."

# Decode and inspect (requires jq)
echo $TOKEN | cut -d. -f2 | base64 -d 2>/dev/null | jq .
```

Verify JWKS endpoint is accessible:

```bash
# From your local machine
curl https://your-provider.com/path/to/jwks

# From inside the cluster
kubectl run curl --rm -it --image=curlimages/curl -- \
  curl http://provider-service.namespace.svc.cluster.local/jwks
```

### Username Claim Selection

Choose the appropriate claim for usernames:

| Claim | Use Case |
|-------|----------|
| `preferred_username` | Human-readable usernames (Keycloak, Authentik) |
| `sub` | Unique user identifier (stable across username changes) |
| `email` | When usernames are email addresses |
| `name` | Full name (Dex default) |

The chosen claim affects resource naming in Kubernetes (namespaces, applications).
