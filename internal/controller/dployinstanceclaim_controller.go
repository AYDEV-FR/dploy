// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
	"github.com/AYDEV-FR/dploy/internal/operatorconfig"
)

const (
	// claimPoolWaitRequeue is the safety-net retry for a claim parked on an empty
	// pool. Freed instances normally wake the claim through the instance watch;
	// this covers the case where the event was missed.
	claimPoolWaitRequeue = 10 * time.Second

	// claimBindRetries bounds how many warm candidates a single reconcile fights
	// over before giving up and retrying later. Losing every attempt means the
	// pool is heavily contended, and backing off beats spinning.
	claimBindRetries = 8

	// quotaSettleWindow is how long after a binding the per-owner quota is still
	// re-ranked. It has to outlast a burst of concurrent claims becoming visible
	// to each other; past it, a running environment is never taken away.
	quotaSettleWindow = 60 * time.Second

	// quotaSettleRequeue paces the re-ranking inside that window.
	quotaSettleRequeue = 3 * time.Second

	// claimMaxConcurrentReconciles lets independent claims bind in parallel. The
	// binding itself is safe under concurrency (optimistic locking on the
	// instance), and serializing it would make a burst of claims crawl.
	claimMaxConcurrentReconciles = 4
)

// DployInstanceClaimReconciler turns a DployInstanceClaim into a running
// environment: it binds a warm pool member (or provisions one on demand), takes
// ownership of the instance so deleting the claim tears it down, anchors the TTL
// at the binding, enforces the per-owner quota, and projects the instance status
// back onto the claim so callers only watch one object.
type DployInstanceClaimReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// APIReader bypasses the informer cache. Two decisions must not be made on
	// stale data — "this claim holds nothing yet" and "this owner is under quota"
	// — because both hand out an environment, and a cache that has not caught up
	// with a binding written moments ago would hand out a second one.
	APIReader client.Reader
}

