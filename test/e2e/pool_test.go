//go:build e2e

// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

// poolBudget scales the wait with the pool size: the operator reconciles
// --instance-concurrency instances at a time (4 by default), and each one is a
// Helm install, so a 30-member pool is an order of magnitude slower to fill
// than a 3-member one. A flat timeout would either be flaky at 30 or waste
// minutes at 3.
func poolBudget(size int) time.Duration {
	d := instanceTimeout + time.Duration(size)*20*time.Second
	if ceiling := 25 * time.Minute; d > ceiling {
		return ceiling
	}
	return d
}

// TestPoolFillAndClaim is the warm-pool contract: members are provisioned
// before anyone asks, a claim takes one over instantly instead of waiting for a
// Helm install, and the pool refills itself back to size behind them.
func TestPoolFillAndClaim(t *testing.T) {
	requireCluster(t)
	ctx := context.Background()

	const size = 3
	createTemplate(ctx, t, newPoolTemplate("e2e-pool", size, 10))

	t.Logf("waiting for %d warm members", size)
	waitPoolAvailable(ctx, t, "e2e-pool", size, poolBudget(size))

	t.Run("warm members are anonymous", func(t *testing.T) {
		instances, err := instancesFor(ctx, "e2e-pool")
		if err != nil {
			t.Fatalf("list instances: %v", err)
		}
		for _, inst := range instances {
			if inst.Spec.Owner != "" {
				t.Errorf("unclaimed member %s has owner %q", inst.Name, inst.Spec.Owner)
			}
			if inst.Labels[dployv1alpha1.LabelClaimUID] != "" {
				t.Errorf("unclaimed member %s carries a claim-uid label", inst.Name)
			}
			if !inst.Spec.Pooled {
				t.Errorf("member %s is not marked pooled", inst.Name)
			}
		}
	})

	t.Run("template status counters agree", func(t *testing.T) {
		eventually(t, 90*time.Second, "status counters to settle", func() error {
			var tmpl dployv1alpha1.DployTemplate
			if err := k8s.Get(ctx, types.NamespacedName{Name: "e2e-pool", Namespace: testNS}, &tmpl); err != nil {
				return err
			}
			if tmpl.Status.PoolAvailable != size || tmpl.Status.PoolTotal != size {
				return fmt.Errorf("available=%d total=%d claimed=%d, want available=total=%d",
					tmpl.Status.PoolAvailable, tmpl.Status.PoolTotal, tmpl.Status.PoolClaimed, size)
			}
			return nil
		})
	})

	// The whole point of a pool: binding takes over a member that already
	// exists, so record which ones were warm and assert the claim landed on one
	// of them rather than provisioning a fresh instance.
	before, err := instancesFor(ctx, "e2e-pool")
	if err != nil {
		t.Fatalf("list instances: %v", err)
	}
	warm := map[string]bool{}
	for _, inst := range before {
		warm[inst.Name] = true
	}

	// The claim is created here rather than inside the subtest below: a subtest's
	// t.Cleanup fires when that subtest ends, which would release the member
	// again before the refill assertion could observe it claimed.
	createClaim(ctx, t, newClaim("e2e-pool-claim", "e2e-pool", "carol"))

	t.Run("a claim takes over a warm member", func(t *testing.T) {
		// A warm member is already installed, so binding is a label write —
		// it must not take anything like a provisioning budget.
		claim := waitClaimBound(ctx, t, "e2e-pool-claim", 2*time.Minute)

		if !warm[claim.Status.InstanceRef] {
			t.Errorf("claim bound %q, which was not one of the warm members %v",
				claim.Status.InstanceRef, keys(warm))
		}

		inst := waitInstancePhase(ctx, t, claim.Status.InstanceRef, 2*time.Minute, dployv1alpha1.PhaseClaimed)
		if inst.Labels[dployv1alpha1.LabelClaimUID] != string(claim.UID) {
			t.Errorf("claim-uid label = %q, want %q", inst.Labels[dployv1alpha1.LabelClaimUID], claim.UID)
		}
		if inst.Annotations[dployv1alpha1.AnnotationBoundAt] == "" {
			t.Error("bound-at annotation is missing — the TTL has no durable anchor")
		}
	})

	t.Run("the pool refills behind the claim", func(t *testing.T) {
		waitPoolAvailable(ctx, t, "e2e-pool", size, poolBudget(size))

		instances, err := instancesFor(ctx, "e2e-pool")
		if err != nil {
			t.Fatalf("list instances: %v", err)
		}
		phases := countPhases(instances)
		if phases[dployv1alpha1.PhaseClaimed] != 1 {
			t.Errorf("claimed=%d want 1 (all phases: %v)", phases[dployv1alpha1.PhaseClaimed], phases)
		}
		if len(instances) != size+1 {
			t.Errorf("total instances=%d, want %d warm + 1 claimed", len(instances), size)
		}
	})
}

