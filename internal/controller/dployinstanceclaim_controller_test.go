// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
	"github.com/AYDEV-FR/dploy/internal/operatorconfig"
)

// waitClaimPhase blocks until the claim reports the wanted phase.
func waitClaimPhase(t *testing.T, ns, name string, want dployv1alpha1.ClaimPhase) *dployv1alpha1.DployInstanceClaim {
	t.Helper()
	var out *dployv1alpha1.DployInstanceClaim
	eventually(t, fmt.Sprintf("claim %q to reach phase %s", name, want), func() (bool, string) {
		c := getClaim(t, ns, name)
		out = c
		return c.Status.Phase == want, fmt.Sprintf("phase=%q", c.Status.Phase)
	})
	return out
}

// poolTemplate builds a pool-method template with a fixed, hard-capped pool.
func poolTemplate(t *testing.T, ns, name string, size int) *dployv1alpha1.DployTemplate {
	t.Helper()
	return makeTemplate(t, ns, name, func(tmpl *dployv1alpha1.DployTemplate) {
		tmpl.Spec.Method = dployv1alpha1.MethodPool
		// MaxSize pins the pool: without it the template controller refills every
		// claimed slot, and a test about scarcity would never see any.
		tmpl.Spec.Pool = &dployv1alpha1.PoolSpec{Size: size, MaxSize: size}
	})
}

// TestClaimBindsWarmPoolInstance walks the whole kubectl-only cycle: apply a
// claim against a warm pool, watch it go Bound with a connection URL projected
// from the instance, and confirm the claim owns the instance it was handed.
func TestClaimBindsWarmPoolInstance(t *testing.T) {
	requireEnvtest(t)
	ns := newNamespace(t)
	tmpl := poolTemplate(t, ns, "webterm", 1)
	waitForPoolReady(t, ns, tmpl.Name, 1)

	claim := makeClaim(t, ns, "alice-webterm", tmpl.Name, "alice", func(c *dployv1alpha1.DployInstanceClaim) {
		c.Spec.Params = map[string]string{"email": "alice@example.com"}
	})

	bound := waitClaimPhase(t, ns, claim.Name, dployv1alpha1.ClaimBound)
	if bound.Status.InstanceRef == "" {
		t.Fatal("bound claim has no instanceRef")
	}
	eventually(t, "the connection URL to be projected onto the claim", func() (bool, string) {
		c := getClaim(t, ns, claim.Name)
		return c.Status.ConnectionURL != "" && c.Status.UUID != "", fmt.Sprintf("url=%q uuid=%q", c.Status.ConnectionURL, c.Status.UUID)
	})

	final := getClaim(t, ns, claim.Name)
	if final.Status.ExpiresAt == nil {
		t.Error("bound claim has no expiry")
	}
	if final.Status.BoundAt == nil {
		t.Fatal("bound claim has no boundAt anchor")
	}
	if c := apimeta.FindStatusCondition(final.Status.Conditions, dployv1alpha1.ConditionBound); c == nil || c.Status != metav1.ConditionTrue {
		t.Errorf("Bound condition = %+v, want True", c)
	}

	// The instance carries the requester's identity and request context, and the
	// pool member has been handed off: the claim is now its sole (controller)
	// owner, which is what makes `kubectl delete dclaim` tear the environment down.
	var inst dployv1alpha1.DployInstance
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: final.Status.InstanceRef}, &inst); err != nil {
		t.Fatalf("get bound instance: %v", err)
	}
	if inst.Spec.Owner != "alice" {
		t.Errorf("instance owner = %q, want alice", inst.Spec.Owner)
	}
	if inst.Spec.Params["email"] != "alice@example.com" {
		t.Errorf("instance params = %v, want the claim's params", inst.Spec.Params)
	}
	if inst.Labels[LabelClaimUID] != string(final.UID) {
		t.Errorf("claim-uid label = %q, want %q", inst.Labels[LabelClaimUID], final.UID)
	}
	if len(inst.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences = %+v, want exactly the claim", inst.OwnerReferences)
	}
	ref := inst.OwnerReferences[0]
	if ref.Kind != "DployInstanceClaim" || ref.UID != final.UID || ref.Controller == nil || !*ref.Controller {
		t.Errorf("ownerReference = %+v, want controller ref to claim %s", ref, final.UID)
	}
}