// reader returns the uncached reader, falling back to the cached client when the
// reconciler was built without one.
func (r *DployInstanceClaimReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// +kubebuilder:rbac:groups=dploy.dev,resources=dployinstanceclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dploy.dev,resources=dployinstanceclaims/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dploy.dev,resources=dployinstanceclaims/finalizers,verbs=update

// Reconcile drives a DployInstanceClaim toward a bound environment.
func (r *DployInstanceClaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var claim dployv1alpha1.DployInstanceClaim
	if err := r.Get(ctx, req.NamespacedName, &claim); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Deletion needs no finalizer: the claim is the instance's owner, so the
	// garbage collector cascades the teardown, and the instance's own finalizer
	// then unwinds the HelmRelease and workload namespace.
	if !claim.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if r.ensureMeta(&claim) {
		if err := r.Update(ctx, &claim); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: continueRequeue}, nil
	}

	original := claim.DeepCopy()

	// Rejected and Expired are terminal. A Rejected claim is re-evaluated when its
	// spec changes (the request itself was the problem); an Expired one never is,
	// because its environment is already gone and reviving it under the old
	// identity would surprise everyone.
	switch claim.Status.Phase {
	case dployv1alpha1.ClaimExpired:
		return ctrl.Result{}, r.observe(ctx, original, &claim)
	case dployv1alpha1.ClaimRejected:
		if claim.Status.ObservedGeneration == claim.Generation {
			return ctrl.Result{}, nil
		}
	case dployv1alpha1.ClaimPending, dployv1alpha1.ClaimBound:
		// Still in flight — fall through and reconcile.
	}

	var tmpl dployv1alpha1.DployTemplate
	if err := r.Get(ctx, types.NamespacedName{Namespace: claim.Namespace, Name: claim.Spec.TemplateRef}, &tmpl); err != nil {
		if apierrors.IsNotFound(err) {
			return r.reject(ctx, original, &claim, "TemplateNotFound",
				fmt.Sprintf("DployTemplate %q not found in namespace %q", claim.Spec.TemplateRef, claim.Namespace))
		}
		return ctrl.Result{}, err
	}
	if !tmpl.Spec.Enabled {
		return r.reject(ctx, original, &claim, "TemplateDisabled",
			fmt.Sprintf("DployTemplate %q is disabled", tmpl.Name))
	}

	eff, err := operatorconfig.Resolve(ctx, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	ttl, maxTTL := resolveClaimTTL(&claim, &tmpl, eff)
	claim.Status.TTLSeconds = ttl
	claim.Status.MaxTTLSeconds = maxTTL

	inst, err := r.boundInstance(ctx, &claim)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Expiry is checked before any (re)binding: the instance controller enforces
	// the same deadline and may already have reaped the instance, and a claim that
	// noticed its instance missing must not quietly help itself to a fresh one.
	if isExpired(&claim, time.Now()) {
		return r.expire(ctx, original, &claim, inst)
	}

	if inst == nil {
		if over, limit, qerr := r.overQuota(ctx, &claim, &tmpl, eff); qerr != nil {
			return ctrl.Result{}, qerr
		} else if over {
			return r.reject(ctx, original, &claim, "QuotaExceeded",
				fmt.Sprintf("owner %q already holds the maximum of %d environment(s)", claim.Spec.Owner, limit))
		}

		boundAt := metav1.NewTime(time.Now().Truncate(time.Second))
		inst, err = r.bind(ctx, &claim, &tmpl, boundAt, ttl, expiryFrom(boundAt, ttl))
		if errors.Is(err, errTemplateAtCapacity) {
			return r.reject(ctx, original, &claim, "TemplateAtCapacity",
				fmt.Sprintf("template %q is at its maximum of %d environment(s)", tmpl.Name, maxTemplateSize(&tmpl)))
		}
		if err != nil {
			return ctrl.Result{}, err
		}
		if inst == nil {
			return r.pending(ctx, original, &claim, "PoolExhausted",
				fmt.Sprintf("waiting for a warm instance of template %q", tmpl.Name))
		}
		claim.Status.BoundAt = &boundAt
	}

	// The TTL clock starts at the binding, so extending is a patch that raises
	// spec.ttlSeconds — no separate "extend" verb, and no drift from re-reconciles.
	//
	// The anchor is read back off the instance when status is missing it: a status
	// write lost to a restart or a conflicting patch must not silently restart the
	// clock, which would hand out a free extension every time it happened.
	if claim.Status.BoundAt == nil {
		claim.Status.BoundAt = boundAtOf(inst)
	}
	claim.Status.ExpiresAt = expiryFrom(*claim.Status.BoundAt, ttl)
	if isExpired(&claim, time.Now()) {
		return r.expire(ctx, original, &claim, inst)
	}

	// Settle quota races after the fact. Concurrent claims for one owner each pass
	// the pre-check before any of them binds, and each then sees a different
	// partial set of instances — so a single post-binding check ranks against an
	// incomplete list and lets the quota drift. Re-ranking on every reconcile
	// while the binding is fresh converges instead: the rule (keep the oldest
	// `limit` instances) is stable, so once every create is visible all claims
	// agree on who loses.
	//
	// The window matters. Past it an environment you hold is yours, so lowering a
	// quota never reaches back and kills something already running.
	// A bound claim whose environment died is not served, and nothing else was
	// watching for it: the claim kept pointing at the dead instance and kept
	// counting against its owner's quota until the TTL ran out. Rejecting is the
	// existing machinery for "this claim cannot be honored" — it releases the
	// instance, which frees the quota, and the owner re-requests when ready.
	if inst != nil && inst.Status.Phase == dployv1alpha1.PhaseFailed {
		claim.Status.BoundAt = nil
		return r.reject(ctx, original, &claim, "InstanceFailed",
			fmt.Sprintf("DployInstance %q failed; the environment was released", inst.Name))
	}

	settling := time.Since(claim.Status.BoundAt.Time) < quotaSettleWindow
	if settling {
		if over, limit, qerr := r.evictIfOverQuota(ctx, &claim, &tmpl, eff, inst); qerr != nil {
			return ctrl.Result{}, qerr
		} else if over {
			claim.Status.BoundAt = nil
			return r.reject(ctx, original, &claim, "QuotaExceeded",
				fmt.Sprintf("owner %q already holds the maximum of %d environment(s)", claim.Spec.Owner, limit))
		}
		// The same race, one level up: concurrent claims each pass the maxSize
		// pre-check before any of their creates are visible. Re-rank on the same
		// stable rule so the overshoot is given back instead of kept.
		if over, limit, cerr := r.evictIfOverMaxSize(ctx, &tmpl, inst); cerr != nil {
			return ctrl.Result{}, cerr
		} else if over {
			claim.Status.BoundAt = nil
			return r.reject(ctx, original, &claim, "TemplateAtCapacity",
				fmt.Sprintf("template %q is at its maximum of %d environment(s)", tmpl.Name, limit))
		}
	}

	if err := r.syncInstance(ctx, &claim, inst, ttl, claim.Status.ExpiresAt); err != nil {
		return ctrl.Result{}, err
	}

	r.project(&claim, inst)
	if err := r.patchStatus(ctx, original, &claim); err != nil {
		return ctrl.Result{}, err
	}

	res := ctrl.Result{}
	if claim.Status.ExpiresAt != nil {
		res.RequeueAfter = time.Until(claim.Status.ExpiresAt.Time)
	}
	// While the binding is fresh, come back soon enough to re-rank against a
	// settled view. Instance status updates usually wake us anyway; this makes the
	// convergence guaranteed rather than incidental.
	if settling && (res.RequeueAfter == 0 || res.RequeueAfter > quotaSettleRequeue) {
		res.RequeueAfter = quotaSettleRequeue
	}
	return res, nil
}

// ensureMeta stamps the labels that make claims listable by owner and template,
// returning true if the object changed and must be persisted.
func (r *DployInstanceClaimReconciler) ensureMeta(claim *dployv1alpha1.DployInstanceClaim) bool {
	if claim.Labels == nil {
		claim.Labels = map[string]string{}
	}
	want := map[string]string{
		LabelManaged:  "true",
		LabelTemplate: claim.Spec.TemplateRef,
	}
	if o := sanitize(claim.Spec.Owner); o != "" {
		want[LabelOwner] = o
	}
	changed := false
	for k, v := range want {
		if claim.Labels[k] != v {
			claim.Labels[k] = v
			changed = true
		}
	}
	return changed
}

// boundInstance finds the instance already bound to this claim, keyed by the
// claim UID label. Looking it up by label rather than trusting status.instanceRef
// makes the binding crash-safe: an operator that dies between winning an instance
// and writing the status picks the same instance back up on restart.
func (r *DployInstanceClaimReconciler) boundInstance(ctx context.Context, claim *dployv1alpha1.DployInstanceClaim) (*dployv1alpha1.DployInstance, error) {
	inst, err := lookupBound(ctx, r.Client, claim)
	if err != nil || inst != nil {
		return inst, err
	}
	// The cache says this claim holds nothing. Confirm against the API server
	// before acting on it: concluding "unbound" from a lagging informer would bind
	// a second instance to a claim that already has one.
	return lookupBound(ctx, r.reader(), claim)
}

func lookupBound(ctx context.Context, rd client.Reader, claim *dployv1alpha1.DployInstanceClaim) (*dployv1alpha1.DployInstance, error) {
	var list dployv1alpha1.DployInstanceList
	if err := rd.List(ctx, &list,
		client.InNamespace(claim.Namespace),
		client.MatchingLabels{LabelClaimUID: string(claim.UID)},
	); err != nil {
		return nil, fmt.Errorf("list bound instances: %w", err)
	}
	for i := range list.Items {
		if list.Items[i].DeletionTimestamp.IsZero() {
			return &list.Items[i], nil
		}
	}
	return nil, nil
}

// bind hands the claim an instance: a warm pool member when the template is
// pooled, otherwise (or when the pool is empty and the caller declined to wait)
// a freshly provisioned one. A nil instance with a nil error means "pool empty,
// caller asked to wait".
func (r *DployInstanceClaimReconciler) bind(
	ctx context.Context,
	claim *dployv1alpha1.DployInstanceClaim,
	tmpl *dployv1alpha1.DployTemplate,
	boundAt metav1.Time,
	ttl int64,
	expiresAt *metav1.Time,
) (*dployv1alpha1.DployInstance, error) {
	if tmpl.Spec.Method == dployv1alpha1.MethodPool {
		inst, err := r.acquirePooled(ctx, claim, tmpl, boundAt)
		if err != nil || inst != nil {
			return inst, err
		}
		if claim.Spec.WaitForPool {
			return nil, nil
		}
		// Falling through here means provisioning a new environment, and this is
		// the only path on which maxSize was never consulted: the template
		// controller checks it in the fill loop, so the cap held for warm members
		// and did nothing at all once demand outran the pool. Taking a warm member
		// is deliberately not gated — it does not change the total.
		if over, err := r.overMaxSize(ctx, tmpl); err != nil {
			return nil, err
		} else if over {
			return nil, errTemplateAtCapacity
		}
	}
	return r.createOnDemand(ctx, claim, tmpl, boundAt, ttl, expiresAt)
}

// acquirePooled atomically wins one warm instance for the claim.
//
// The only contended write is the claim-UID label, and it goes through a full
// object update, so the API server's optimistic concurrency decides the winner:
// a claimer holding a stale copy of the instance is rejected with a conflict and
// moves on to the next candidate. Everything else about the binding (ownership,
// owner key, params, TTL) is applied afterwards, off the contention path.
func (r *DployInstanceClaimReconciler) acquirePooled(
	ctx context.Context,
	claim *dployv1alpha1.DployInstanceClaim,
	tmpl *dployv1alpha1.DployTemplate,
	boundAt metav1.Time,
) (*dployv1alpha1.DployInstance, error) {
	var list dployv1alpha1.DployInstanceList
	if err := r.List(ctx, &list,
		client.InNamespace(claim.Namespace),
		client.MatchingLabels{LabelTemplate: tmpl.Name, LabelPooled: "true"},
	); err != nil {
		return nil, fmt.Errorf("list pool instances: %w", err)
	}

	candidates := make([]*dployv1alpha1.DployInstance, 0, len(list.Items))
	for i := range list.Items {
		if isClaimable(&list.Items[i]) {
			candidates = append(candidates, &list.Items[i])
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	// Spread concurrent claimers over the candidate list instead of having them
	// all start at index 0. Conflicts still decide correctness; this only keeps
	// them rare.
	start := hashIndex(claim.UID, len(candidates))
	attempts := min(len(candidates), claimBindRetries)
	for i := 0; i < attempts; i++ {
		cand := candidates[(start+i)%len(candidates)].DeepCopy()
		if cand.Labels == nil {
			cand.Labels = map[string]string{}
		}
		cand.Labels[LabelClaimUID] = string(claim.UID)
		cand.Labels[LabelClaim] = claim.Name
		stampBoundAt(cand, boundAt)
		if err := r.Update(ctx, cand); err != nil {
			// Conflict means someone else wrote this instance first (very likely
			// the winning claimer); NotFound means it was reaped. Either way the
			// candidate is gone — try the next one.
			if apierrors.IsConflict(err) || apierrors.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("bind pool instance %q: %w", cand.Name, err)
		}
		return cand, nil
	}
	return nil, nil
}

// isClaimable reports whether a warm pool member is free to be bound.
func isClaimable(inst *dployv1alpha1.DployInstance) bool {
	return inst.DeletionTimestamp.IsZero() &&
		inst.Spec.Owner == "" &&
		inst.Labels[LabelClaimUID] == "" &&
		inst.Status.Phase == dployv1alpha1.PhaseAvailable
}

// createOnDemand provisions a fresh instance owned by the claim.
func (r *DployInstanceClaimReconciler) createOnDemand(
	ctx context.Context,
	claim *dployv1alpha1.DployInstanceClaim,
	tmpl *dployv1alpha1.DployTemplate,
	boundAt metav1.Time,
	ttl int64,
	expiresAt *metav1.Time,
) (*dployv1alpha1.DployInstance, error) {
	inst := &dployv1alpha1.DployInstance{}
	inst.Namespace = claim.Namespace
	// GenerateName rather than a deterministic name: a name collision with a
	// previous claim of the same owner/template would be unrecoverable, whereas a
	// generated name is always adoptable through the claim-UID label.
	inst.GenerateName = truncate(fmt.Sprintf("%s-%s", sanitize(claim.Spec.Owner), sanitize(tmpl.Name)), 200) + "-"
	inst.Labels = map[string]string{
		LabelManaged:  "true",
		LabelTemplate: tmpl.Name,
		LabelOwner:    sanitize(claim.Spec.Owner),
		LabelClaim:    claim.Name,
		LabelClaimUID: string(claim.UID),
	}
	stampBoundAt(inst, boundAt)
	inst.Spec = dployv1alpha1.DployInstanceSpec{
		TemplateRef: tmpl.Name,
		Owner:       claim.Spec.Owner,
		Params:      claim.Spec.Params,
		TTLSeconds:  ttl,
		ExpiresAt:   expiresAt,
	}
	if err := controllerutil.SetControllerReference(claim, inst, r.Scheme); err != nil {
		return nil, fmt.Errorf("set claim as owner: %w", err)
	}
	if err := r.Create(ctx, inst); err != nil {
		return nil, fmt.Errorf("create on-demand instance: %w", err)
	}
	return inst, nil
}

// syncInstance applies everything the binding implies onto the instance:
// ownership (so deleting the claim cascades), the owner key, the request params
// and the TTL. It is idempotent and retries on conflict, since the instance
// controller writes the same object.
func (r *DployInstanceClaimReconciler) syncInstance(
	ctx context.Context,
	claim *dployv1alpha1.DployInstanceClaim,
	inst *dployv1alpha1.DployInstance,
	ttl int64,
	expiresAt *metav1.Time,
) error {
	key := client.ObjectKeyFromObject(inst)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var cur dployv1alpha1.DployInstance
		if err := r.Get(ctx, key, &cur); err != nil {
			return err
		}
		desired := cur.DeepCopy()

		// The claim becomes the sole owner. For a warm pool member this replaces
		// the DployTemplate's controller reference — an instance owned by both
		// would outlive the claim, because the collector only reaps an object once
		// every owner is gone.
		desired.OwnerReferences = nil
		if err := controllerutil.SetControllerReference(claim, desired, r.Scheme); err != nil {
			return err
		}
		if desired.Labels == nil {
			desired.Labels = map[string]string{}
		}
		desired.Labels[LabelClaim] = claim.Name
		desired.Labels[LabelClaimUID] = string(claim.UID)
		desired.Labels[LabelOwner] = sanitize(claim.Spec.Owner)
		if claim.Status.BoundAt != nil {
			stampBoundAt(desired, *claim.Status.BoundAt)
		}

		desired.Spec.Owner = claim.Spec.Owner
		desired.Spec.Params = claim.Spec.Params
		desired.Spec.TTLSeconds = ttl
		desired.Spec.ExpiresAt = expiresAt

		if equality.Semantic.DeepEqual(&cur, desired) {
			return nil
		}
		if err := r.Update(ctx, desired); err != nil {
			return err
		}
		*inst = *desired
		return nil
	})
}

// project copies the bound instance's observed state onto the claim, so a caller
// watching the claim never has to read the instance.
func (r *DployInstanceClaimReconciler) project(claim *dployv1alpha1.DployInstanceClaim, inst *dployv1alpha1.DployInstance) {
	claim.Status.Phase = dployv1alpha1.ClaimBound
	claim.Status.InstanceRef = inst.Name
	claim.Status.UUID = inst.Status.UUID
	claim.Status.ConnectionURL = inst.Status.URL
	claim.Status.ConnectionType = inst.Status.ConnectionType
	claim.Status.ConnectionMessage = inst.Status.ConnectionMessage
	claim.Status.InstancePhase = inst.Status.Phase
	claim.Status.Health = inst.Status.Health

	apimeta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
		Type:               dployv1alpha1.ConditionBound,
		Status:             metav1.ConditionTrue,
		Reason:             "Bound",
		Message:            fmt.Sprintf("bound to DployInstance %q", inst.Name),
		ObservedGeneration: claim.Generation,
	})

	ready := metav1.Condition{
		Type:               dployv1alpha1.ConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             "Provisioning",
		Message:            "instance is converging",
		ObservedGeneration: claim.Generation,
	}
	if c := apimeta.FindStatusCondition(inst.Status.Conditions, "Ready"); c != nil {
		ready.Status = c.Status
		ready.Reason = c.Reason
		ready.Message = c.Message
	}
	apimeta.SetStatusCondition(&claim.Status.Conditions, ready)
}

