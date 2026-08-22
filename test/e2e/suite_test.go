//go:build e2e

// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

// Package e2e drives the operator against a real cluster: real Flux, real Helm
// releases, real workload pods. Unlike the envtest suite in internal/controller
// — which stubs the instance controller because envtest has no Flux — nothing
// here is stubbed, so it is the only place the claim → instance → HelmRelease →
// workload chain is exercised end to end.
//
// It is excluded from `go test ./...` by the e2e build tag. See README.md for
// what the target cluster needs and how to run it.
package e2e

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

var (
	k8s        client.Client
	testScheme = runtime.NewScheme()

	// testNS is the namespace every test's templates and claims live in. The
	// operator is cluster-scoped, so the suite gets its own namespace rather
	// than sharing dploy-system with a real install.
	testNS string

	// ownedConfig records that the suite created the OperatorConfig singleton
	// and must therefore delete it again. A cluster that already had one is
	// left exactly as it was found.
	ownedConfig bool

	// skipReason is set when the cluster is unreachable or not prepared, so the
	// suite degrades to a skip instead of a wall of connection failures.
	skipReason string
)

// Knobs. Everything has a working default; only the context and pool size are
// routinely overridden.
var (
	kubeContext = envOr("E2E_KUBECONTEXT", "")
	chartRepo   = envOr("E2E_CHART_REPO", "https://github.com/AYDEV-FR/dploy-charts")
	chartPath   = envOr("E2E_CHART_PATH", "web-app")
	chartRev    = envOr("E2E_CHART_REVISION", "main")
	baseDomain  = envOr("E2E_BASE_DOMAIN", "e2e.cyber.local")
	poolSize    = envInt("E2E_POOL_SIZE", 5)
	keepNS      = os.Getenv("E2E_KEEP") != ""

	// Set when the operator is run outside the cluster, which makes the
	// in-cluster Deployment check meaningless rather than merely noisy.
	outOfCluster = os.Getenv("E2E_OPERATOR_OUT_OF_CLUSTER") != ""

	// Provisioning a pool member means a git fetch, a Helm install and a pod
	// pull, and the operator reconciles only --instance-concurrency at a time,
	// so the pool budget scales with its size instead of being a flat number.
	instanceTimeout = envDuration("E2E_INSTANCE_TIMEOUT", 5*time.Minute)
	deleteTimeout   = envDuration("E2E_DELETE_TIMEOUT", 4*time.Minute)
)

func TestMain(m *testing.M) {
	utilruntime.Must(clientgoscheme.AddToScheme(testScheme))
	utilruntime.Must(dployv1alpha1.AddToScheme(testScheme))
	utilruntime.Must(helmv2.AddToScheme(testScheme))
	utilruntime.Must(sourcev1.AddToScheme(testScheme))

	code := run(m)
	os.Exit(code)
}

// run owns the setup/teardown pair so deferred cleanup still runs on the paths
// where TestMain would otherwise os.Exit straight out of a failure.
func run(m *testing.M) int {
	cfg, err := restConfig()
	if err != nil {
		skipReason = fmt.Sprintf("no kubeconfig: %v", err)
		return m.Run()
	}
	k8s, err = client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		skipReason = fmt.Sprintf("build client: %v", err)
		return m.Run()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := preflight(ctx); err != nil {
		skipReason = err.Error()
		return m.Run()
	}
	if err := ensureOperatorConfig(ctx); err != nil {
		skipReason = fmt.Sprintf("operator config: %v", err)
		return m.Run()
	}

	testNS = fmt.Sprintf("dploy-e2e-%d", time.Now().Unix())
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   testNS,
		Labels: map[string]string{"dploy.dev/e2e": "true"},
	}}); err != nil {
		skipReason = fmt.Sprintf("create namespace %s: %v", testNS, err)
		return m.Run()
	}

	code := m.Run()
	teardown()
	return code
}

