// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

// countPooled returns the live pool members of a template, split into unclaimed
// and claimed.
func countPooled(t *testing.T, ns, templateRef string) (unclaimed, claimed int) {
	t.Helper()
	insts := listInstances(t, ns, client.MatchingLabels{
		LabelTemplate: templateRef,
		LabelPooled:   "true",
	})
	// Indexed rather than ranged by value: a DployInstance is 536 bytes and
	// this runs per poll in a loop that waits on a pool.
	for i := range insts {
		if insts[i].Spec.Owner == "" {
			unclaimed++
		} else {
			claimed++
		}
	}
	return unclaimed, claimed
}

// TestPoolFillsExactlyOnce pins the refill arithmetic against the informer.
//
// Filling a pool creates N instances in one reconcile, and every one of those
// creates fires an instance event that enqueues the template again. If such a
// re-reconcile ran against a cache that had not caught up, the "count what I
// can see, create the difference" loop would count the same empty slot twice
// and fill it twice — and nothing downstream would undo it, since the extra
// members are legitimate warm instances costing a workload namespace and a Helm
// release each until their TTL.
//
// It holds because the loop completes inside one reconcile while the cache
// converges underneath it; see the comment on the fill loop for the two changes
// that would break it.
func TestPoolFillsExactlyOnce(t *testing.T) {
	requireEnvtest(t)
	ns := newNamespace(t)
	const size = 6

	makeTemplate(t, ns, "burst", func(tm *dployv1alpha1.DployTemplate) {
		tm.Spec.Method = dployv1alpha1.MethodPool
		tm.Spec.Pool = &dployv1alpha1.PoolSpec{Size: size}
	})

	eventually(t, fmt.Sprintf("%d pool members", size), func() (bool, string) {
		unclaimed, _ := countPooled(t, ns, "burst")
		return unclaimed >= size, fmt.Sprintf("%d unclaimed", unclaimed)
	})

	// The overshoot is transient in the sense that it happens during the burst —
	// but it is never undone, so observing the pool for a moment after it fills
	// is enough to catch it.
	consistently(t, 3*time.Second, "pool overshot its size", func() (bool, string) {
		unclaimed, _ := countPooled(t, ns, "burst")
		return unclaimed <= size, fmt.Sprintf("%d unclaimed members for a pool of %d", unclaimed, size)
	})
}

// TestPoolRefillsExactlyOnceAfterClaims is the same property on the refill
// path, where the burst comes from outside: claims taking several members at
// once enqueue the template while its own refill is still in flight.
func TestPoolRefillsExactlyOnceAfterClaims(t *testing.T) {
	requireEnvtest(t)
	ns := newNamespace(t)
	const size = 4
	const claims = 3

	makeTemplate(t, ns, "churn", func(tm *dployv1alpha1.DployTemplate) {
		tm.Spec.Method = dployv1alpha1.MethodPool
		tm.Spec.Pool = &dployv1alpha1.PoolSpec{Size: size}
		tm.Spec.MaxInstancesPerUser = new(claims)
	})
	waitForPoolReady(t, ns, "churn", size)

	for i := range claims {
		makeClaim(t, ns, fmt.Sprintf("c%d", i), "churn", fmt.Sprintf("owner-%d", i), nil)
	}

	eventually(t, "the pool to refill after the claims", func() (bool, string) {
		unclaimed, claimed := countPooled(t, ns, "churn")
		return claimed == claims && unclaimed >= size,
			fmt.Sprintf("%d unclaimed, %d claimed", unclaimed, claimed)
	})

	consistently(t, 3*time.Second, "pool overshot its size after refilling", func() (bool, string) {
		unclaimed, _ := countPooled(t, ns, "churn")
		return unclaimed <= size, fmt.Sprintf("%d unclaimed members for a pool of %d", unclaimed, size)
	})
}

// setPoolSize patches a template's pool size, retrying on the conflicts the
// controller's own status writes produce.
func setPoolSize(t *testing.T, ns, name string, size int) {
	t.Helper()
	eventually(t, fmt.Sprintf("pool size of %q set to %d", name, size), func() (bool, string) {
		var tmpl dployv1alpha1.DployTemplate
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Name: name, Namespace: ns}, &tmpl); err != nil {
			return false, err.Error()
		}
		patch := client.MergeFrom(tmpl.DeepCopy())
		if tmpl.Spec.Pool == nil {
			tmpl.Spec.Pool = &dployv1alpha1.PoolSpec{}
		}
		tmpl.Spec.Pool.Size = size
		if err := k8sClient.Patch(context.Background(), &tmpl, patch); err != nil {
			return false, err.Error()
		}
		return true, ""
	})
}