// TestConcurrentClaimsOnLimitedPool is the core safety property: many claims
// racing for a pool that cannot serve them all must hand every instance to
// exactly one claim, with the losers parked rather than double-booked.
func TestConcurrentClaimsOnLimitedPool(t *testing.T) {
	requireEnvtest(t)
	const poolSize = 3
	const claimants = 12

	ns := newNamespace(t)
	tmpl := poolTemplate(t, ns, "race", poolSize)
	waitForPoolReady(t, ns, tmpl.Name, poolSize)

	// Fire every claim at once so they genuinely contend: the reconciler runs
	// several workers, so these really do race on the same candidates.
	var wg sync.WaitGroup
	for i := 0; i < claimants; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := &dployv1alpha1.DployInstanceClaim{}
			c.Name = fmt.Sprintf("racer-%02d", i)
			c.Namespace = ns
			c.Spec = dployv1alpha1.DployInstanceClaimSpec{
				TemplateRef: tmpl.Name,
				Owner:       fmt.Sprintf("user%02d", i),
				WaitForPool: true,
			}
			if err := k8sClient.Create(context.Background(), c); err != nil {
				t.Errorf("create claim %s: %v", c.Name, err)
			}
		}(i)
	}
	wg.Wait()

	// Every claim must settle, and exactly poolSize of them may hold an instance.
	eventually(t, "all claims to settle with the pool fully allotted", func() (bool, string) {
		bound, pending, other := claimTally(t, ns)
		return bound == poolSize && pending == claimants-poolSize && other == 0,
			fmt.Sprintf("bound=%d pending=%d other=%d", bound, pending, other)
	})

	// Hold the line: no straggler reconcile may over-allot afterwards.
	consistently(t, 2*time.Second, "pool stayed over-allotted", func() (bool, string) {
		bound, _, _ := claimTally(t, ns)
		return bound <= poolSize, fmt.Sprintf("bound=%d > poolSize=%d", bound, poolSize)
	})

	assertBindingsUnique(t, ns, poolSize)

	// Release one environment the way a cascade would, and a waiting claim must
	// pick up the replacement the pool controller creates.
	insts := listInstances(t, ns, client.MatchingLabels{LabelTemplate: tmpl.Name})
	var released string
	for i := range insts {
		if insts[i].Labels[LabelClaimUID] != "" {
			released = insts[i].Labels[LabelClaim]
			if err := k8sClient.Delete(context.Background(), &insts[i]); err != nil {
				t.Fatalf("delete instance: %v", err)
			}
			break
		}
	}
	if released == "" {
		t.Fatal("no bound instance to release")
	}
	if err := k8sClient.Delete(context.Background(), getClaim(t, ns, released)); err != nil {
		t.Fatalf("delete released claim: %v", err)
	}

	eventually(t, "a waiting claim to take over the freed slot", func() (bool, string) {
		bound, pending, other := claimTally(t, ns)
		return bound == poolSize && pending == claimants-poolSize-1 && other == 0,
			fmt.Sprintf("bound=%d pending=%d other=%d", bound, pending, other)
	})
	assertBindingsUnique(t, ns, poolSize)
}

// claimTally counts claims by phase in a namespace.
func claimTally(t *testing.T, ns string) (bound, pending, other int) {
	t.Helper()
	var list dployv1alpha1.DployInstanceClaimList
	if err := k8sClient.List(context.Background(), &list, client.InNamespace(ns)); err != nil {
		t.Fatalf("list claims: %v", err)
	}
	for i := range list.Items {
		if !list.Items[i].DeletionTimestamp.IsZero() {
			continue
		}
		switch list.Items[i].Status.Phase {
		case dployv1alpha1.ClaimBound:
			bound++
		case dployv1alpha1.ClaimPending:
			pending++
		default:
			other++
		}
	}
	return bound, pending, other
}

