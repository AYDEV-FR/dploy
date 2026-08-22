//go:build e2e

// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

// valuesTemplate is what every e2e template deploys: the smallest web-app the
// charts repo can render. Resource requests are deliberately tiny — the pool
// tests run dozens of these at once and the point is to exercise the operator,
// not to fill the node. networkPolicy is off because the chart's default is a
// CTF-style deny-all that has nothing to do with what is under test.
const valuesTemplate = `image:
  repository: nginxinc/nginx-unprivileged
  tag: "1.27-alpine"
resources:
  requests:
    cpu: "10m"
    memory: "32Mi"
  limits:
    cpu: "500m"
    memory: "128Mi"
networkPolicy:
  enabled: false
`

// newTemplate builds an on-demand template. Callers mutate the returned object
// before creating it — poolTemplate is the pool-shaped variant.
func newTemplate(name string) *dployv1alpha1.DployTemplate {
	return &dployv1alpha1.DployTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec: dployv1alpha1.DployTemplateSpec{
			DisplayName: name,
			Description: "dploy e2e fixture",
			Enabled:     true,
			Method:      dployv1alpha1.MethodOnDemand,
			Chart: dployv1alpha1.ChartSource{
				Type:           dployv1alpha1.ChartSourceGit,
				RepoURL:        chartRepo,
				Path:           chartPath,
				TargetRevision: chartRev,
			},
			TTL:            &dployv1alpha1.TTLSpec{Seconds: 3600},
			ValuesTemplate: valuesTemplate,
		},
	}
}

// newPoolTemplate builds a warm-pool template of the given size.
func newPoolTemplate(name string, size, maxSize int) *dployv1alpha1.DployTemplate {
	tmpl := newTemplate(name)
	tmpl.Spec.Method = dployv1alpha1.MethodPool
	tmpl.Spec.Pool = &dployv1alpha1.PoolSpec{Size: size, MaxSize: maxSize}
	return tmpl
}

// newClaim builds a claim for a template.
func newClaim(name, templateRef, owner string) *dployv1alpha1.DployInstanceClaim {
	return &dployv1alpha1.DployInstanceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec: dployv1alpha1.DployInstanceClaimSpec{
			TemplateRef: templateRef,
			Owner:       owner,
		},
	}
}

// createTemplate creates a template and registers its teardown. Cleanup deletes
// the template and waits for its instances to drain, because the next test's
// pool counts are only meaningful once the previous one has actually gone.
func createTemplate(ctx context.Context, t *testing.T, tmpl *dployv1alpha1.DployTemplate) *dployv1alpha1.DployTemplate {
	t.Helper()
	if err := k8s.Create(ctx, tmpl); err != nil {
		t.Fatalf("create template %s: %v", tmpl.Name, err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), deleteTimeout)
		defer cancel()
		_ = k8s.Delete(cctx, &dployv1alpha1.DployTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: tmpl.Name, Namespace: testNS},
		})
		_ = waitGone(cctx, tmpl.Name, deleteTimeout)
	})
	return tmpl
}

// createClaim creates a claim and registers its deletion.
func createClaim(ctx context.Context, t *testing.T, claim *dployv1alpha1.DployInstanceClaim) {
	t.Helper()
	if err := k8s.Create(ctx, claim); err != nil {
		t.Fatalf("create claim %s: %v", claim.Name, err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), deleteTimeout)
		defer cancel()
		_ = k8s.Delete(cctx, &dployv1alpha1.DployInstanceClaim{
			ObjectMeta: metav1.ObjectMeta{Name: claim.Name, Namespace: testNS},
		})
	})
}

// eventually polls until check returns nil or the budget runs out, reporting
// the last error on failure. Everything the operator does is asynchronous, so
// this is the only way any of these assertions can be written.
func eventually(t *testing.T, timeout time.Duration, desc string, check func() error) {
	t.Helper()
	var last error
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if last = check(); last == nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("timed out after %s waiting for %s: %v", timeout, desc, last)
}

