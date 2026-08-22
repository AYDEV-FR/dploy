//go:build e2e

// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

// TestTTLExpiry proves the clock is real: an instance past its TTL is torn down
// and its claim becomes a tombstone rather than silently keeping a dead
// reference. For a claim-created instance the expiry is anchored at the binding
// (the claim controller stamps spec.expiresAt, which applyTTL prefers over its
// own "first became active" anchor), so the clock is already running while the
// instance provisions.
func TestTTLExpiry(t *testing.T) {
	requireCluster(t)
	ctx := context.Background()

	// The TTL has to outlast provisioning, not race it. The clock starts at the
	// binding, so a 30s lifetime reaps the instance before it is ever Ready on
	// any cluster slower than a workstation — the assertion below then fails on
	// "not found" and reads like a bug in expiry rather than a budget too tight
	// to observe it.
	tmpl := newTemplate("e2e-ttl")
	tmpl.Spec.TTL = &dployv1alpha1.TTLSpec{Seconds: 120}
	createTemplate(ctx, t, tmpl)
	createClaim(ctx, t, newClaim("e2e-ttl-claim", "e2e-ttl", "dave"))

	claim := waitClaimBound(ctx, t, "e2e-ttl-claim", instanceTimeout)
	inst := waitInstancePhase(ctx, t, claim.Status.InstanceRef, instanceTimeout, dployv1alpha1.PhaseReady)
	workloadNS := inst.Status.Namespace

	if claim.Status.ExpiresAt == nil {
		t.Fatal("a 120s TTL produced no expiry")
	}

	eventually(t, 6*time.Minute, "the expired instance to be reaped", func() error {
		var got dployv1alpha1.DployInstance
		err := k8s.Get(ctx, types.NamespacedName{Name: inst.Name, Namespace: testNS}, &got)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("still present in phase %q", got.Status.Phase)
	})

	t.Run("the claim survives as a tombstone", func(t *testing.T) {
		waitClaimPhase(ctx, t, "e2e-ttl-claim", dployv1alpha1.ClaimExpired, 2*time.Minute)
	})

	t.Run("the workload namespace goes with it", func(t *testing.T) {
		eventually(t, deleteTimeout, "the workload namespace to be reclaimed", func() error {
			gone, err := namespaceGone(ctx, workloadNS)
			if err != nil {
				return err
			}
			if !gone {
				return fmt.Errorf("namespace %s still present", workloadNS)
			}
			return nil
		})
	})
}

// TestTTLExtend covers the extend path: raising the claim's requested lifetime
// has to move the expiry the operator enforces, not just the field.
func TestTTLExtend(t *testing.T) {
	requireCluster(t)
	ctx := context.Background()

	tmpl := newTemplate("e2e-extend")
	tmpl.Spec.TTL = &dployv1alpha1.TTLSpec{Seconds: 600, ExtendSeconds: 600, MaxExtends: 3}
	createTemplate(ctx, t, tmpl)
	createClaim(ctx, t, newClaim("e2e-extend-claim", "e2e-extend", "erin"))

	claim := waitClaimBound(ctx, t, "e2e-extend-claim", instanceTimeout)
	if claim.Status.ExpiresAt == nil {
		t.Fatal("bound claim has no expiry")
	}
	before := claim.Status.ExpiresAt.Time

	// Extending is a patch that raises the requested TTL.
	eventually(t, 60*time.Second, "the TTL to be raised", func() error {
		var got dployv1alpha1.DployInstanceClaim
		if err := k8s.Get(ctx, types.NamespacedName{Name: claim.Name, Namespace: testNS}, &got); err != nil {
			return err
		}
		patch := client.MergeFrom(got.DeepCopy())
		got.Spec.TTLSeconds = 3600
		return k8s.Patch(ctx, &got, patch)
	})

	eventually(t, 2*time.Minute, "the enforced expiry to move out", func() error {
		var got dployv1alpha1.DployInstanceClaim
		if err := k8s.Get(ctx, types.NamespacedName{Name: claim.Name, Namespace: testNS}, &got); err != nil {
			return err
		}
		if got.Status.ExpiresAt == nil {
			return fmt.Errorf("expiry disappeared")
		}
		if !got.Status.ExpiresAt.After(before) {
			return fmt.Errorf("expiry still %s (was %s)", got.Status.ExpiresAt.Time, before)
		}
		return nil
	})
}