// teardown removes what the suite created. Templates go first and are waited
// on: deleting the namespace out from under a live instance would strand
// finalizers and leave the workload namespaces behind.
func teardown() {
	if keepNS {
		fmt.Printf("E2E_KEEP set — leaving namespace %s in place\n", testNS)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	_ = k8s.DeleteAllOf(ctx, &dployv1alpha1.DployInstanceClaim{}, client.InNamespace(testNS))
	_ = k8s.DeleteAllOf(ctx, &dployv1alpha1.DployTemplate{}, client.InNamespace(testNS))

	// Wait for the instances to drain so their workload namespaces go with them.
	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) {
		var list dployv1alpha1.DployInstanceList
		if err := k8s.List(ctx, &list, client.InNamespace(testNS)); err != nil || len(list.Items) == 0 {
			break
		}
		time.Sleep(3 * time.Second)
	}

	_ = k8s.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNS}})
	if ownedConfig {
		_ = k8s.Delete(ctx, &dployv1alpha1.OperatorConfig{ObjectMeta: metav1.ObjectMeta{Name: "default"}})
	}
}

// restConfig builds a client config, honoring E2E_KUBECONTEXT over the
// kubeconfig's current context so a stray `kubectl config use-context` cannot
// silently point the suite at the wrong cluster.
func restConfig() (*rest.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if kubeContext != "" {
		overrides.CurrentContext = kubeContext
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
}

// preflight fails fast with an actionable message when the cluster is missing a
// prerequisite, rather than letting every test time out on the same cause.
func preflight(ctx context.Context) error {
	var instances dployv1alpha1.DployInstanceList
	if err := k8s.List(ctx, &instances, client.Limit(1)); err != nil {
		return fmt.Errorf("dploy CRDs not installed (helm install the chart first): %w", err)
	}
	var releases helmv2.HelmReleaseList
	if err := k8s.List(ctx, &releases, client.Limit(1)); err != nil {
		return fmt.Errorf("flux HelmRelease CRD not installed (`flux install`): %w", err)
	}

	// Running the operator out of cluster (`go run ./cmd/operator` against this
	// kubeconfig) is the only way to exercise an unreleased change without a
	// working image build, and then there is no Deployment to find.
	if outOfCluster {
		return nil
	}

	var deploys appsv1.DeploymentList
	if err := k8s.List(ctx, &deploys, client.MatchingLabels{"app.kubernetes.io/component": "operator"}); err != nil {
		return fmt.Errorf("list operator deployments: %w", err)
	}
	for i := range deploys.Items {
		if deploys.Items[i].Status.ReadyReplicas > 0 {
			return nil
		}
	}
	// The label is a convention, not a contract — fall back to a name match so a
	// differently-labeled install is not reported as missing.
	for _, name := range []string{"dploy-operator", "dploy-system/dploy-operator"} {
		parts := strings.SplitN(name, "/", 2)
		key := types.NamespacedName{Name: parts[len(parts)-1], Namespace: "dploy-system"}
		var d appsv1.Deployment
		if err := k8s.Get(ctx, key, &d); err == nil && d.Status.ReadyReplicas > 0 {
			return nil
		}
	}
	return fmt.Errorf("no ready dploy operator deployment found")
}

// ensureOperatorConfig makes sure the cluster-scoped singleton exists. It is
// shared cluster state, so an existing one is reused untouched — overwriting a
// real install's defaults would be a nasty surprise on a shared cluster.
func ensureOperatorConfig(ctx context.Context) error {
	var existing dployv1alpha1.OperatorConfig
	err := k8s.Get(ctx, types.NamespacedName{Name: "default"}, &existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	cfg := &dployv1alpha1.OperatorConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec: dployv1alpha1.OperatorConfigSpec{
			DefaultEngine: dployv1alpha1.EngineFlux,
			BaseDomain:    baseDomain,
			Flux: dployv1alpha1.FluxConfig{
				Namespace: "flux-system",
				Interval:  "1m",
			},
			// The e2e cluster terminates no TLS, so advertise http like the dev
			// stack does — status.url is asserted against this.
			ConnectionURLTemplate: "http://{{ .Host }}",
			Defaults: dployv1alpha1.InstanceDefaults{
				TTLSeconds:          3600,
				ExtendSeconds:       1800,
				MaxExtends:          3,
				MaxInstancesPerUser: 100, // the pool tests file many claims at once
			},
		},
	}
	if err := k8s.Create(ctx, cfg); err != nil {
		return err
	}
	ownedConfig = true
	return nil
}

// requireCluster skips a test when the suite never reached a usable cluster.
func requireCluster(t *testing.T) {
	t.Helper()
	if skipReason != "" {
		t.Skip(skipReason)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
