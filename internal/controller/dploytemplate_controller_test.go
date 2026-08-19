// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package controller

import (
	"fmt"
	"testing"
	"time"

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