// assertBindingsUnique proves no instance is shared between two claims and every
// binding points at a claim that believes it holds that instance.
func assertBindingsUnique(t *testing.T, ns string, wantBound int) {
	t.Helper()
	insts := listInstances(t, ns)
	seen := map[string]string{}
	bound := 0
	for i := range insts {
		uid := insts[i].Labels[LabelClaimUID]
		if uid == "" {
			continue
		}
		bound++
		if prev, dup := seen[uid]; dup {
			t.Fatalf("claim %s is bound to two instances: %s and %s", uid, prev, insts[i].Name)
		}
		seen[uid] = insts[i].Name

		claim := getClaim(t, ns, insts[i].Labels[LabelClaim])
		if string(claim.UID) != uid {
			t.Errorf("instance %s carries claim-uid %s but claim %s has UID %s",
				insts[i].Name, uid, claim.Name, claim.UID)
		}
		if claim.Status.InstanceRef != insts[i].Name {
			t.Errorf("claim %s points at instance %q but is bound to %q",
				claim.Name, claim.Status.InstanceRef, insts[i].Name)
		}
	}
	if bound != wantBound {
		t.Errorf("bound instances = %d, want %d", bound, wantBound)
	}
}

// TestClaimWaitsForPool checks the waitForPool=true contract: an exhausted pool
// parks the claim rather than provisioning around it, and it binds once a slot
// frees up.
func TestClaimWaitsForPool(t *testing.T) {
	requireEnvtest(t)
	ns := newNamespace(t)
	tmpl := poolTemplate(t, ns, "scarce", 1)
	waitForPoolReady(t, ns, tmpl.Name, 1)

	first := makeClaim(t, ns, "first", tmpl.Name, "alice", func(c *dployv1alpha1.DployInstanceClaim) {
		c.Spec.WaitForPool = true
	})
	waitClaimPhase(t, ns, first.Name, dployv1alpha1.ClaimBound)

	second := makeClaim(t, ns, "second", tmpl.Name, "bob", func(c *dployv1alpha1.DployInstanceClaim) {
		c.Spec.WaitForPool = true
	})
	waitClaimPhase(t, ns, second.Name, dployv1alpha1.ClaimPending)

	// Pending is the point: it must not quietly provision an instance instead.
	consistently(t, 2*time.Second, "waiting claim provisioned an instance anyway", func() (bool, string) {
		c := getClaim(t, ns, second.Name)
		return c.Status.Phase == dployv1alpha1.ClaimPending && c.Status.InstanceRef == "",
			fmt.Sprintf("phase=%q instanceRef=%q", c.Status.Phase, c.Status.InstanceRef)
	})
	pending := getClaim(t, ns, second.Name)
	if c := apimeta.FindStatusCondition(pending.Status.Conditions, dployv1alpha1.ConditionBound); c == nil || c.Reason != "PoolExhausted" {
		t.Errorf("Bound condition = %+v, want reason PoolExhausted", c)
	}

	// Free the slot; the pool controller replaces the instance and the waiter takes it.
	held := listInstances(t, ns, client.MatchingLabels{LabelClaim: first.Name})
	if len(held) != 1 {
		t.Fatalf("expected 1 instance held by %q, got %d", first.Name, len(held))
	}
	if err := k8sClient.Delete(context.Background(), &held[0]); err != nil {
		t.Fatalf("release instance: %v", err)
	}
	if err := k8sClient.Delete(context.Background(), getClaim(t, ns, first.Name)); err != nil {
		t.Fatalf("delete first claim: %v", err)
	}
	waitClaimPhase(t, ns, second.Name, dployv1alpha1.ClaimBound)
}

// TestClaimFallsBackToOnDemand checks waitForPool=false: an empty pool means
// provisioning a dedicated instance rather than waiting.
func TestClaimFallsBackToOnDemand(t *testing.T) {
	requireEnvtest(t)
	ns := newNamespace(t)
	tmpl := poolTemplate(t, ns, "fallback", 1)
	waitForPoolReady(t, ns, tmpl.Name, 1)

	first := makeClaim(t, ns, "first", tmpl.Name, "alice", nil)
	waitClaimPhase(t, ns, first.Name, dployv1alpha1.ClaimBound)

	second := makeClaim(t, ns, "second", tmpl.Name, "bob", func(c *dployv1alpha1.DployInstanceClaim) {
		c.Spec.WaitForPool = false
	})
	bound := waitClaimPhase(t, ns, second.Name, dployv1alpha1.ClaimBound)

	var inst dployv1alpha1.DployInstance
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: bound.Status.InstanceRef}, &inst); err != nil {
		t.Fatalf("get on-demand instance: %v", err)
	}
	if inst.Spec.Pooled {
		t.Error("fallback instance is marked pooled; it should be a dedicated one")
	}
	if inst.Spec.Owner != "bob" {
		t.Errorf("on-demand instance owner = %q, want bob", inst.Spec.Owner)
	}
	if len(inst.OwnerReferences) != 1 || inst.OwnerReferences[0].UID != bound.UID {
		t.Errorf("ownerReferences = %+v, want the claim", inst.OwnerReferences)
	}
}

