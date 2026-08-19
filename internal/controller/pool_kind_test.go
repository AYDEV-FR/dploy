// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

// TestPoolRefillAgainstKind runs the pool maintainer against a real cluster.
//
// envtest cannot show this hazard: its API server is in-process, so a watch
// event and the cache update that follows it land in the same breath, and the
// re-reconcile a create triggers always sees that create. A real API server
// sits behind a socket and a network, so the gap between "my create returned"
// and "my cache knows about it" is wide enough to count the same empty slot
// twice — which is exactly how the quota bug showed up in kind and not here.
//
// Skipped unless DPLOY_KIND_CONTEXT names a kubeconfig context, e.g.
//
//	kind create cluster --name dploy-pool
//	kubectl apply -f config/crd/bases
//	DPLOY_KIND_CONTEXT=kind-dploy-pool go test ./internal/controller/ -run Kind -v
//
// Only DployTemplateReconciler runs: nothing else is needed to fill a pool, and
// leaving the instance controller out keeps Flux (and any real workload) out of
// a test about arithmetic.
func TestPoolRefillAgainstKind(t *testing.T) {
	kubeContext := os.Getenv("DPLOY_KIND_CONTEXT")
	if kubeContext == "" {
		t.Skip("set DPLOY_KIND_CONTEXT (e.g. kind-dploy-pool) to run the real-cluster pool repro")
	}

	var (
		size      = envInt("DPLOY_KIND_POOL_SIZE", 20)
		churn     = envInt("DPLOY_KIND_POOL_CHURN", 10)
		rounds    = envInt("DPLOY_KIND_POOL_ROUNDS", 3)
		sampleGap = 40 * time.Millisecond
	)

	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: kubeContext},
	).ClientConfig()
	if err != nil {
		t.Fatalf("build rest config for context %q: %v", kubeContext, err)
	}

	// Assertions read through a direct client: the point of the test is that the
	// controller's cached view and the cluster's truth disagree, so the test must
	// not share the controller's cache.
	direct, err := client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		t.Fatalf("build direct client: %v", err)
	}

	// The envtest suite in TestMain already registered a controller under this
	// name in the global metrics registry, and this manager is a second one in
	// the same process.
	skipNameValidation := true
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:     testScheme,
		Metrics:    metricsserver.Options{BindAddress: "0"},
		Controller: ctrlconfig.Controller{SkipNameValidation: &skipNameValidation},
	})
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}
	if err := (&DployTemplateReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager(mgr); err != nil {
		t.Fatalf("register template reconciler: %v", err)
	}

	ctx := t.Context()
	go func() {
		if err := mgr.Start(ctx); err != nil {
			t.Errorf("manager stopped: %v", err)
		}
	}()
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("cache never synced")
	}

	ns := &corev1.Namespace{}
	ns.Name = "kind-pool-" + shortUUID()
	if err := direct.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	t.Cleanup(func() {
		_ = direct.Delete(context.Background(), ns)
	})

	tmpl := &dployv1alpha1.DployTemplate{}
	tmpl.Name = "burst"
	tmpl.Namespace = ns.Name
	tmpl.Spec = dployv1alpha1.DployTemplateSpec{
		Enabled: true,
		Method:  dployv1alpha1.MethodPool,
		Pool:    &dployv1alpha1.PoolSpec{Size: size},
		Chart: dployv1alpha1.ChartSource{
			Type:           dployv1alpha1.ChartSourceGit,
			RepoURL:        "https://example.invalid/charts.git",
			Path:           "charts/app",
			TargetRevision: "main",
		},
	}
	if err := direct.Create(ctx, tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}

	live := func() []dployv1alpha1.DployInstance {
		var list dployv1alpha1.DployInstanceList
		if err := direct.List(ctx, &list, client.InNamespace(ns.Name),
			client.MatchingLabels{LabelTemplate: tmpl.Name, LabelPooled: "true"}); err != nil {
			t.Fatalf("list instances: %v", err)
		}
		out := make([]dployv1alpha1.DployInstance, 0, len(list.Items))
		for i := range list.Items {
			if list.Items[i].DeletionTimestamp.IsZero() && list.Items[i].Spec.Owner == "" {
				out = append(out, list.Items[i])
			}
		}
		return out
	}

	// Sample continuously rather than only between phases: an overshoot is
	// created during a burst, and waiting for the burst to end to look would miss
	// one that a later reconcile trims.
	peak := make(chan int, 1)
	peak <- 0
	sample := func() int {
		n := len(live())
		p := <-peak
		if n > p {
			p = n
			t.Logf("%d unclaimed members (pool size %d)", n, size)
		}
		peak <- p
		return n
	}
	current := func() int {
		p := <-peak
		peak <- p
		return p
	}

	waitFull := func(phase string) {
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			if sample() >= size {
				return
			}
			time.Sleep(sampleGap)
		}
		t.Fatalf("%s: pool never reached %d members", phase, size)
	}

	waitFull("fill")

	// Churn: taking members out in batches is what a CTF opening looks like. The
	// batches deliberately do not wait for the pool to settle — deleting again
	// while the previous refill is still in flight is what would make a reconcile
	// run against a cache that is behind on both the deletes and its own creates.
	for range rounds {
		members := live()
		for i := 0; i < churn && i < len(members); i++ {
			if err := direct.Delete(ctx, &members[i]); err != nil {
				t.Fatalf("delete pool member: %v", err)
			}
		}
		for range 10 {
			sample()
			time.Sleep(sampleGap)
		}
	}

	// Let the last burst land, then watch a while longer: the tail of a create
	// burst arrives after the count first hits the target.
	waitFull("refill")
	stop := time.Now().Add(8 * time.Second)
	for time.Now().Before(stop) {
		sample()
		time.Sleep(sampleGap)
	}

	if p := current(); p > size {
		t.Fatalf("pool overshot: peaked at %d unclaimed members for a pool of %d", p, size)
	}
	t.Logf("peak was %d unclaimed members for a pool of %d", current(), size)
}

// envInt reads a positive integer override, falling back to def.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
