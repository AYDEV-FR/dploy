// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

// The envtest suite runs the DployTemplate and DployInstanceClaim controllers
// against a real API server. The DployInstance controller is deliberately not
// started: it talks to Flux, which envtest has no CRDs for. instanceStub stands
// in for exactly the part the claim flow depends on — an instance eventually
// reporting a phase and a URL.
var (
	testEnv    *envtest.Environment
	k8sClient  client.Client
	testScheme = runtime.NewScheme()

	// envtestSkip is set when the kubebuilder assets are missing, so the suite
	// degrades to a skip instead of failing on machines that never ran
	// `make envtest`.
	envtestSkip string
)

func TestMain(m *testing.M) {
	utilruntime.Must(clientgoscheme.AddToScheme(testScheme))
	utilruntime.Must(dployv1alpha1.AddToScheme(testScheme))
	// controller-runtime insists on a logger being installed. Reconciler output is
	// noise across a dozen tests, so it is discarded unless DPLOY_TEST_LOGS asks
	// for it while debugging a failure.
	if os.Getenv("DPLOY_TEST_LOGS") != "" {
		logf.SetLogger(zap.New(zap.UseDevMode(true)))
	} else {
		logf.SetLogger(logr.Discard())
	}

	assets, err := envtestAssets()
	if err != nil {
		envtestSkip = err.Error()
		os.Exit(m.Run())
	}

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		BinaryAssetsDirectory: assets,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		panic(fmt.Sprintf("start envtest: %v", err))
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		panic(fmt.Sprintf("build test client: %v", err))
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  testScheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		panic(fmt.Sprintf("build manager: %v", err))
	}
	must((&DployTemplateReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme(), APIReader: mgr.GetAPIReader()}).SetupWithManager(mgr))
	must((&DployInstanceClaimReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		APIReader: mgr.GetAPIReader(),
	}).SetupWithManager(mgr))
	must((&instanceStub{Client: mgr.GetClient()}).SetupWithManager(mgr))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := mgr.Start(ctx); err != nil {
			panic(fmt.Sprintf("start manager: %v", err))
		}
	}()
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		panic("cache never synced")
	}

	code := m.Run()

	cancel()
	_ = testEnv.Stop()
	os.Exit(code)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// envtestAssets locates the control-plane binaries: an explicit KUBEBUILDER_ASSETS
// wins, otherwise the `make envtest` download under bin/envtest.
func envtestAssets() (string, error) {
	if p := os.Getenv("KUBEBUILDER_ASSETS"); p != "" {
		return p, nil
	}
	matches, _ := filepath.Glob(filepath.Join("..", "..", "bin", "envtest", "k8s", "*"))
	for _, m := range matches {
		if fi, err := os.Stat(filepath.Join(m, "kube-apiserver")); err == nil && !fi.IsDir() {
			return m, nil
		}
	}
	return "", fmt.Errorf("no envtest assets: set KUBEBUILDER_ASSETS or run `make envtest`")
}

// requireEnvtest skips a test when the control plane could not be started.
func requireEnvtest(t *testing.T) {
	t.Helper()
	if envtestSkip != "" {
		t.Skip(envtestSkip)
	}
}

// --- instance stub ---

// instanceStub stands in for DployInstanceReconciler. The real one materializes a
// Flux HelmRelease and projects its health back; envtest has no Flux, so without
// a stand-in no pool member would ever reach Available and nothing could be
// claimed. It reuses the production phase mapping (phaseFor) against a healthy
// engine so the phases under test are the real ones.
type instanceStub struct {
	client.Client
}

func (s *instanceStub) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var inst dployv1alpha1.DployInstance
	if err := s.Get(ctx, req.NamespacedName, &inst); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !inst.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	original := inst.DeepCopy()
	if inst.Status.UUID == "" {
		inst.Status.UUID = shortUUID()
	}
	if inst.Status.Namespace == "" {
		inst.Status.Namespace = workloadNamespace(inst.Spec.Owner, inst.Spec.TemplateRef, inst.Status.UUID)
	}
	inst.Status.Engine = dployv1alpha1.EngineFlux
	inst.Status.URL = "https://" + defaultHost(inst.Spec.TemplateRef, inst.Status.UUID, "test.dploy.dev")
	inst.Status.ConnectionType = dployv1alpha1.ConnectionWeb
	inst.Status.Health = "Healthy"
	apimeta.SetStatusCondition(&inst.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "InstallSucceeded",
		Message:            "stubbed healthy release",
		ObservedGeneration: inst.Generation,
	})
	inst.Status.Phase = phaseFor(&inst, helmReleaseState{
		readiness: readinessReady,
		status:    metav1.ConditionTrue,
		reason:    "InstallSucceeded",
		message:   "stubbed healthy release",
		health:    "Healthy",
	})
	inst.Status.ObservedGeneration = inst.Generation

	if err := s.Status().Patch(ctx, &inst, client.MergeFrom(original)); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