// TestPoolShrinkRemovesSurplus is the purge path: lowering pool.size has to
// reclaim the members that are now surplus.
//
// Before the purge existed this was a silent no-op — the reconciler only ever
// filled, and an unclaimed member never expires on its own because applyTTL
// starts the clock at Ready or Claimed and a warm member sits in Available.
func TestPoolShrinkRemovesSurplus(t *testing.T) {
	requireEnvtest(t)
	ns := newNamespace(t)

	makeTemplate(t, ns, "shrink", func(tm *dployv1alpha1.DployTemplate) {
		tm.Spec.Method = dployv1alpha1.MethodPool
		tm.Spec.Pool = &dployv1alpha1.PoolSpec{Size: 5}
	})
	waitForPoolReady(t, ns, "shrink", 5)

	setPoolSize(t, ns, "shrink", 2)

	eventually(t, "the pool to shrink to 2", func() (bool, string) {
		unclaimed, _ := countPooled(t, ns, "shrink")
		return unclaimed == 2, fmt.Sprintf("%d unclaimed", unclaimed)
	})

	// And it settles there rather than oscillating between the fill and purge
	// paths, which is the failure mode a naive implementation would have.
	consistently(t, 3*time.Second, "pool did not settle at 2", func() (bool, string) {
		unclaimed, _ := countPooled(t, ns, "shrink")
		return unclaimed == 2, fmt.Sprintf("%d unclaimed", unclaimed)
	})
}

// TestPoolShrinkSparesClaimedInstances is the safety property: shrinking must
// only ever take from the unclaimed set. Destroying someone's live environment
// to satisfy a size change would be the worst possible reading of "shrink".
func TestPoolShrinkSparesClaimedInstances(t *testing.T) {
	requireEnvtest(t)
	ns := newNamespace(t)

	makeTemplate(t, ns, "shrink-claimed", func(tm *dployv1alpha1.DployTemplate) {
		tm.Spec.Method = dployv1alpha1.MethodPool
		tm.Spec.Pool = &dployv1alpha1.PoolSpec{Size: 3}
	})
	waitForPoolReady(t, ns, "shrink-claimed", 3)

	makeClaim(t, ns, "holder", "shrink-claimed", "alice", nil)
	eventually(t, "the claim to bind", func() (bool, string) {
		_, claimed := countPooled(t, ns, "shrink-claimed")
		return claimed == 1, fmt.Sprintf("%d claimed", claimed)
	})

	setPoolSize(t, ns, "shrink-claimed", 0)

	eventually(t, "the warm members to drain", func() (bool, string) {
		unclaimed, _ := countPooled(t, ns, "shrink-claimed")
		return unclaimed == 0, fmt.Sprintf("%d unclaimed", unclaimed)
	})

	// The claimed one is still standing, and stays that way.
	consistently(t, 3*time.Second, "a claimed instance was purged", func() (bool, string) {
		_, claimed := countPooled(t, ns, "shrink-claimed")
		return claimed == 1, fmt.Sprintf("%d claimed", claimed)
	})
}

// TestPoolPurgedWhenMethodLeavesPool covers the leak a method switch used to
// open: acquirePooled only runs for MethodPool, so warm members left behind by
// a template that is no longer pooled can never be claimed by anyone.
func TestPoolPurgedWhenMethodLeavesPool(t *testing.T) {
	requireEnvtest(t)
	ns := newNamespace(t)

	makeTemplate(t, ns, "switcher", func(tm *dployv1alpha1.DployTemplate) {
		tm.Spec.Method = dployv1alpha1.MethodPool
		tm.Spec.Pool = &dployv1alpha1.PoolSpec{Size: 3}
	})
	waitForPoolReady(t, ns, "switcher", 3)

	eventually(t, "the method to flip to on-demand", func() (bool, string) {
		var tmpl dployv1alpha1.DployTemplate
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Name: "switcher", Namespace: ns}, &tmpl); err != nil {
			return false, err.Error()
		}
		patch := client.MergeFrom(tmpl.DeepCopy())
		tmpl.Spec.Method = dployv1alpha1.MethodOnDemand
		if err := k8sClient.Patch(context.Background(), &tmpl, patch); err != nil {
			return false, err.Error()
		}
		return true, ""
	})

	eventually(t, "the orphaned warm members to be reclaimed", func() (bool, string) {
		unclaimed, _ := countPooled(t, ns, "switcher")
		return unclaimed == 0, fmt.Sprintf("%d unclaimed", unclaimed)
	})
}