// TestPoolScaling resizes a live pool in both directions. Shrinking is the
// interesting half: the operator has to pick victims out of the unclaimed set
// and converge on the new size without oscillating against its own fill loop.
func TestPoolScaling(t *testing.T) {
	requireCluster(t)
	ctx := context.Background()

	tmpl := createTemplate(ctx, t, newPoolTemplate("e2e-pool-scale", 2, 10))
	waitPoolAvailable(ctx, t, "e2e-pool-scale", 2, poolBudget(2))

	t.Run("growing the pool provisions the difference", func(t *testing.T) {
		setPoolSize(ctx, t, tmpl.Name, 5)
		waitPoolAvailable(ctx, t, "e2e-pool-scale", 5, poolBudget(5))
	})

	t.Run("shrinking the pool reclaims the surplus", func(t *testing.T) {
		setPoolSize(ctx, t, tmpl.Name, 1)
		waitPoolAvailable(ctx, t, "e2e-pool-scale", 1, deleteTimeout+2*time.Minute)

		instances, err := livePoolInstances(ctx, "e2e-pool-scale")
		if err != nil {
			t.Fatalf("list instances: %v", err)
		}
		if len(instances) != 1 {
			t.Errorf("after shrinking to 1 there are %d live instances (phases: %v)",
				len(instances), countPhases(instances))
		}

		// Converging once is not enough: a purge that races its own fill loop
		// would oscillate, and a single observation would not notice.
		settle := 45 * time.Second
		deadline := time.Now().Add(settle)
		for time.Now().Before(deadline) {
			instances, err := livePoolInstances(ctx, "e2e-pool-scale")
			if err != nil {
				t.Fatalf("list instances: %v", err)
			}
			if len(instances) != 1 {
				t.Fatalf("pool did not settle at 1: %d live instances (phases: %v)",
					len(instances), countPhases(instances))
			}
			time.Sleep(5 * time.Second)
		}

		// Leaving the pool is not the same as being gone: pin that the purged
		// members actually finish tearing down rather than lingering forever as
		// tombstones, which filtering them out of the checks above would hide.
		eventually(t, deleteTimeout, "the purged members to finish tearing down", func() error {
			all, err := instancesFor(ctx, "e2e-pool-scale")
			if err != nil {
				return err
			}
			if len(all) != 1 {
				return fmt.Errorf("%d instance objects remain (phases: %v)", len(all), countPhases(all))
			}
			return nil
		})
	})

	t.Run("shrinking to zero drains the pool", func(t *testing.T) {
		setPoolSize(ctx, t, tmpl.Name, 0)
		waitPoolAvailable(ctx, t, "e2e-pool-scale", 0, deleteTimeout)
	})
}