// pending parks the claim without an instance, recording why.
func (r *DployInstanceClaimReconciler) pending(ctx context.Context, original, claim *dployv1alpha1.DployInstanceClaim, reason, message string) (ctrl.Result, error) {
	claim.Status.Phase = dployv1alpha1.ClaimPending
	claim.Status.InstanceRef = ""
	apimeta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
		Type:               dployv1alpha1.ConditionBound,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: claim.Generation,
	})
	if err := r.patchStatus(ctx, original, claim); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: claimPoolWaitRequeue}, nil
}

// reject marks the claim unsatisfiable as written. It stays that way until the
// spec changes.
//
// A claim can already hold an instance when it is rejected — its template was
// deleted or disabled out from under a running environment, or a quota race
// settled against it. Releasing it here is what makes `status.instanceRef = ""`
// below true rather than merely tidy: the instance is owner-referenced to the
// claim, so nothing else would ever collect it, and it would sit Failed with its
// workload namespace and pods running until someone deleted it by hand. The
// quota path already deletes before rejecting; this makes every reason behave
// the same, and the double delete is a harmless NotFound.
func (r *DployInstanceClaimReconciler) reject(ctx context.Context, original, claim *dployv1alpha1.DployInstanceClaim, reason, message string) (ctrl.Result, error) {
	if err := r.releaseHeld(ctx, claim); err != nil {
		return ctrl.Result{}, err
	}
	claim.Status.Phase = dployv1alpha1.ClaimRejected
	claim.Status.InstanceRef = ""
	claim.Status.ExpiresAt = nil
	apimeta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
		Type:               dployv1alpha1.ConditionBound,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: claim.Generation,
	})
	return ctrl.Result{}, r.patchStatus(ctx, original, claim)
}