// instancesFor lists the instances belonging to a template.
func instancesFor(ctx context.Context, template string) ([]dployv1alpha1.DployInstance, error) {
	var list dployv1alpha1.DployInstanceList
	err := k8s.List(ctx, &list,
		client.InNamespace(testNS),
		client.MatchingLabels{dployv1alpha1.LabelTemplate: template},
	)
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// livePoolInstances is the pool as the operator itself counts it: an instance
// carrying a deletionTimestamp has already left the pool, even though its
// object lingers while Flux tears the environment down. That teardown takes
// tens of seconds against a real cluster; under envtest the instance
// controller is stubbed and the object disappears at once, which is why an
// assertion on the raw count can pass there and fail here.
func livePoolInstances(ctx context.Context, template string) ([]dployv1alpha1.DployInstance, error) {
	all, err := instancesFor(ctx, template)
	if err != nil {
		return nil, err
	}
	live := make([]dployv1alpha1.DployInstance, 0, len(all))
	for i := range all {
		if all[i].DeletionTimestamp.IsZero() {
			live = append(live, all[i])
		}
	}
	return live, nil
}

// countPhases buckets a template's instances by phase — the shape most pool
// assertions want, and a readable failure message when they miss.
func countPhases(instances []dployv1alpha1.DployInstance) map[dployv1alpha1.InstancePhase]int {
	out := map[dployv1alpha1.InstancePhase]int{}
	for i := range instances {
		out[instances[i].Status.Phase]++
	}
	return out
}

// waitPoolAvailable waits until a template holds exactly want warm, unclaimed
// members. It counts Available instances rather than reading status counters so
// a stale status cannot make the assertion pass on its own.
func waitPoolAvailable(ctx context.Context, t *testing.T, template string, want int, timeout time.Duration) {
	t.Helper()
	eventually(t, timeout, fmt.Sprintf("%d available pool members of %q", want, template), func() error {
		instances, err := instancesFor(ctx, template)
		if err != nil {
			return err
		}
		phases := countPhases(instances)
		if got := phases[dployv1alpha1.PhaseAvailable]; got != want {
			return fmt.Errorf("available=%d want %d (all phases: %v)", got, want, phases)
		}
		return nil
	})
}

// waitClaimBound waits for a claim to reach Bound and returns it.
func waitClaimBound(ctx context.Context, t *testing.T, name string, timeout time.Duration) *dployv1alpha1.DployInstanceClaim {
	t.Helper()
	var claim dployv1alpha1.DployInstanceClaim
	eventually(t, timeout, fmt.Sprintf("claim %q to bind", name), func() error {
		if err := k8s.Get(ctx, types.NamespacedName{Name: name, Namespace: testNS}, &claim); err != nil {
			return err
		}
		if claim.Status.Phase != dployv1alpha1.ClaimBound {
			return fmt.Errorf("phase=%q instance=%q", claim.Status.Phase, claim.Status.InstanceRef)
		}
		if claim.Status.InstanceRef == "" {
			return fmt.Errorf("bound with no instanceRef")
		}
		return nil
	})
	return &claim
}

// waitInstancePhase waits for a named instance to reach one of the given phases.
func waitInstancePhase(ctx context.Context, t *testing.T, name string, timeout time.Duration, want ...dployv1alpha1.InstancePhase) *dployv1alpha1.DployInstance {
	t.Helper()
	var inst dployv1alpha1.DployInstance
	eventually(t, timeout, fmt.Sprintf("instance %q to reach %v", name, want), func() error {
		if err := k8s.Get(ctx, types.NamespacedName{Name: name, Namespace: testNS}, &inst); err != nil {
			return err
		}
		for _, w := range want {
			if inst.Status.Phase == w {
				return nil
			}
		}
		return fmt.Errorf("phase=%q health=%q", inst.Status.Phase, inst.Status.Health)
	})
	return &inst
}

// waitGone waits until a template has no instances left.
func waitGone(ctx context.Context, template string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		instances, err := instancesFor(ctx, template)
		if err != nil {
			return err
		}
		if len(instances) == 0 {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("instances of %q still present after %s", template, timeout)
}

// namespaceGone reports whether a workload namespace is fully gone. Terminating
// counts as still present: the operator's cleanup is only done once the API
// server has actually removed it.
func namespaceGone(ctx context.Context, name string) (bool, error) {
	if name == "" {
		return true, nil
	}
	var ns corev1.Namespace
	err := k8s.Get(ctx, types.NamespacedName{Name: name}, &ns)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}