// TestPoolAtScale is the load case the pool exists for: E2E_POOL_SIZE warm
// members at once (30 in the documented run), then a full teardown. It checks
// the two things that only break under concurrency — every member converges,
// and deleting the template reclaims every namespace it created.
func TestPoolAtScale(t *testing.T) {
	requireCluster(t)
	if testing.Short() {
		t.Skip("scale test skipped in -short mode")
	}
	ctx := context.Background()

	size := poolSize
	tmpl := createTemplate(ctx, t, newPoolTemplate("e2e-pool-scale-load", size, size))

	start := time.Now()
	t.Logf("filling a pool of %d — this is the slow one", size)
	waitPoolAvailable(ctx, t, tmpl.Name, size, poolBudget(size))
	t.Logf("pool of %d warm in %s (%.1fs per member)", size,
		time.Since(start).Round(time.Second), time.Since(start).Seconds()/float64(size))

	instances, err := instancesFor(ctx, tmpl.Name)
	if err != nil {
		t.Fatalf("list instances: %v", err)
	}

	t.Run("every member got its own workload namespace", func(t *testing.T) {
		seen := map[string]string{}
		for _, inst := range instances {
			ns := inst.Status.Namespace
			if ns == "" {
				t.Errorf("instance %s has no workload namespace", inst.Name)
				continue
			}
			if prev, dup := seen[ns]; dup {
				t.Errorf("instances %s and %s share namespace %s", prev, inst.Name, ns)
			}
			seen[ns] = inst.Name
		}
		if len(seen) != size {
			t.Errorf("got %d distinct workload namespaces, want %d", len(seen), size)
		}
	})

	t.Run("every member reports a distinct URL", func(t *testing.T) {
		seen := map[string]bool{}
		for _, inst := range instances {
			if inst.Status.URL == "" {
				t.Errorf("instance %s has no URL", inst.Name)
				continue
			}
			if seen[inst.Status.URL] {
				t.Errorf("duplicate URL %s", inst.Status.URL)
			}
			seen[inst.Status.URL] = true
		}
	})

	// Deleting the template is the documented way to retire a pool, and it is
	// where leaks would show up: 30 namespaces and 30 HelmReleases all have to
	// go with it.
	t.Run("deleting the pool reclaims every namespace", func(t *testing.T) {
		namespaces := make([]string, 0, len(instances))
		for _, inst := range instances {
			namespaces = append(namespaces, inst.Status.Namespace)
		}

		delStart := time.Now()
		if err := k8s.Delete(ctx, &dployv1alpha1.DployTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: tmpl.Name, Namespace: testNS},
		}); err != nil {
			t.Fatalf("delete template: %v", err)
		}

		budget := poolBudget(size)
		eventually(t, budget, "all pool instances to be removed", func() error {
			remaining, err := instancesFor(ctx, tmpl.Name)
			if err != nil {
				return err
			}
			if len(remaining) != 0 {
				return fmt.Errorf("%d instances left (phases: %v)", len(remaining), countPhases(remaining))
			}
			return nil
		})

		eventually(t, budget, "all workload namespaces to be removed", func() error {
			var left []string
			for _, ns := range namespaces {
				gone, err := namespaceGone(ctx, ns)
				if err != nil {
					return err
				}
				if !gone {
					left = append(left, ns)
				}
			}
			if len(left) > 0 {
				return fmt.Errorf("%d namespaces left, e.g. %s", len(left), left[0])
			}
			return nil
		})
		t.Logf("pool of %d fully reclaimed in %s", size, time.Since(delStart).Round(time.Second))

		// The template object itself must go too — a stuck finalizer here would
		// be invisible above, since the instances are already gone.
		eventually(t, 2*time.Minute, "the template object to be removed", func() error {
			var got dployv1alpha1.DployTemplate
			err := k8s.Get(ctx, types.NamespacedName{Name: tmpl.Name, Namespace: testNS}, &got)
			if apierrors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
			return fmt.Errorf("template still present (finalizers: %v)", got.Finalizers)
		})
	})
}

// setPoolSize patches a template's pool size, retrying the read-modify-write on
// conflict — the operator writes status to the same object continuously.
func setPoolSize(ctx context.Context, t *testing.T, name string, size int) {
	t.Helper()
	eventually(t, 60*time.Second, fmt.Sprintf("pool size of %q to be set to %d", name, size), func() error {
		var tmpl dployv1alpha1.DployTemplate
		if err := k8s.Get(ctx, types.NamespacedName{Name: name, Namespace: testNS}, &tmpl); err != nil {
			return err
		}
		patch := client.MergeFrom(tmpl.DeepCopy())
		if tmpl.Spec.Pool == nil {
			tmpl.Spec.Pool = &dployv1alpha1.PoolSpec{}
		}
		tmpl.Spec.Pool.Size = size
		return k8s.Patch(ctx, &tmpl, patch)
	})
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