// releaseHeld deletes the instance a claim is holding, if any. It looks the
// instance up by the claim-UID label rather than status.instanceRef so a claim
// rejected before its status caught up still releases what it actually won.
func (r *DployInstanceClaimReconciler) releaseHeld(ctx context.Context, claim *dployv1alpha1.DployInstanceClaim) error {
	inst, err := r.boundInstance(ctx, claim)
	if err != nil || inst == nil {
		return err
	}
	if err := r.Delete(ctx, inst); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("release instance %q of rejected claim: %w", inst.Name, err)
	}
	return nil
}

// expire tears the environment down and leaves the claim behind as a tombstone,
// so the owner can see why their environment went away — and so it stops
// counting against their quota.
func (r *DployInstanceClaimReconciler) expire(ctx context.Context, original, claim *dployv1alpha1.DployInstanceClaim, inst *dployv1alpha1.DployInstance) (ctrl.Result, error) {
	if inst != nil {
		if err := r.Delete(ctx, inst); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("delete expired instance: %w", err)
		}
	}
	claim.Status.Phase = dployv1alpha1.ClaimExpired
	claim.Status.InstanceRef = ""
	claim.Status.ConnectionURL = ""
	claim.Status.ConnectionMessage = ""
	claim.Status.InstancePhase = ""
	claim.Status.Health = ""
	apimeta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
		Type:               dployv1alpha1.ConditionBound,
		Status:             metav1.ConditionFalse,
		Reason:             "Expired",
		Message:            "the environment reached its TTL and was torn down",
		ObservedGeneration: claim.Generation,
	})
	apimeta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
		Type:               dployv1alpha1.ConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             "Expired",
		Message:            "the environment reached its TTL and was torn down",
		ObservedGeneration: claim.Generation,
	})
	return ctrl.Result{}, r.patchStatus(ctx, original, claim)
}