func (s *instanceStub) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dployv1alpha1.DployInstance{}).
		Named("instancestub").
		Complete(s)
}

// --- test helpers ---

const (
	pollInterval = 25 * time.Millisecond
	pollTimeout  = 20 * time.Second
)

// newNamespace creates an isolated namespace for one test.
func newNamespace(t *testing.T) string {
	t.Helper()
	name := "t-" + shortUUID()
	ns := &corev1.Namespace{}
	ns.Name = name
	if err := k8sClient.Create(context.Background(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	return name
}

// eventually polls cond until it returns true or the timeout elapses, failing the
// test with msg (and the last error from cond) otherwise.
func eventually(t *testing.T, msg string, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(pollTimeout)
	last := ""
	for time.Now().Before(deadline) {
		ok, detail := cond()
		if ok {
			return
		}
		last = detail
		time.Sleep(pollInterval)
	}
	t.Fatalf("timed out waiting for %s (last state: %s)", msg, last)
}

// consistently asserts cond holds for the whole window — used to prove a claim
// does not bind, which no single observation can establish.
func consistently(t *testing.T, d time.Duration, msg string, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok, detail := cond(); !ok {
			t.Fatalf("%s: %s", msg, detail)
		}
		time.Sleep(pollInterval)
	}
}

func getClaim(t *testing.T, ns, name string) *dployv1alpha1.DployInstanceClaim {
	t.Helper()
	var c dployv1alpha1.DployInstanceClaim
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, &c); err != nil {
		t.Fatalf("get claim %s/%s: %v", ns, name, err)
	}
	return &c
}

func listInstances(t *testing.T, ns string, opts ...client.ListOption) []dployv1alpha1.DployInstance {
	t.Helper()
	var list dployv1alpha1.DployInstanceList
	opts = append([]client.ListOption{client.InNamespace(ns)}, opts...)
	if err := k8sClient.List(context.Background(), &list, opts...); err != nil {
		t.Fatalf("list instances: %v", err)
	}
	live := make([]dployv1alpha1.DployInstance, 0, len(list.Items))
	for i := range list.Items {
		if list.Items[i].DeletionTimestamp.IsZero() {
			live = append(live, list.Items[i])
		}
	}
	return live
}

// makeTemplate builds a minimal but valid DployTemplate; mutate applies the
// test-specific bits before it is created.
func makeTemplate(t *testing.T, ns, name string, mutate func(*dployv1alpha1.DployTemplate)) *dployv1alpha1.DployTemplate {
	t.Helper()
	tmpl := &dployv1alpha1.DployTemplate{}
	tmpl.Name = name
	tmpl.Namespace = ns
	tmpl.Spec = dployv1alpha1.DployTemplateSpec{
		Enabled: true,
		Method:  dployv1alpha1.MethodOnDemand,
		Chart: dployv1alpha1.ChartSource{
			Type:           dployv1alpha1.ChartSourceGit,
			RepoURL:        "https://example.invalid/charts.git",
			Path:           "charts/app",
			TargetRevision: "main",
		},
	}
	if mutate != nil {
		mutate(tmpl)
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	return tmpl
}

// makeClaim creates a DployInstanceClaim and returns it.
func makeClaim(t *testing.T, ns, name, templateRef, owner string, mutate func(*dployv1alpha1.DployInstanceClaim)) *dployv1alpha1.DployInstanceClaim {
	t.Helper()
	claim := &dployv1alpha1.DployInstanceClaim{}
	claim.Name = name
	claim.Namespace = ns
	claim.Spec = dployv1alpha1.DployInstanceClaimSpec{
		TemplateRef: templateRef,
		Owner:       owner,
	}
	if mutate != nil {
		mutate(claim)
	}
	if err := k8sClient.Create(context.Background(), claim); err != nil {
		t.Fatalf("create claim: %v", err)
	}
	return claim
}

// waitForPoolReady blocks until the template reports n warm, claimable instances.
func waitForPoolReady(t *testing.T, ns, templateRef string, n int) {
	t.Helper()
	eventually(t, fmt.Sprintf("%d available pool instance(s) of %q", n, templateRef), func() (bool, string) {
		insts := listInstances(t, ns, client.MatchingLabels{LabelTemplate: templateRef, LabelPooled: "true"})
		avail := 0
		for i := range insts {
			if isClaimable(&insts[i]) {
				avail++
			}
		}
		return avail == n, fmt.Sprintf("%d available of %d total", avail, len(insts))
	})
}