// TestPerOwnerQuota pins the refusal that protects the cluster: past the
// template's per-owner cap, further claims are Rejected outright rather than
// provisioning and being cleaned up afterwards.
func TestPerOwnerQuota(t *testing.T) {
	requireCluster(t)
	ctx := context.Background()

	quota := 2
	tmpl := newTemplate("e2e-quota")
	tmpl.Spec.MaxInstancesPerUser = &quota
	createTemplate(ctx, t, tmpl)

	for i := range 2 {
		createClaim(ctx, t, newClaim(fmt.Sprintf("e2e-quota-%d", i), "e2e-quota", "frank"))
	}
	for i := range 2 {
		waitClaimBound(ctx, t, fmt.Sprintf("e2e-quota-%d", i), instanceTimeout)
	}

	createClaim(ctx, t, newClaim("e2e-quota-over", "e2e-quota", "frank"))
	waitClaimPhase(ctx, t, "e2e-quota-over", dployv1alpha1.ClaimRejected, 2*time.Minute)

	t.Run("the refused claim provisioned nothing", func(t *testing.T) {
		instances, err := instancesFor(ctx, "e2e-quota")
		if err != nil {
			t.Fatalf("list instances: %v", err)
		}
		if len(instances) != quota {
			t.Errorf("%d instances for a quota of %d", len(instances), quota)
		}
	})

	t.Run("a different owner is unaffected", func(t *testing.T) {
		createClaim(ctx, t, newClaim("e2e-quota-other", "e2e-quota", "grace"))
		waitClaimBound(ctx, t, "e2e-quota-other", instanceTimeout)
	})
}

// TestConcurrentClaimsRaceForOneMember is the binding race under real
// contention. With a pool of one and no headroom to refill, exactly one of the
// competing claims may win — the optimistic lock on the claim-UID label is what
// makes that true, and nothing about it is observable in a single-claim test.
func TestConcurrentClaimsRaceForOneMember(t *testing.T) {
	requireCluster(t)
	ctx := context.Background()

	// maxSize caps total instances, so once the single member is claimed the
	// pool cannot refill and the losers have nothing to fall back on.
	createTemplate(ctx, t, newPoolTemplate("e2e-race", 1, 1))
	waitPoolAvailable(ctx, t, "e2e-race", 1, poolBudget(1))

	const claimants = 5
	var wg sync.WaitGroup
	for i := range claimants {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claim := newClaim(fmt.Sprintf("e2e-race-%d", i), "e2e-race", fmt.Sprintf("racer%d", i))
			// Without this the losers fall back to a dedicated on-demand
			// instance (WaitForPool defaults to false on a raw CR, unlike the
			// API's default), and the contention under test never happens.
			claim.Spec.WaitForPool = true
			_ = k8s.Create(ctx, claim)
		}(i)
	}
	wg.Wait()

	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), deleteTimeout)
		defer cancel()
		for i := range claimants {
			_ = k8s.Delete(cctx, &dployv1alpha1.DployInstanceClaim{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("e2e-race-%d", i), Namespace: testNS},
			})
		}
	})

	// Exactly one winner, and it stays exactly one — a late reconcile handing a
	// second claim the same member is the failure this guards.
	deadline := time.Now().Add(90 * time.Second)
	sawWinner := false
	for time.Now().Before(deadline) {
		var list dployv1alpha1.DployInstanceClaimList
		if err := k8s.List(ctx, &list, client.InNamespace(testNS)); err != nil {
			t.Fatalf("list claims: %v", err)
		}
		bound := map[string]string{}
		holders := map[string]string{}
		for i := range list.Items {
			c := &list.Items[i]
			if c.Spec.TemplateRef != "e2e-race" || c.Status.Phase != dployv1alpha1.ClaimBound {
				continue
			}
			bound[c.Name] = c.Status.InstanceRef
			// Two claims naming the same instance is the corruption this test
			// exists for: the claim-UID label is written under optimistic
			// concurrency precisely so it cannot happen.
			if prev, dup := holders[c.Status.InstanceRef]; dup {
				t.Fatalf("claims %q and %q both hold instance %q", prev, c.Name, c.Status.InstanceRef)
			}
			holders[c.Status.InstanceRef] = c.Name
		}
		if len(bound) > 1 {
			t.Fatalf("%d claims bound at once for a pool of 1 with waitForPool: %v", len(bound), bound)
		}
		if len(bound) == 1 {
			sawWinner = true
		}
		time.Sleep(3 * time.Second)
	}
	if !sawWinner {
		t.Error("no claim ever won the single warm member")
	}
}

