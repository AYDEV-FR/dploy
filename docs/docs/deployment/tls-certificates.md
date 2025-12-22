---
sidebar_position: 1
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# TLS Certificates

This guide covers TLS certificate management for Dploy environments, including security considerations and two recommended approaches: **Ingress with Wildcard Certificates** and **Gateway API**.

## Security Considerations

Before configuring TLS for dynamic environments, it's important to understand the security implications of different certificate strategies.

### Individual Certificates per Environment

Generating a certificate for each environment hostname (e.g., `john-abc12345.env.dploy.dev`) presents several issues:

#### Let's Encrypt Rate Limits

Let's Encrypt enforces [rate limits](https://letsencrypt.org/docs/rate-limits/) that can quickly become problematic:

- **50 certificates per registered domain per week** (e.g., `*.dploy.dev`)
- **5 duplicate certificates per week**
- **300 new orders per account per 3 hours**

With dynamic environments, you could easily hit these limits:
- 10 users creating 5 environments each = 50 certificates/week (limit reached)
- Environments recreated frequently multiply this problem

#### Certificate Transparency Logs

All publicly trusted certificates are logged in [Certificate Transparency (CT) logs](https://certificate.transparency.dev/). This means:

- **Every subdomain is publicly visible**: Anyone can see `john-abc12345.env.dploy.dev` was issued a certificate
- **User activity patterns exposed**: CT logs reveal when users create environments
- **Reconnaissance tool**: Attackers use CT logs to discover subdomains (e.g., [crt.sh](https://crt.sh))

Example query to find all your subdomains:
```
https://crt.sh/?q=%.env.dploy.dev
```

### Wildcard Certificates

Wildcard certificates (`*.env.dploy.dev`) solve these issues but introduce different considerations:

#### Advantages

- **Single certificate**: One certificate covers all environment subdomains
- **No rate limit issues**: Only one certificate to renew
- **Privacy**: Individual hostnames don't appear in CT logs (only `*.env.dploy.dev`)
- **Simpler management**: No per-environment certificate handling

#### Risks to Consider

- **Broader impact if compromised**: A stolen wildcard private key allows impersonating any subdomain
- **Requires DNS-01 challenge**: HTTP-01 validation doesn't work for wildcards
- **DNS provider API access**: Your cert-manager needs credentials to modify DNS records

#### Mitigation Strategies

1. **Limit the wildcard scope**: Use a dedicated subdomain (`*.env.dploy.dev`) rather than `*.dploy.dev`
2. **Short certificate lifetime**: Use shorter validity periods (cert-manager default: 90 days)
3. **Secure the private key**: Store in a dedicated namespace with restricted RBAC
4. **Monitor certificate usage**: Alert on unexpected certificate requests

---

## Prerequisites

Both approaches require:

- **cert-manager** installed in your cluster
- **DNS provider API credentials** for DNS-01 challenge
- **Ingress Controller** or **Gateway API controller** (examples provided for NGINX, Traefik, and Cilium)

### Install cert-manager

```bash
helm repo add jetstack https://charts.jetstack.io
helm repo update

helm install cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --create-namespace \
  --set crds.enabled=true
```

### Configure ClusterIssuer with DNS-01

Choose your DNS provider and create the appropriate ClusterIssuer.

#### Cloudflare

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cloudflare-api-token
  namespace: cert-manager
type: Opaque
stringData:
  api-token: "your-cloudflare-api-token"
---
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: admin@dploy.dev
    privateKeySecretRef:
      name: letsencrypt-prod-account
    solvers:
    - dns01:
        cloudflare:
          apiTokenSecretRef:
            name: cloudflare-api-token
            key: api-token
      selector:
        dnsZones:
          - "dploy.dev"
```

#### OVH

```yaml
# Requires the OVH webhook: https://github.com/baarde/cert-manager-webhook-ovh
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: admin@dploy.dev
    privateKeySecretRef:
      name: letsencrypt-prod-account
    solvers:
    - dns01:
        webhook:
          groupName: acme.dploy.dev
          solverName: ovh
          config:
            endpoint: ovh-eu
            applicationSecretRef:
              name: ovh-credentials
              key: application-secret
            consumerKeySecretRef:
              name: ovh-credentials
              key: consumer-key
```

#### Route53 (AWS)

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: admin@dploy.dev
    privateKeySecretRef:
      name: letsencrypt-prod-account
    solvers:
    - dns01:
        route53:
          region: eu-west-1
          # Use IRSA (recommended) or accessKeyIDSecretRef
          # accessKeyIDSecretRef:
          #   name: route53-credentials
          #   key: access-key-id
          # secretAccessKeySecretRef:
          #   name: route53-credentials
          #   key: secret-access-key
```

---

## Option 1: Ingress with Wildcard Certificate

This approach uses traditional Kubernetes Ingress resources with a wildcard certificate replicated to all environment namespaces.

### Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│  Namespace: cert-manager                                            │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │ Certificate: wildcard-env-dploy-dev                           │  │
│  │ Secret: wildcard-env-dploy-dev-tls                            │  │
│  │   annotations:                                                 │  │
│  │     replicator.v1.mittwald.de/replicate-to-matching: ...      │──┼──► Replicated
│  └───────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
                                   │
        ┌──────────────────────────┼──────────────────────────┐
        ▼                          ▼                          ▼
┌───────────────────┐    ┌───────────────────┐    ┌───────────────────┐
│ Namespace:        │    │ Namespace:        │    │ Namespace:        │
│ john-webterm-abc1 │    │ jane-vscode-def2  │    │ bob-jupyter-ghi3  │
│                   │    │                   │    │                   │
│ Secret (copy):    │    │ Secret (copy):    │    │ Secret (copy):    │
│ wildcard-env-tls  │    │ wildcard-env-tls  │    │ wildcard-env-tls  │
│        │          │    │        │          │    │        │          │
│        ▼          │    │        ▼          │    │        ▼          │
│ Ingress ──────────┼────│ Ingress          │    │ Ingress           │
│ TLS: wildcard-tls │    │ TLS: wildcard-tls│    │ TLS: wildcard-tls │
└───────────────────┘    └───────────────────┘    └───────────────────┘
```

### Step 1: Install kubernetes-replicator

[kubernetes-replicator](https://github.com/mittwald/kubernetes-replicator) automatically copies secrets across namespaces based on annotations.

```bash
helm repo add mittwald https://helm.mittwald.de
helm repo update

helm install kubernetes-replicator mittwald/kubernetes-replicator \
  --namespace kube-system
```

### Step 2: Create the Wildcard Certificate

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: wildcard-env-dploy-dev
  namespace: cert-manager
spec:
  secretName: wildcard-env-dploy-dev-tls
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
  dnsNames:
    - "*.env.dploy.dev"
  # Template for the generated secret
  secretTemplate:
    annotations:
      # Replicate to namespaces with dploy.dev/managed=true label
      replicator.v1.mittwald.de/replicate-to-matching: >
        dploy.dev/managed=true
```

:::info
Dploy automatically labels environment namespaces with `dploy.dev/managed=true`. The replicator will only copy the secret to these namespaces.
:::

### Step 3: Configure Your Ingress Controller

<Tabs>
<TabItem value="nginx" label="NGINX Ingress" default>

#### Install NGINX Ingress Controller

```bash
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx
helm repo update

helm install ingress-nginx ingress-nginx/ingress-nginx \
  --namespace ingress-nginx \
  --create-namespace
```

#### NGINX Ingress Configuration

No special configuration needed for TLS. NGINX Ingress automatically uses the secret referenced in the Ingress resource.

</TabItem>
<TabItem value="traefik" label="Traefik">

#### Install Traefik

```bash
helm repo add traefik https://traefik.github.io/charts
helm repo update

helm install traefik traefik/traefik \
  --namespace traefik-system \
  --create-namespace
```

#### Traefik Configuration

Ensure the `websecure` entrypoint is enabled:

```yaml
# Traefik Helm values
ports:
  websecure:
    port: 8443
    exposedPort: 443
    protocol: TCP
    tls:
      enabled: true
```

</TabItem>
</Tabs>

### Step 4: Configure Environment Helm Charts

Your environment Helm charts must reference the replicated secret.

<Tabs>
<TabItem value="nginx" label="NGINX Ingress" default>

```yaml
# values.yaml for environment charts (NGINX)
ingress:
  enabled: true
  className: nginx
  annotations: {}
```

```yaml
# templates/ingress.yaml
{{- if .Values.ingress.enabled }}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ .Release.Name }}
  annotations:
    {{- toYaml .Values.ingress.annotations | nindent 4 }}
spec:
  ingressClassName: {{ .Values.ingress.className }}
  tls:
    - hosts:
        - {{ .Values.ingressHost | quote }}
      secretName: wildcard-env-dploy-dev-tls
  rules:
    - host: {{ .Values.ingressHost | quote }}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: {{ .Release.Name }}
                port:
                  number: 80
{{- end }}
```

</TabItem>
<TabItem value="traefik" label="Traefik">

```yaml
# values.yaml for environment charts (Traefik)
ingress:
  enabled: true
  className: traefik
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: websecure
    traefik.ingress.kubernetes.io/router.tls: "true"
```

```yaml
# templates/ingress.yaml
{{- if .Values.ingress.enabled }}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ .Release.Name }}
  annotations:
    {{- toYaml .Values.ingress.annotations | nindent 4 }}
spec:
  ingressClassName: {{ .Values.ingress.className }}
  tls:
    - hosts:
        - {{ .Values.ingressHost | quote }}
      secretName: wildcard-env-dploy-dev-tls
  rules:
    - host: {{ .Values.ingressHost | quote }}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: {{ .Release.Name }}
                port:
                  number: 80
{{- end }}
```

</TabItem>
</Tabs>

---

## Option 2: Gateway API (Recommended)

Gateway API provides a cleaner architecture where TLS termination happens at a central Gateway, eliminating the need to replicate secrets.

### Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│  Namespace: gateway-system                                          │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │ Gateway: dploy-gateway                                         │  │
│  │   listeners:                                                   │  │
│  │     - hostname: "*.env.dploy.dev"                              │  │
│  │       tls:                                                     │  │
│  │         certificateRefs:                                       │  │
│  │           - name: wildcard-env-dploy-dev-tls ◄─────────────────┼──┼── Single certificate
│  │       allowedRoutes:                                           │  │
│  │         namespaces:                                            │  │
│  │           from: Selector                                       │  │
│  │           selector:                                            │  │
│  │             matchLabels:                                       │  │
│  │               dploy.dev/managed: "true"                        │  │
│  └───────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
                                   │
        ┌──────────────────────────┼──────────────────────────┐
        ▼                          ▼                          ▼
┌───────────────────┐    ┌───────────────────┐    ┌───────────────────┐
│ Namespace:        │    │ Namespace:        │    │ Namespace:        │
│ john-webterm-abc1 │    │ jane-vscode-def2  │    │ bob-jupyter-ghi3  │
│ label:            │    │ label:            │    │ label:            │
│  dploy.dev/managed│    │  dploy.dev/managed│    │  dploy.dev/managed│
│                   │    │                   │    │                   │
│ HTTPRoute ────────┼────┼─► Gateway         │    │ HTTPRoute         │
│ (no TLS config!)  │    │                   │    │ (no TLS config!)  │
└───────────────────┘    └───────────────────┘    └───────────────────┘
```

### Advantages over Ingress

| Aspect | Ingress + Replicator | Gateway API |
|--------|----------------------|-------------|
| Certificate location | Replicated to N namespaces | Single namespace |
| Secret exposure | In every environment namespace | Isolated in gateway namespace |
| Dependency | Requires replicator controller | Native Kubernetes |
| Configuration | TLS in each Ingress | TLS only in Gateway |
| Access control | RBAC on secrets | `allowedRoutes` selector |

### Step 1: Install Gateway API CRDs

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/standard-install.yaml
```

### Step 2: Install and Configure Your Gateway Controller

<Tabs>
<TabItem value="traefik" label="Traefik" default>

#### Install Traefik with Gateway API support

```bash
helm repo add traefik https://traefik.github.io/charts
helm repo update

helm install traefik traefik/traefik \
  --namespace traefik-system \
  --create-namespace \
  --set "providers.kubernetesGateway.enabled=true" \
  --set "gateway.enabled=false"
```

Or update an existing installation:

```yaml
# Traefik Helm values
providers:
  kubernetesGateway:
    enabled: true

gateway:
  enabled: false  # We'll create our own Gateway
```

#### GatewayClass

Traefik automatically creates a GatewayClass named `traefik`. Verify it exists:

```bash
kubectl get gatewayclass traefik
```

</TabItem>
<TabItem value="cilium" label="Cilium">

#### Install Cilium with Gateway API support

If installing Cilium fresh:

```bash
helm repo add cilium https://helm.cilium.io
helm repo update

helm install cilium cilium/cilium \
  --namespace kube-system \
  --set gatewayAPI.enabled=true \
  --set kubeProxyReplacement=true \
  --set k8sServiceHost=<API_SERVER_IP> \
  --set k8sServicePort=<API_SERVER_PORT>
```

Or enable Gateway API on an existing Cilium installation:

```bash
helm upgrade cilium cilium/cilium \
  --namespace kube-system \
  --reuse-values \
  --set gatewayAPI.enabled=true
```

#### GatewayClass

Cilium creates a GatewayClass named `cilium`. Verify it exists:

```bash
kubectl get gatewayclass cilium
```

:::info
Cilium Gateway API requires `kubeProxyReplacement=true` for full functionality. See the [Cilium Gateway API documentation](https://docs.cilium.io/en/stable/network/servicemesh/gateway-api/gateway-api/) for details.
:::

</TabItem>
</Tabs>

### Step 3: Create the Certificate

The certificate must be created in the same namespace as the Gateway.

<Tabs>
<TabItem value="traefik" label="Traefik" default>

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: wildcard-env-dploy-dev
  namespace: traefik-system
spec:
  secretName: wildcard-env-dploy-dev-tls
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
  dnsNames:
    - "*.env.dploy.dev"
```

</TabItem>
<TabItem value="cilium" label="Cilium">

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: wildcard-env-dploy-dev
  namespace: kube-system  # Or a dedicated namespace for your Gateway
spec:
  secretName: wildcard-env-dploy-dev-tls
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
  dnsNames:
    - "*.env.dploy.dev"
```

:::tip
For better security isolation, consider creating a dedicated namespace (e.g., `cilium-gateway`) for your Gateway and certificates instead of using `kube-system`.
:::

</TabItem>
</Tabs>

### Step 4: Create the Gateway

<Tabs>
<TabItem value="traefik" label="Traefik" default>

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: dploy-gateway
  namespace: traefik-system
spec:
  gatewayClassName: traefik
  listeners:
    # HTTPS listener for environment subdomains
    - name: https-envs
      hostname: "*.env.dploy.dev"
      port: 443
      protocol: HTTPS
      tls:
        mode: Terminate
        certificateRefs:
          - name: wildcard-env-dploy-dev-tls
            kind: Secret
      allowedRoutes:
        namespaces:
          from: Selector
          selector:
            matchLabels:
              dploy.dev/managed: "true"

    # Optional: HTTPS listener for main Dploy UI
    - name: https-main
      hostname: "dploy.dev"
      port: 443
      protocol: HTTPS
      tls:
        mode: Terminate
        certificateRefs:
          - name: dploy-main-tls
            kind: Secret
      allowedRoutes:
        namespaces:
          from: Same
```

</TabItem>
<TabItem value="cilium" label="Cilium">

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: dploy-gateway
  namespace: kube-system  # Or your dedicated gateway namespace
spec:
  gatewayClassName: cilium
  listeners:
    # HTTPS listener for environment subdomains
    - name: https-envs
      hostname: "*.env.dploy.dev"
      port: 443
      protocol: HTTPS
      tls:
        mode: Terminate
        certificateRefs:
          - name: wildcard-env-dploy-dev-tls
            kind: Secret
      allowedRoutes:
        namespaces:
          from: Selector
          selector:
            matchLabels:
              dploy.dev/managed: "true"

    # Optional: HTTPS listener for main Dploy UI
    - name: https-main
      hostname: "dploy.dev"
      port: 443
      protocol: HTTPS
      tls:
        mode: Terminate
        certificateRefs:
          - name: dploy-main-tls
            kind: Secret
      allowedRoutes:
        namespaces:
          from: Same
```

:::note
Cilium Gateway API creates a LoadBalancer service automatically. Check the external IP:

```bash
kubectl get svc -n kube-system cilium-gateway-dploy-gateway
```
:::

</TabItem>
</Tabs>

### Step 5: Configure Environment Helm Charts

Environment charts use HTTPRoute instead of Ingress. The HTTPRoute configuration is the same regardless of the Gateway controller.

<Tabs>
<TabItem value="traefik" label="Traefik" default>

```yaml
# templates/httproute.yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{ .Release.Name }}
  namespace: {{ .Release.Namespace }}
spec:
  parentRefs:
    - name: dploy-gateway
      namespace: traefik-system
  hostnames:
    - {{ .Values.ingressHost | quote }}
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: {{ .Release.Name }}
          port: 80
```

</TabItem>
<TabItem value="cilium" label="Cilium">

```yaml
# templates/httproute.yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{ .Release.Name }}
  namespace: {{ .Release.Namespace }}
spec:
  parentRefs:
    - name: dploy-gateway
      namespace: kube-system  # Or your dedicated gateway namespace
  hostnames:
    - {{ .Values.ingressHost | quote }}
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: {{ .Release.Name }}
          port: 80
```

</TabItem>
</Tabs>

:::tip
With Gateway API, the HTTPRoute doesn't need any TLS configuration. The Gateway handles all TLS termination.
:::

### Step 6: Create ReferenceGrant (if required)

Some Gateway implementations require explicit permission for cross-namespace references.

<Tabs>
<TabItem value="traefik" label="Traefik" default>

```yaml
apiVersion: gateway.networking.k8s.io/v1beta1
kind: ReferenceGrant
metadata:
  name: allow-dploy-routes
  namespace: traefik-system
spec:
  from:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      namespace: "*"
  to:
    - group: ""
      kind: Service
    - group: gateway.networking.k8s.io
      kind: Gateway
```

</TabItem>
<TabItem value="cilium" label="Cilium">

```yaml
apiVersion: gateway.networking.k8s.io/v1beta1
kind: ReferenceGrant
metadata:
  name: allow-dploy-routes
  namespace: kube-system  # Or your dedicated gateway namespace
spec:
  from:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      namespace: "*"
  to:
    - group: ""
      kind: Service
    - group: gateway.networking.k8s.io
      kind: Gateway
```

:::info
Cilium is generally permissive with cross-namespace references, but adding a ReferenceGrant is recommended for security best practices.
:::

</TabItem>
</Tabs>

---

## Comparison Summary

| Criteria | Ingress + Replicator | Gateway API |
|----------|----------------------|-------------|
| **Setup complexity** | Medium | Medium |
| **Secret security** | Lower (replicated everywhere) | Higher (single location) |
| **Additional dependencies** | kubernetes-replicator | None (native K8s) |
| **Controller support** | NGINX, Traefik, any Ingress controller | Traefik (v2.10+), Cilium, Envoy, Istio |
| **Future-proof** | Legacy approach | Kubernetes standard |
| **Debugging** | Familiar tooling | Newer, improving documentation |

### Controller Comparison

| Feature | NGINX Ingress | Traefik | Cilium |
|---------|---------------|---------|--------|
| **Ingress support** | Yes | Yes | Yes |
| **Gateway API support** | Limited (NGINX Gateway Fabric) | Yes (v2.10+) | Yes |
| **CNI integration** | No | No | Yes (native) |
| **eBPF acceleration** | No | No | Yes |
| **Best for** | Traditional setups | Flexibility, middleware | High performance, security |

### Recommendation

- **New deployments**: Use Gateway API for better security and cleaner architecture
- **Existing Ingress setups**: Ingress + Replicator works well and is easier to retrofit
- **Cilium users**: Gateway API is the natural choice with native integration
- **Performance-critical**: Cilium Gateway API with eBPF provides best performance
- **Multi-cluster**: Gateway API with shared Gateway definitions scales better

---

## Troubleshooting

### Certificate not issued

```bash
# Check certificate status
kubectl describe certificate wildcard-env-dploy-dev -n cert-manager

# Check cert-manager logs
kubectl logs -n cert-manager -l app=cert-manager

# Check DNS-01 challenge
kubectl get challenges -A

# Check order status
kubectl get orders -A
```

### Secret not replicated (Ingress approach)

```bash
# Check replicator logs
kubectl logs -n kube-system -l app.kubernetes.io/name=kubernetes-replicator

# Verify namespace labels
kubectl get ns -l dploy.dev/managed=true

# Check secret annotations
kubectl get secret wildcard-env-dploy-dev-tls -n cert-manager -o yaml

# Verify secret exists in target namespace
kubectl get secret wildcard-env-dploy-dev-tls -n <environment-namespace>
```

### Ingress not working

<Tabs>
<TabItem value="nginx" label="NGINX Ingress" default>

```bash
# Check NGINX Ingress controller logs
kubectl logs -n ingress-nginx -l app.kubernetes.io/name=ingress-nginx

# Check Ingress status
kubectl describe ingress <name> -n <namespace>

# Verify IngressClass exists
kubectl get ingressclass nginx
```

</TabItem>
<TabItem value="traefik" label="Traefik">

```bash
# Check Traefik logs
kubectl logs -n traefik-system -l app.kubernetes.io/name=traefik

# Check Traefik entrypoints
kubectl get svc -n traefik-system

# Verify TLS is enabled on websecure
kubectl get deployment traefik -n traefik-system -o yaml | grep -A 20 "args:"

# Check IngressClass
kubectl get ingressclass traefik
```

</TabItem>
</Tabs>

### HTTPRoute not working (Gateway API)

<Tabs>
<TabItem value="traefik" label="Traefik" default>

```bash
# Check Gateway status
kubectl describe gateway dploy-gateway -n traefik-system

# Check HTTPRoute status
kubectl describe httproute <name> -n <namespace>

# Verify GatewayClass exists
kubectl get gatewayclass traefik

# Check Traefik logs for Gateway API
kubectl logs -n traefik-system -l app.kubernetes.io/name=traefik
```

</TabItem>
<TabItem value="cilium" label="Cilium">

```bash
# Check Gateway status
kubectl describe gateway dploy-gateway -n kube-system

# Check HTTPRoute status
kubectl describe httproute <name> -n <namespace>

# Verify GatewayClass exists
kubectl get gatewayclass cilium

# Check Cilium Gateway API service
kubectl get svc -n kube-system -l io.cilium.gateway/owning-gateway=dploy-gateway

# Check Cilium agent logs
kubectl logs -n kube-system -l k8s-app=cilium | grep -i gateway

# Verify Cilium Gateway API is enabled
cilium config view | grep gateway
```

</TabItem>
</Tabs>

### Gateway not getting external IP

```bash
# Check LoadBalancer service status
kubectl get svc -A | grep -i gateway

# For cloud providers, check cloud controller logs
kubectl logs -n kube-system -l app=cloud-controller-manager

# For bare metal, ensure MetalLB or similar is configured
kubectl get svc -n metallb-system
```