// TestClaimRejectedOverQuota checks the per-owner cap. Both claims are created at
// once, so the quota has to hold under the same race the binding does.
func TestClaimRejectedOverQuota(t *testing.T) {
	requireEnvtest(t)
	ns := newNamespace(t)
	limit := 1
	tmpl := makeTemplate(t, ns, "quota", func(tp *dployv1alpha1.DployTemplate) {
		tp.Spec.MaxInstancesPerUser = &limit
	})

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := &dployv1alpha1.DployInstanceClaim{}
			c.Name = fmt.Sprintf("greedy-%d", i)
			c.Namespace = ns
			c.Spec = dployv1alpha1.DployInstanceClaimSpec{TemplateRef: tmpl.Name, Owner: "alice"}
			if err := k8sClient.Create(context.Background(), c); err != nil {
				t.Errorf("create claim: %v", err)
			}
		}(i)
	}
	wg.Wait()

	eventually(t, "one claim bound and one rejected", func() (bool, string) {
		var list dployv1alpha1.DployInstanceClaimList
		if err := k8sClient.List(context.Background(), &list, client.InNamespace(ns)); err != nil {
			return false, err.Error()
		}
		bound, rejected := 0, 0
		for i := range list.Items {
			switch list.Items[i].Status.Phase {
			case dployv1alpha1.ClaimBound:
				bound++
			case dployv1alpha1.ClaimRejected:
				rejected++
			case dployv1alpha1.ClaimPending, dployv1alpha1.ClaimExpired:
				// Neither outcome yet — keep waiting.
			}
		}
		return bound == 1 && rejected == 1, fmt.Sprintf("bound=%d rejected=%d", bound, rejected)
	})

	// The cap is about environments, not bookkeeping: exactly one may exist.
	eventually(t, "exactly one instance to survive the quota race", func() (bool, string) {
		insts := listInstances(t, ns, client.MatchingLabels{LabelOwner: "alice"})
		return len(insts) == 1, fmt.Sprintf("%d instances", len(insts))
	})
}

// TestClaimRejectedForMissingTemplate covers the unsatisfiable-request path.
func TestClaimRejectedForMissingTemplate(t *testing.T) {
	requireEnvtest(t)
	ns := newNamespace(t)
	claim := makeClaim(t, ns, "ghost", "does-not-exist", "alice", nil)

	rejected := waitClaimPhase(t, ns, claim.Name, dployv1alpha1.ClaimRejected)
	c := apimeta.FindStatusCondition(rejected.Status.Conditions, dployv1alpha1.ConditionBound)
	if c == nil || c.Reason != "TemplateNotFound" {
		t.Errorf("Bound condition = %+v, want reason TemplateNotFound", c)
	}
	if got := listInstances(t, ns); len(got) != 0 {
		t.Errorf("rejected claim created %d instance(s)", len(got))
	}
}

