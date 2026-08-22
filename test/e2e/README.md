# End-to-end tests

These drive the operator against a **real cluster** — real Flux, real Helm
releases, real workload pods. Nothing is stubbed, which is what separates them
from the envtest suite in `internal/controller`: that one replaces the instance
controller with `instanceStub`, because envtest has no Flux. The chain
`claim → DployInstance → GitRepository + HelmRelease → workload namespace → pod`
is only exercised here.

They are excluded from `go test ./...` by the `e2e` build tag, so they never run
in the normal test job.

## What the cluster needs

1. **Flux** — the source and helm controllers:
   ```sh
   flux install --components=source-controller,helm-controller
   ```
2. **The CRDs and the operator**, e.g. operator-only (API scaled to zero):
   ```sh
   helm upgrade --install dploy ./charts/dploy \
     --namespace dploy-system --create-namespace \
     --set replicaCount=0 \
     --set operator.image.tag=main \
     --set auth.jwksURL=https://idp.invalid/jwks \
     --set auth.jwtIssuer=https://idp.invalid
   ```
   `operator.image.tag` matters: the default follows `Chart.appVersion`, and no
   image is published under that tag until the release is cut.

No ingress controller and no StorageClass are required — the fixture chart is
stateless and nothing asserts that a URL resolves, only that the operator
advertises the right one.

## Running

```sh
make test-e2e KUBECONTEXT=cyber                  # default pool size (5)
make test-e2e-scale KUBECONTEXT=cyber            # the 30-member pool run
E2E_KUBECONTEXT=cyber go test -tags e2e -v -run TestOnDemandLifecycle ./test/e2e/
```

The suite **skips** rather than fails when the cluster is unreachable or a
prerequisite is missing, and says which one.

## Knobs

| Variable | Default | Meaning |
|---|---|---|
| `E2E_KUBECONTEXT` | kubeconfig's current context | Which cluster to drive. Set it — a stray `kubectl config use-context` otherwise decides for you. |
| `E2E_POOL_SIZE` | `5` | Members in `TestPoolAtScale`. |
| `E2E_CHART_REPO` | `https://github.com/AYDEV-FR/dploy-charts` | Git repo holding the fixture chart. |
| `E2E_CHART_PATH` | `web-app` | Chart path within that repo. |
| `E2E_CHART_REVISION` | `main` | Git revision. |
| `E2E_BASE_DOMAIN` | `e2e.cyber.local` | `OperatorConfig.baseDomain`, asserted against `status.url`. |
| `E2E_INSTANCE_TIMEOUT` | `5m` | Budget for one instance to converge. |
| `E2E_DELETE_TIMEOUT` | `4m` | Budget for teardown assertions. |
| `E2E_KEEP` | unset | Leave the test namespace behind for inspection. |
| `E2E_OPERATOR_OUT_OF_CLUSTER` | unset | Skip the in-cluster operator check. Set it when running the operator locally (`go run ./cmd/operator`) against the target cluster — the usual way to test an unreleased change. |

## What it touches

Everything lands in a generated `dploy-e2e-<timestamp>` namespace, plus the
workload namespaces the operator creates, all removed on teardown. The one piece
of shared cluster state is the **cluster-scoped `OperatorConfig` singleton**: the
suite creates it only if absent and deletes it only if it created it, so an
existing install is left exactly as found.

## Notes

`TestPoolScaling` covers both directions, including shrinking to zero. Shrinking
used to be a silent no-op — the template reconciler was fill-only and an
unclaimed member never expires, because `applyTTL` starts the clock only at
`Ready` or `Claimed`. The purge path added alongside these tests fixes that; it
only ever takes from the *unclaimed* set, and deletes are conditional on the
observed `resourceVersion` so a claim that lands mid-purge wins the race.

Running the suite against an operator image that predates the purge will fail
`shrinking_the_pool_reclaims_the_surplus` — that is the test doing its job, not
a flake.