// observe records that the current generation has been seen without changing the
// claim's outcome. It is what keeps a terminal claim from reconciling forever.
func (r *DployInstanceClaimReconciler) observe(ctx context.Context, original, claim *dployv1alpha1.DployInstanceClaim) error {
	if claim.Status.ObservedGeneration == claim.Generation {
		return nil
	}
	return r.patchStatus(ctx, original, claim)
}

// patchStatus writes the observed state. A claim deleted mid-reconcile is an
// ordinary outcome — the user released their environment — so NotFound is not an
// error worth retrying or logging.
func (r *DployInstanceClaimReconciler) patchStatus(ctx context.Context, original, claim *dployv1alpha1.DployInstanceClaim) error {
	claim.Status.ObservedGeneration = claim.Generation
	if err := r.Status().Patch(ctx, claim, client.MergeFrom(original)); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("patch DployInstanceClaim status: %w", err)
	}
	return nil
}

// --- quota ---

// ownerLimit resolves the per-owner environment cap: the template's override, or
// the cluster default. A non-positive limit means unlimited.
func ownerLimit(tmpl *dployv1alpha1.DployTemplate, eff operatorconfig.Effective) int {
	if tmpl.Spec.MaxInstancesPerUser != nil && *tmpl.Spec.MaxInstancesPerUser > 0 {
		return *tmpl.Spec.MaxInstancesPerUser
	}
	return eff.MaxInstancesPerUser
}