// TestWaitForPoolFallback covers the two answers to an empty pool: park the
// claim, or provision a dedicated instance instead.
func TestWaitForPoolFallback(t *testing.T) {
	requireCluster(t)
	ctx := context.Background()

	// The pool must be empty for both answers to be observable, and it must not
	// refill behind the test. Capping it at the pool size did both at once, but
	// that cap now also forbids the on-demand instance the second subtest is
	// about: one claimed member already sits at a cap of 1. Leave room for
	// exactly one fallback, then close the pool to keep it empty.
	tmpl := createTemplate(ctx, t, newPoolTemplate("e2e-wait", 1, 2))
	waitPoolAvailable(ctx, t, "e2e-wait", 1, poolBudget(1))
	createClaim(ctx, t, newClaim("e2e-wait-holder", "e2e-wait", "holder"))
	waitClaimBound(ctx, t, "e2e-wait-holder", 2*time.Minute)
	setPoolSize(ctx, t, tmpl.Name, 0)

	t.Run("waitForPool parks the claim as Pending", func(t *testing.T) {
		claim := newClaim("e2e-wait-parked", "e2e-wait", "waiter")
		claim.Spec.WaitForPool = true
		createClaim(ctx, t, claim)

		// Pending is the absence of an event, so it needs a window, not a poll.
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			var got dployv1alpha1.DployInstanceClaim
			if err := k8s.Get(ctx, types.NamespacedName{Name: claim.Name, Namespace: testNS}, &got); err != nil {
				t.Fatalf("get claim: %v", err)
			}
			if got.Status.Phase == dployv1alpha1.ClaimBound {
				t.Fatalf("a waiting claim bound %q even though the pool was capped and full",
					got.Status.InstanceRef)
			}
			time.Sleep(3 * time.Second)
		}
	})

	t.Run("waitForPool=false falls back to an on-demand instance", func(t *testing.T) {
		claim := newClaim("e2e-wait-fallback", "e2e-wait", "impatient")
		claim.Spec.WaitForPool = false
		createClaim(ctx, t, claim)

		bound := waitClaimBound(ctx, t, claim.Name, instanceTimeout)
		var inst dployv1alpha1.DployInstance
		if err := k8s.Get(ctx, types.NamespacedName{Name: bound.Status.InstanceRef, Namespace: testNS}, &inst); err != nil {
			t.Fatalf("get instance: %v", err)
		}
		if inst.Spec.Pooled {
			t.Error("the fallback handed out a pool member instead of a dedicated instance")
		}
		if inst.Spec.Owner != "impatient" {
			t.Errorf("instance owner = %q, want impatient", inst.Spec.Owner)
		}
	})
}

