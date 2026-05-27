---
title: Quick Start
description: Run Dploy on a local Kind cluster with Dex for OIDC, then launch an environment through the authenticated API.
---

This walkthrough gets Dploy running on a local [Kind](https://kind.sigs.k8s.io/) cluster with a
local [Dex](https://dexidp.io/) identity provider (static username/password), so you can
authenticate and drive the **real API** — not just the operator. For production see
[Installation](/installation/) and [OIDC Providers](/deployment/oidc-providers/).

## Prerequisites

- [Kind](https://kind.sigs.k8s.io/), `kubectl`, `helm`, and Docker/Podman
- the [`flux`](https://fluxcd.io/flux/installation/#install-the-flux-cli) CLI
- `jq` and `curl` (to fetch a token and call the API)
- a clone of the repo (the chart is referenced locally):

  ```bash
  git clone https://github.com/AYDEV-FR/dploy.git
  cd dploy
  ```

## 1. Create a cluster and install Flux

```bash
kind create cluster --name dploy

# Dploy only needs these two Flux controllers
flux install --components=source-controller,helm-controller
```

## 2. Build and load the images

```bash
make docker-build docker-build-operator
kind load docker-image dploy-api:local dploy-operator:local --name dploy
```

## 3. Deploy Dex with a local user

Dex ships a built-in password database — ideal for local testing. The config below defines one
static user (**`admin@dploy.dev`** / **`password`**) and a `dploy` OAuth2 client, and enables the
password grant so we can fetch a token without a browser.

```bash
helm repo add dex https://charts.dexidp.io
helm repo update

cat > /tmp/dex-values.yaml <<'EOF'
config:
  # The issuer is an in-cluster URL: the dploy API validates tokens against it,
  # and Dex signs tokens with it regardless of how you reach the token endpoint.
  issuer: http://dex.dex.svc.cluster.local:5556
  storage:
    type: memory
  enablePasswordDB: true
  oauth2:
    passwordConnector: local   # enables the password grant against the static users
    skipApprovalScreen: true
  staticPasswords:
    - email: "admin@dploy.dev"
      # bcrypt hash of the password "password"
      hash: "$2a$10$2b2cU8CPhOTaGrs1HRQuAueS7JTT5ZHsHSzYiFPm1leZck7Mc8T4"
      username: "admin"
      userID: "08a8684b-db88-4b73-90a9-3cd1661f5466"
  staticClients:
    - id: dploy
      name: Dploy
      secret: dploy-secret
      redirectURIs:
        - http://localhost:8080/auth/callback
EOF

helm install dex dex/dex --namespace dex --create-namespace -f /tmp/dex-values.yaml
kubectl -n dex rollout status deploy/dex
```

:::note
Generate your own password hash with `htpasswd -bnBC 10 "" 'your-password' | tr -d ':\n'`.
:::

## 4. Install Dploy (pointed at Dex)

```bash
helm install dploy ./charts/dploy \
  --namespace dploy-system --create-namespace \
  --set image.repository=dploy-api --set image.tag=local \
  --set operator.image.repository=dploy-operator --set operator.image.tag=local \
  --set auth.jwksURL=http://dex.dex.svc.cluster.local:5556/keys \
  --set auth.jwtIssuer=http://dex.dex.svc.cluster.local:5556 \
  --set auth.jwtAudience=dploy \
  --set auth.jwtUsernameClaim=name \
  --set auth.oidcClientID=dploy \
  --set auth.oidcClientSecret=dploy-secret \
  --set auth.oidcIssuer=http://dex.dex.svc.cluster.local:5556 \
  --set auth.oidcRedirectURL=http://localhost:8080/auth/callback

kubectl -n dploy-system rollout status deploy/dploy-operator
kubectl -n dploy-system rollout status deploy/dploy
```

## 5. Add a template to the catalog

This `DployTemplate` deploys the public [podinfo](https://github.com/stefanprodan/podinfo) chart.

```bash
kubectl apply -f - <<'EOF'
apiVersion: dploy.dev/v1alpha1
kind: DployTemplate
metadata:
  name: podinfo
  namespace: dploy-system
spec:
  displayName: "Podinfo"
  description: "Tiny demo web app"
  enabled: true
  method: on-demand
  chart:
    type: helm
    repoURL: https://stefanprodan.github.io/podinfo
    chart: podinfo
    targetRevision: "6.7.1"
  ttl:
    seconds: 3600
  valuesTemplate: |
    ui:
      message: "Hello {{ .Owner }} — instance {{ .UUID }}"
EOF
```

## 6. Get a token and drive the API

Port-forward Dex and the API (background them, or use separate terminals):

```bash
kubectl -n dex port-forward svc/dex 5556:5556 >/dev/null 2>&1 &
kubectl -n dploy-system port-forward svc/dploy 8080:80 >/dev/null 2>&1 &
```

Fetch an `id_token` with the OAuth2 password grant:

```bash
TOKEN=$(curl -s http://localhost:5556/token \
  -d grant_type=password \
  -d client_id=dploy -d client_secret=dploy-secret \
  -d username=admin@dploy.dev -d password=password \
  -d scope="openid profile email" | jq -r .id_token)

echo "$TOKEN" | cut -d. -f2 | base64 -d 2>/dev/null | jq   # inspect the claims
```

Now call the API as `admin`:

```bash
# Public catalog (no auth)
curl -s http://localhost:8080/api/environments/available | jq

# Launch podinfo — creates a DployInstance owned by "admin"
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/run/podinfo | jq
# { "uuid": "…", "status": "pending", "url": "…", "owner": "admin" }

# Your environments
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/environments | jq
```

:::tip
You can also open **http://localhost:8080/** in a browser, click **Login with SSO**, and sign in
at Dex with `admin@dploy.dev` / `password`.
:::

Watch the operator converge the instance and materialize a Flux `HelmRelease`:

```bash
kubectl get dployinstance -n dploy-system -w
flux get helmreleases -A
```

:::note[Operator-direct alternative]
You can skip the API entirely and drive the operator with `kubectl` by applying a `DployInstance`
yourself (set `spec.owner` to any key) — handy for testing without a token:

```bash
kubectl apply -f - <<'EOF'
apiVersion: dploy.dev/v1alpha1
kind: DployInstance
metadata:
  name: alice-podinfo
  namespace: dploy-system
  labels: { dploy.dev/owner: alice, dploy.dev/template: podinfo }
spec:
  templateRef: podinfo
  owner: alice
  ttlSeconds: 3600
EOF
```
:::

## 7. Open it

```bash
NS=$(kubectl get dployinstance admin-podinfo -n dploy-system -o jsonpath='{.status.namespace}')
SVC=$(kubectl get svc -n "$NS" -o jsonpath='{.items[0].metadata.name}')
kubectl -n "$NS" port-forward "svc/$SVC" 9898:9898
# open http://localhost:9898
```

## 8. Clean up

```bash
# Delete the environment via the API (operator finalizer tears down the workload)
curl -s -X DELETE -H "Authorization: Bearer $TOKEN" http://localhost:8080/run/podinfo

# Stop the port-forwards, then remove everything
kill %1 %2 2>/dev/null
kind delete cluster --name dploy
```

## Next steps

- [Installation](/installation/) — production install with real images and OIDC
- [Templates & Instances](/concepts/templates/) — git charts, pools, parameters, and the
  [`ownerClaim`](/concepts/templates/#ownership) (e.g. `groups`) for team-shared environments
- [OIDC Providers](/deployment/oidc-providers/) — Authentik, Keycloak, and Dex in production