// TestClaimTTLAnchoredAtBinding checks that the clock starts at the binding, not
// at creation, and that extending is nothing more than raising spec.ttlSeconds.
func TestClaimTTLAnchoredAtBinding(t *testing.T) {
	requireEnvtest(t)
	ns := newNamespace(t)
	tmpl := makeTemplate(t, ns, "ttl", func(tp *dployv1alpha1.DployTemplate) {
		tp.Spec.TTL = &dployv1alpha1.TTLSpec{Seconds: 3600}
	})

	claim := makeClaim(t, ns, "alice-ttl", tmpl.Name, "alice", nil)
	bound := waitClaimPhase(t, ns, claim.Name, dployv1alpha1.ClaimBound)

	if bound.Status.TTLSeconds != 3600 {
		t.Errorf("granted ttl = %d, want the template's 3600", bound.Status.TTLSeconds)
	}
	if bound.Status.BoundAt == nil || bound.Status.ExpiresAt == nil {
		t.Fatalf("boundAt=%v expiresAt=%v, want both set", bound.Status.BoundAt, bound.Status.ExpiresAt)
	}
	if got := bound.Status.ExpiresAt.Sub(bound.Status.BoundAt.Time); got != 3600*time.Second {
		t.Errorf("expiry is boundAt+%v, want boundAt+1h", got)
	}

	// Extend by patching the spec — the same thing a user does with kubectl.
	boundAt := bound.Status.BoundAt.Time
	patched := getClaim(t, ns, claim.Name)
	patch := client.MergeFrom(patched.DeepCopy())
	patched.Spec.TTLSeconds = 7200
	if err := k8sClient.Patch(context.Background(), patched, patch); err != nil {
		t.Fatalf("patch ttl: %v", err)
	}

	eventually(t, "the extension to move the expiry", func() (bool, string) {
		c := getClaim(t, ns, claim.Name)
		if c.Status.ExpiresAt == nil {
			return false, "no expiry"
		}
		return c.Status.ExpiresAt.Sub(boundAt) == 7200*time.Second, c.Status.ExpiresAt.String()
	})

	// The anchor must not move: extending grants more time from the binding, it
	// does not restart the clock.
	after := getClaim(t, ns, claim.Name)
	if !after.Status.BoundAt.Time.Equal(boundAt) {
		t.Errorf("boundAt moved from %v to %v on extension", boundAt, after.Status.BoundAt.Time)
	}

	// And the instance carries the same deadline, so its own TTL enforcement agrees.
	eventually(t, "the instance expiry to follow the claim", func() (bool, string) {
		var inst dployv1alpha1.DployInstance
		if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: after.Status.InstanceRef}, &inst); err != nil {
			return false, err.Error()
		}
		if inst.Spec.ExpiresAt == nil {
			return false, "instance has no expiry"
		}
		return inst.Spec.ExpiresAt.Time.Equal(after.Status.ExpiresAt.Time), inst.Spec.ExpiresAt.String()
	})
}

// TestClaimTTLAnchorSurvivesLostStatus simulates the window an operator restart
// opens: the instance is bound but the claim's status write never landed. The
// anchor must come back off the instance, not restart — otherwise every crash
// would quietly hand every running environment a full extra lifetime.
func TestClaimTTLAnchorSurvivesLostStatus(t *testing.T) {
	requireEnvtest(t)
	ns := newNamespace(t)
	tmpl := makeTemplate(t, ns, "anchor", func(tp *dployv1alpha1.DployTemplate) {
		tp.Spec.TTL = &dployv1alpha1.TTLSpec{Seconds: 3600}
	})

	claim := makeClaim(t, ns, "alice-anchor", tmpl.Name, "alice", nil)
	bound := waitClaimPhase(t, ns, claim.Name, dployv1alpha1.ClaimBound)
	originalExpiry := bound.Status.ExpiresAt
	if originalExpiry == nil {
		t.Fatal("bound claim has no expiry")
	}

	// The binding stamped the anchor on the instance in the same write that won it.
	var inst dployv1alpha1.DployInstance
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: bound.Status.InstanceRef}, &inst); err != nil {
		t.Fatalf("get bound instance: %v", err)
	}
	if inst.Annotations[AnnotationBoundAt] == "" {
		t.Fatalf("instance carries no %s annotation", AnnotationBoundAt)
	}

	// Wipe the claim's copy of the anchor, as a lost status write would.
	lost := getClaim(t, ns, claim.Name)
	patch := client.MergeFrom(lost.DeepCopy())
	lost.Status.BoundAt = nil
	lost.Status.ExpiresAt = nil
	if err := k8sClient.Status().Patch(context.Background(), lost, patch); err != nil {
		t.Fatalf("clear status anchor: %v", err)
	}

	// Nudge the controller and check the recovered expiry matches the original.
	touch := getClaim(t, ns, claim.Name)
	tp := client.MergeFrom(touch.DeepCopy())
	touch.Spec.TTLSeconds = 3600
	if err := k8sClient.Patch(context.Background(), touch, tp); err != nil {
		t.Fatalf("touch claim: %v", err)
	}

	eventually(t, "the anchor to be recovered from the instance", func() (bool, string) {
		c := getClaim(t, ns, claim.Name)
		if c.Status.ExpiresAt == nil {
			return false, "no expiry yet"
		}
		return c.Status.ExpiresAt.Time.Equal(originalExpiry.Time),
			fmt.Sprintf("recovered %s, want %s", c.Status.ExpiresAt, originalExpiry)
	})
}