// TestPoolSurvivesDisable pins the deliberate exception: a disabled template
// keeps its warm set. Disabling is usually temporary, and draining would make
// re-enabling pay the full provisioning cost again.
func TestPoolSurvivesDisable(t *testing.T) {
	requireEnvtest(t)
	ns := newNamespace(t)

	makeTemplate(t, ns, "disabled-pool", func(tm *dployv1alpha1.DployTemplate) {
		tm.Spec.Method = dployv1alpha1.MethodPool
		tm.Spec.Pool = &dployv1alpha1.PoolSpec{Size: 2}
	})
	waitForPoolReady(t, ns, "disabled-pool", 2)

	eventually(t, "the template to be disabled", func() (bool, string) {
		var tmpl dployv1alpha1.DployTemplate
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Name: "disabled-pool", Namespace: ns}, &tmpl); err != nil {
			return false, err.Error()
		}
		patch := client.MergeFrom(tmpl.DeepCopy())
		tmpl.Spec.Enabled = false
		if err := k8sClient.Patch(context.Background(), &tmpl, patch); err != nil {
			return false, err.Error()
		}
		return true, ""
	})

	consistently(t, 3*time.Second, "a disabled template lost its warm pool", func() (bool, string) {
		unclaimed, _ := countPooled(t, ns, "disabled-pool")
		return unclaimed == 2, fmt.Sprintf("%d unclaimed", unclaimed)
	})
}

// TestPurgeVictimOrder pins which member is destroyed first. The ranking is the
// difference between shrinking a pool and degrading it: a Failed member is worth
// nothing, one still provisioning has no user waiting on it, and an Available
// member is the only kind anyone can claim right now — so Available goes last,
// and among equals the newest goes first because the oldest has been proven
// healthy for longest.
func TestPurgeVictimOrder(t *testing.T) {
	now := metav1.Now()
	older := metav1.NewTime(now.Add(-time.Hour))

	mk := func(name string, phase dployv1alpha1.InstancePhase, created metav1.Time) *dployv1alpha1.DployInstance {
		inst := &dployv1alpha1.DployInstance{}
		inst.Name = name
		inst.CreationTimestamp = created
		inst.Status.Phase = phase
		return inst
	}

	tests := []struct {
		name  string
		input []*dployv1alpha1.DployInstance
		want  []string
	}{
		{
			name: "failed first, then provisioning, then available",
			input: []*dployv1alpha1.DployInstance{
				mk("available", dployv1alpha1.PhaseAvailable, now),
				mk("provisioning", dployv1alpha1.PhaseProvisioning, now),
				mk("failed", dployv1alpha1.PhaseFailed, now),
			},
			want: []string{"failed", "provisioning", "available"},
		},
		{
			name: "newest first among equals",
			input: []*dployv1alpha1.DployInstance{
				mk("old", dployv1alpha1.PhaseAvailable, older),
				mk("new", dployv1alpha1.PhaseAvailable, now),
			},
			want: []string{"new", "old"},
		},
		{
			name: "name breaks ties so the choice is stable",
			input: []*dployv1alpha1.DployInstance{
				mk("b", dployv1alpha1.PhaseAvailable, now),
				mk("a", dployv1alpha1.PhaseAvailable, now),
			},
			want: []string{"a", "b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sortPurgeVictims(tc.input)
			names := make([]string, len(got))
			for i := range got {
				names[i] = got[i].Name
			}
			for i := range tc.want {
				if names[i] != tc.want[i] {
					t.Fatalf("order = %v, want %v", names, tc.want)
				}
			}
		})
	}
}