// ownedInstances lists the live instances held by a claim's owner, sorted oldest
// first. Counting instances rather than claims keeps the quota honest: it
// measures environments actually held, so a pile-up of Pending claims can't
// inflate it.
func (r *DployInstanceClaimReconciler) ownedInstances(ctx context.Context, claim *dployv1alpha1.DployInstanceClaim) ([]dployv1alpha1.DployInstance, error) {
	owner := sanitize(claim.Spec.Owner)
	if owner == "" {
		return nil, nil
	}
	var list dployv1alpha1.DployInstanceList
	if err := r.reader().List(ctx, &list,
		client.InNamespace(claim.Namespace),
		client.MatchingLabels{LabelOwner: owner},
	); err != nil {
		return nil, fmt.Errorf("list owner instances: %w", err)
	}
	out := make([]dployv1alpha1.DployInstance, 0, len(list.Items))
	for i := range list.Items {
		if list.Items[i].DeletionTimestamp.IsZero() {
			out = append(out, list.Items[i])
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreationTimestamp.Equal(&out[j].CreationTimestamp) {
			return out[i].CreationTimestamp.Before(&out[j].CreationTimestamp)
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// overQuota reports whether binding one more environment would put the owner over
// their cap. It runs before the binding, so the common case never provisions
// anything it has to take back.
func (r *DployInstanceClaimReconciler) overQuota(
	ctx context.Context,
	claim *dployv1alpha1.DployInstanceClaim,
	tmpl *dployv1alpha1.DployTemplate,
	eff operatorconfig.Effective,
) (over bool, limit int, err error) {
	limit = ownerLimit(tmpl, eff)
	if limit <= 0 {
		return false, 0, nil
	}
	owned, err := r.ownedInstances(ctx, claim)
	if err != nil {
		return false, limit, err
	}
	held := 0
	for i := range owned {
		if owned[i].Labels[LabelClaimUID] != string(claim.UID) {
			held++
		}
	}
	return held >= limit, limit, nil
}

// evictIfOverQuota settles a quota race after the fact. Concurrent claims for one
// owner can each pass the pre-check before any of them binds, so once the
// instances exist we re-count and apply a stable rule: the oldest instances keep
// their slots. A claim that lost gives its instance back.
func (r *DployInstanceClaimReconciler) evictIfOverQuota(
	ctx context.Context,
	claim *dployv1alpha1.DployInstanceClaim,
	tmpl *dployv1alpha1.DployTemplate,
	eff operatorconfig.Effective,
	inst *dployv1alpha1.DployInstance,
) (evicted bool, limit int, err error) {
	limit = ownerLimit(tmpl, eff)
	if limit <= 0 {
		return false, 0, nil
	}
	owned, err := r.ownedInstances(ctx, claim)
	if err != nil {
		return false, limit, err
	}
	rank := -1
	for i := range owned {
		if owned[i].Name == inst.Name {
			rank = i
			break
		}
	}
	// Not in the list yet (a stale cache read) — the pre-check already passed, so
	// let it stand; the next reconcile re-evaluates.
	if rank < 0 || rank < limit {
		return false, limit, nil
	}
	if derr := r.Delete(ctx, inst); derr != nil && !apierrors.IsNotFound(derr) {
		return false, limit, fmt.Errorf("release over-quota instance: %w", derr)
	}
	return true, limit, nil
}

// errTemplateAtCapacity signals that serving a claim would require creating an
// instance the template's maxSize forbids. It travels as an error so bind stays
// a two-value function.
var errTemplateAtCapacity = errors.New("template at capacity")

// maxTemplateSize is the template's instance cap, 0 meaning unlimited.
func maxTemplateSize(tmpl *dployv1alpha1.DployTemplate) int {
	if tmpl.Spec.Method != dployv1alpha1.MethodPool || tmpl.Spec.Pool == nil {
		return 0
	}
	return tmpl.Spec.Pool.MaxSize
}

// templateInstances lists a template's live instances oldest-first, read
// uncached: a cached count is stale exactly when it matters, which is while a
// burst of claims is creating the instances being counted.
func (r *DployInstanceClaimReconciler) templateInstances(ctx context.Context, namespace, template string) ([]dployv1alpha1.DployInstance, error) {
	var list dployv1alpha1.DployInstanceList
	if err := r.reader().List(ctx, &list,
		client.InNamespace(namespace),
		client.MatchingLabels{LabelTemplate: template},
	); err != nil {
		return nil, fmt.Errorf("list template instances: %w", err)
	}
	out := make([]dployv1alpha1.DployInstance, 0, len(list.Items))
	for i := range list.Items {
		if list.Items[i].DeletionTimestamp.IsZero() {
			out = append(out, list.Items[i])
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreationTimestamp.Equal(&out[j].CreationTimestamp) {
			return out[i].CreationTimestamp.Before(&out[j].CreationTimestamp)
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// overMaxSize is the pre-check: is the template already at its cap?
func (r *DployInstanceClaimReconciler) overMaxSize(ctx context.Context, tmpl *dployv1alpha1.DployTemplate) (bool, error) {
	limit := maxTemplateSize(tmpl)
	if limit <= 0 {
		return false, nil
	}
	live, err := r.templateInstances(ctx, tmpl.Namespace, tmpl.Name)
	if err != nil {
		return false, err
	}
	return len(live) >= limit, nil
}

// evictIfOverMaxSize settles a capacity race after the fact, on the same rule
// the quota uses: the oldest `limit` instances keep their slots, so once every
// create is visible all claims agree on who overshot. A claim that lost gives
// its environment back rather than keeping the template above its cap.
func (r *DployInstanceClaimReconciler) evictIfOverMaxSize(
	ctx context.Context,
	tmpl *dployv1alpha1.DployTemplate,
	inst *dployv1alpha1.DployInstance,
) (evicted bool, limit int, err error) {
	limit = maxTemplateSize(tmpl)
	if limit <= 0 || inst == nil {
		return false, 0, nil
	}
	live, err := r.templateInstances(ctx, tmpl.Namespace, tmpl.Name)
	if err != nil {
		return false, limit, err
	}
	rank := -1
	for i := range live {
		if live[i].Name == inst.Name {
			rank = i
			break
		}
	}
	// Not visible yet — the pre-check already passed, so let it stand and let
	// the next reconcile decide against a complete list.
	if rank < 0 || rank < limit {
		return false, limit, nil
	}
	if derr := r.Delete(ctx, inst); derr != nil && !apierrors.IsNotFound(derr) {
		return false, limit, fmt.Errorf("release over-capacity instance: %w", derr)
	}
	return true, limit, nil
}

// --- TTL ---

// resolveClaimTTL returns the effective lifetime granted to the claim and the
// ceiling its spec may be raised to. Extending an environment means patching
// spec.ttlSeconds, so the ceiling is what the template's extend budget
// (ttl.maxExtends × ttl.extendSeconds) would have allowed.
func resolveClaimTTL(claim *dployv1alpha1.DployInstanceClaim, tmpl *dployv1alpha1.DployTemplate, eff operatorconfig.Effective) (ttl, maxTTL int64) {
	base := resolveInstanceTTL(tmpl, eff)
	maxTTL = maxLifetime(base, tmpl, eff)

	switch {
	case claim.Spec.TTLSeconds == 0:
		ttl = base
	case claim.Spec.TTLSeconds < 0:
		ttl = -1
	default:
		ttl = claim.Spec.TTLSeconds
	}

	// An unlimited request is only honored where the template itself is unlimited;
	// otherwise it collapses to the ceiling.
	if ttl == -1 && maxTTL != -1 {
		ttl = maxTTL
	}
	if maxTTL > 0 && ttl > maxTTL {
		ttl = maxTTL
	}
	return ttl, maxTTL
}

// maxLifetime is the longest an environment of this template may live: its base
// TTL plus the full extend budget. -1 means no ceiling.
func maxLifetime(base int64, tmpl *dployv1alpha1.DployTemplate, eff operatorconfig.Effective) int64 {
	if base == -1 {
		return -1
	}
	maxExtends, extend := eff.MaxExtends, eff.ExtendSeconds
	if tmpl.Spec.TTL != nil {
		if tmpl.Spec.TTL.MaxExtends != 0 {
			maxExtends = tmpl.Spec.TTL.MaxExtends
		}
		if tmpl.Spec.TTL.ExtendSeconds > 0 {
			extend = tmpl.Spec.TTL.ExtendSeconds
		}
	}
	if maxExtends <= 0 { // unset or explicitly unlimited
		return -1
	}
	return base + int64(maxExtends)*extend
}

// stampBoundAt records when an instance was bound, in the same write that wins
// it. Keeping the anchor on the instance — rather than only in the claim status,
// which is a second, separate write — is what lets a binding survive an operator
// that dies between the two.
func stampBoundAt(inst *dployv1alpha1.DployInstance, boundAt metav1.Time) {
	if inst.Annotations == nil {
		inst.Annotations = map[string]string{}
	}
	if inst.Annotations[AnnotationBoundAt] == "" {
		inst.Annotations[AnnotationBoundAt] = boundAt.UTC().Format(time.RFC3339)
	}
}

// boundAtOf recovers the binding anchor from an instance, falling back to its
// creation time (an on-demand instance is bound the moment it is created) and
// then to now, so a missing annotation can only ever shorten a lifetime, never
// extend one.
func boundAtOf(inst *dployv1alpha1.DployInstance) *metav1.Time {
	if raw := inst.Annotations[AnnotationBoundAt]; raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			out := metav1.NewTime(t)
			return &out
		}
	}
	if !inst.CreationTimestamp.IsZero() && !inst.Spec.Pooled {
		out := inst.CreationTimestamp.DeepCopy()
		return out
	}
	out := metav1.NewTime(time.Now())
	return &out
}

func expiryFrom(boundAt metav1.Time, ttl int64) *metav1.Time {
	if ttl <= 0 { // unlimited
		return nil
	}
	t := metav1.NewTime(boundAt.Add(time.Duration(ttl) * time.Second))
	return &t
}

func isExpired(claim *dployv1alpha1.DployInstanceClaim, now time.Time) bool {
	exp := claim.Status.ExpiresAt
	return exp != nil && !now.Before(exp.Time)
}

// --- helpers ---

// hashIndex maps a claim UID onto a stable index in [0, n). The shift keeps the
// value inside a 32-bit signed int, so the conversion is safe on any platform.
func hashIndex(uid types.UID, n int) int {
	if n <= 0 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(uid))
	return int(h.Sum32()>>1) % n
}

// SetupWithManager registers the controller with the manager.
func (r *DployInstanceClaimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dployv1alpha1.DployInstanceClaim{}).
		Watches(&dployv1alpha1.DployInstance{}, handler.EnqueueRequestsFromMapFunc(r.claimsForInstance)).
		WithOptions(controller.Options{MaxConcurrentReconciles: claimMaxConcurrentReconciles}).
		Named("dployinstanceclaim").
		Complete(r)
}

// claimsForInstance routes instance events to the claims that care: the owning
// claim for a bound instance, and every waiting claim for the template when a
// warm instance frees up. Without the second case a parked claim would only bind
// on its next periodic retry.
func (r *DployInstanceClaimReconciler) claimsForInstance(ctx context.Context, obj client.Object) []reconcile.Request {
	inst, ok := obj.(*dployv1alpha1.DployInstance)
	if !ok {
		return nil
	}
	if name := inst.Labels[LabelClaim]; name != "" {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: inst.Namespace, Name: name}}}
	}
	if !inst.Spec.Pooled || inst.Spec.Owner != "" {
		return nil
	}

	var claims dployv1alpha1.DployInstanceClaimList
	if err := r.List(ctx, &claims,
		client.InNamespace(inst.Namespace),
		client.MatchingLabels{LabelTemplate: inst.Spec.TemplateRef},
	); err != nil {
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(claims.Items))
	for i := range claims.Items {
		if claims.Items[i].Status.Phase != dployv1alpha1.ClaimPending {
			continue
		}
		reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: claims.Items[i].Namespace,
			Name:      claims.Items[i].Name,
		}})
	}
	return reqs
}