// TestClaimExpires checks the end of the lifecycle: the environment is torn down
// and the claim survives as a tombstone that no longer costs its owner a slot.
func TestClaimExpires(t *testing.T) {
	requireEnvtest(t)
	ns := newNamespace(t)
	tmpl := makeTemplate(t, ns, "shortlived", nil)

	claim := makeClaim(t, ns, "alice-shortlived", tmpl.Name, "alice", func(c *dployv1alpha1.DployInstanceClaim) {
		c.Spec.TTLSeconds = 2
	})
	bound := waitClaimPhase(t, ns, claim.Name, dployv1alpha1.ClaimBound)
	instName := bound.Status.InstanceRef

	expired := waitClaimPhase(t, ns, claim.Name, dployv1alpha1.ClaimExpired)
	if expired.Status.ConnectionURL != "" {
		t.Errorf("expired claim still advertises %q", expired.Status.ConnectionURL)
	}

	eventually(t, "the expired instance to be torn down", func() (bool, string) {
		var inst dployv1alpha1.DployInstance
		err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: instName}, &inst)
		return apierrors.IsNotFound(err), fmt.Sprintf("get err=%v", err)
	})

	// A tombstone must not resurrect itself on the next reconcile.
	consistently(t, 2*time.Second, "expired claim came back to life", func() (bool, string) {
		c := getClaim(t, ns, claim.Name)
		return c.Status.Phase == dployv1alpha1.ClaimExpired && c.Status.InstanceRef == "",
			fmt.Sprintf("phase=%q instanceRef=%q", c.Status.Phase, c.Status.InstanceRef)
	})

	if getClaim(t, ns, claim.Name).IsActive() {
		t.Error("expired claim still counts as active against the owner quota")
	}
}

// --- unit tests (no control plane needed) ---

func TestResolveClaimTTL(t *testing.T) {
	eff := operatorconfig.Effective{TTLSeconds: 3600, ExtendSeconds: 1800, MaxExtends: 0}

	tmplWith := func(ttl *dployv1alpha1.TTLSpec) *dployv1alpha1.DployTemplate {
		tp := &dployv1alpha1.DployTemplate{}
		tp.Spec.TTL = ttl
		return tp
	}
	claimWith := func(ttl int64) *dployv1alpha1.DployInstanceClaim {
		c := &dployv1alpha1.DployInstanceClaim{}
		c.Spec.TTLSeconds = ttl
		return c
	}

	cases := []struct {
		name           string
		claim          *dployv1alpha1.DployInstanceClaim
		tmpl           *dployv1alpha1.DployTemplate
		wantTTL, wantX int64
	}{
		{"unset falls back to the cluster default", claimWith(0), tmplWith(nil), 3600, -1},
		{"template TTL wins over the cluster default", claimWith(0), tmplWith(&dployv1alpha1.TTLSpec{Seconds: 600}), 600, -1},
		{"an explicit request is honored when uncapped", claimWith(120), tmplWith(nil), 120, -1},
		{
			"an extend budget caps the request",
			claimWith(99999),
			tmplWith(&dployv1alpha1.TTLSpec{Seconds: 600, ExtendSeconds: 300, MaxExtends: 2}),
			1200, 1200,
		},
		{
			"unlimited collapses to the ceiling when the template has one",
			claimWith(-1),
			tmplWith(&dployv1alpha1.TTLSpec{Seconds: 600, ExtendSeconds: 300, MaxExtends: 2}),
			1200, 1200,
		},
		{"unlimited is honored when the template is unlimited", claimWith(-1), tmplWith(&dployv1alpha1.TTLSpec{Seconds: -1}), -1, -1},
		{"maxExtends -1 means no ceiling", claimWith(99999), tmplWith(&dployv1alpha1.TTLSpec{Seconds: 600, MaxExtends: -1}), 99999, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ttl, maxTTL := resolveClaimTTL(tc.claim, tc.tmpl, eff)
			if ttl != tc.wantTTL || maxTTL != tc.wantX {
				t.Errorf("resolveClaimTTL() = (%d, %d), want (%d, %d)", ttl, maxTTL, tc.wantTTL, tc.wantX)
			}
		})
	}
}