// TestPoolMaxSizeCap pins the cap: maxSize bounds idle + claimed together, so a
// pool that is fully claimed does not keep provisioning replacements.
func TestPoolMaxSizeCap(t *testing.T) {
	requireCluster(t)
	ctx := context.Background()

	createTemplate(ctx, t, newPoolTemplate("e2e-maxsize", 2, 3))
	waitPoolAvailable(ctx, t, "e2e-maxsize", 2, poolBudget(2))

	// Claim both warm members. Refilling to size 2 would need 4 instances total,
	// which the cap of 3 forbids — so exactly one replacement may appear.
	for i := range 2 {
		createClaim(ctx, t, newClaim(fmt.Sprintf("e2e-maxsize-%d", i), "e2e-maxsize", fmt.Sprintf("user%d", i)))
	}
	for i := range 2 {
		waitClaimBound(ctx, t, fmt.Sprintf("e2e-maxsize-%d", i), 3*time.Minute)
	}

	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		instances, err := instancesFor(ctx, "e2e-maxsize")
		if err != nil {
			t.Fatalf("list instances: %v", err)
		}
		if len(instances) > 3 {
			t.Fatalf("pool grew to %d instances despite maxSize=3 (phases: %v)",
				len(instances), countPhases(instances))
		}
		time.Sleep(5 * time.Second)
	}
}

// TestTemplateDeletionReclaimsLiveInstances deletes a template while an instance
// is claimed and in use — the operator has to take the whole tree with it rather
// than orphaning the workload namespace.
func TestTemplateDeletionReclaimsLiveInstances(t *testing.T) {
	requireCluster(t)
	ctx := context.Background()

	tmpl := newTemplate("e2e-tmpl-delete")
	if err := k8s.Create(ctx, tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	createClaim(ctx, t, newClaim("e2e-tmpl-delete-claim", "e2e-tmpl-delete", "heidi"))

	claim := waitClaimBound(ctx, t, "e2e-tmpl-delete-claim", instanceTimeout)
	inst := waitInstancePhase(ctx, t, claim.Status.InstanceRef, instanceTimeout, dployv1alpha1.PhaseReady)
	workloadNS := inst.Status.Namespace

	if err := k8s.Delete(ctx, &dployv1alpha1.DployTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: tmpl.Name, Namespace: testNS},
	}); err != nil {
		t.Fatalf("delete template: %v", err)
	}

	eventually(t, deleteTimeout, "the live instance to be reclaimed", func() error {
		remaining, err := instancesFor(ctx, tmpl.Name)
		if err != nil {
			return err
		}
		if len(remaining) != 0 {
			return fmt.Errorf("%d instances left (phases: %v)", len(remaining), countPhases(remaining))
		}
		return nil
	})

	eventually(t, deleteTimeout, "its workload namespace to be reclaimed", func() error {
		gone, err := namespaceGone(ctx, workloadNS)
		if err != nil {
			return err
		}
		if !gone {
			return fmt.Errorf("namespace %s still present", workloadNS)
		}
		return nil
	})
}

// TestClaimReleaseRecyclesMember covers release: giving a warm member back must
// not hand the next user a dirty environment, so the default is to destroy and
// replace it rather than return it to the pool as-is.
func TestClaimReleaseRecyclesMember(t *testing.T) {
	requireCluster(t)
	ctx := context.Background()

	createTemplate(ctx, t, newPoolTemplate("e2e-recycle", 1, 4))
	waitPoolAvailable(ctx, t, "e2e-recycle", 1, poolBudget(1))

	createClaim(ctx, t, newClaim("e2e-recycle-claim", "e2e-recycle", "ivan"))
	claim := waitClaimBound(ctx, t, "e2e-recycle-claim", 2*time.Minute)
	claimed := claim.Status.InstanceRef

	if err := k8s.Delete(ctx, &dployv1alpha1.DployInstanceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: claim.Name, Namespace: testNS},
	}); err != nil {
		t.Fatalf("delete claim: %v", err)
	}

	eventually(t, deleteTimeout, "the released member to be destroyed", func() error {
		var got dployv1alpha1.DployInstance
		err := k8s.Get(ctx, types.NamespacedName{Name: claimed, Namespace: testNS}, &got)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("released member %s still present in phase %q", claimed, got.Status.Phase)
	})

	// And the pool comes back to strength with a fresh member.
	waitPoolAvailable(ctx, t, "e2e-recycle", 1, poolBudget(1))
	instances, err := instancesFor(ctx, "e2e-recycle")
	if err != nil {
		t.Fatalf("list instances: %v", err)
	}
	for i := range instances {
		if instances[i].Name == claimed {
			t.Errorf("the recycled member %s came back", claimed)
		}
	}
}