func TestExpiryFromAndIsExpired(t *testing.T) {
	base := metav1.NewTime(time.Unix(1_700_000_000, 0))
	if got := expiryFrom(base, -1); got != nil {
		t.Errorf("unlimited TTL should have no expiry, got %v", got)
	}
	got := expiryFrom(base, 60)
	if got == nil || !got.Time.Equal(base.Add(60*time.Second)) {
		t.Fatalf("expiryFrom(_, 60) = %v, want boundAt+60s", got)
	}

	claim := &dployv1alpha1.DployInstanceClaim{}
	if isExpired(claim, base.Time) {
		t.Error("a claim with no expiry is never expired")
	}
	claim.Status.ExpiresAt = got
	if isExpired(claim, base.Time) {
		t.Error("not expired one minute early")
	}
	if !isExpired(claim, got.Time) {
		t.Error("expired exactly at the deadline")
	}
}

func TestStampAndRecoverBoundAt(t *testing.T) {
	at := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())

	inst := &dployv1alpha1.DployInstance{}
	stampBoundAt(inst, at)
	if got := inst.Annotations[AnnotationBoundAt]; got != "2023-11-14T22:13:20Z" {
		t.Errorf("stamped %q", got)
	}
	// The first stamp wins: re-binding logic must never push the anchor forward.
	stampBoundAt(inst, metav1.NewTime(at.Add(time.Hour)))
	if got := inst.Annotations[AnnotationBoundAt]; got != "2023-11-14T22:13:20Z" {
		t.Errorf("anchor moved to %q", got)
	}
	if got := boundAtOf(inst); !got.Time.Equal(at.Time) {
		t.Errorf("boundAtOf = %v, want %v", got, at)
	}

	// An on-demand instance is bound the moment it is created, so its creation
	// time is a safe fallback when the annotation is missing.
	created := &dployv1alpha1.DployInstance{}
	created.CreationTimestamp = at
	if got := boundAtOf(created); !got.Time.Equal(at.Time) {
		t.Errorf("on-demand fallback = %v, want the creation time %v", got, at)
	}

	// A pool member was created long before it was claimed, so falling back to its
	// creation time would expire it instantly. Fall back to now instead.
	pooled := &dployv1alpha1.DployInstance{}
	pooled.CreationTimestamp = at
	pooled.Spec.Pooled = true
	if got := boundAtOf(pooled); got.Time.Before(time.Now().Add(-time.Minute)) {
		t.Errorf("pool fallback = %v, want roughly now", got)
	}
}

func TestIsClaimable(t *testing.T) {
	warm := func(mutate func(*dployv1alpha1.DployInstance)) *dployv1alpha1.DployInstance {
		inst := &dployv1alpha1.DployInstance{}
		inst.Labels = map[string]string{}
		inst.Spec.Pooled = true
		inst.Status.Phase = dployv1alpha1.PhaseAvailable
		if mutate != nil {
			mutate(inst)
		}
		return inst
	}
	if !isClaimable(warm(nil)) {
		t.Error("a warm, unowned, Available instance is claimable")
	}
	if isClaimable(warm(func(i *dployv1alpha1.DployInstance) { i.Spec.Owner = "alice" })) {
		t.Error("an owned instance is not claimable")
	}
	if isClaimable(warm(func(i *dployv1alpha1.DployInstance) { i.Labels[LabelClaimUID] = "abc" })) {
		t.Error("an instance already bound to a claim is not claimable")
	}
	if isClaimable(warm(func(i *dployv1alpha1.DployInstance) { i.Status.Phase = dployv1alpha1.PhaseProvisioning })) {
		t.Error("an instance that is not Available yet is not claimable")
	}
	if isClaimable(warm(func(i *dployv1alpha1.DployInstance) {
		now := metav1.Now()
		i.DeletionTimestamp = &now
	})) {
		t.Error("an instance being deleted is not claimable")
	}
}
