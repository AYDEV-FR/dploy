// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
	"github.com/AYDEV-FR/dploy/internal/operatorconfig"
)

const poolMaintenanceInterval = 30 * time.Second

// DployTemplateReconciler maintains the warm pool for pool-method templates by
// creating unclaimed DployInstances up to the configured size, and reports pool
// occupancy in the template status.
type DployTemplateReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=dploy.dev,resources=dploytemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dploy.dev,resources=dploytemplates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dploy.dev,resources=dploytemplates/finalizers,verbs=update
// +kubebuilder:rbac:groups=dploy.dev,resources=dployinstances,verbs=get;list;watch;create;update;patch;delete

// Reconcile keeps the template's warm pool at the desired size and updates occupancy.
func (r *DployTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var tmpl dployv1alpha1.DployTemplate
	if err := r.Get(ctx, req.NamespacedName, &tmpl); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var list dployv1alpha1.DployInstanceList
	if err := r.List(ctx, &list,
		client.InNamespace(tmpl.Namespace),
		client.MatchingLabels{LabelTemplate: tmpl.Name},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("list instances: %w", err)
	}

	var availableReady, claimed, total int
	// unclaimed is kept, not just counted: shrinking the pool has to pick
	// victims out of exactly this set.
	unclaimed := make([]*dployv1alpha1.DployInstance, 0, len(list.Items))
	for i := range list.Items {
		inst := &list.Items[i]
		if !inst.DeletionTimestamp.IsZero() {
			continue
		}
		total++
		if !inst.Spec.Pooled {
			continue
		}
		if inst.Spec.Owner == "" {
			unclaimed = append(unclaimed, inst)
			if inst.Status.Phase == dployv1alpha1.PhaseAvailable {
				availableReady++
			}
		} else {
			claimed++
		}
	}
	unclaimedSlots := len(unclaimed)

	// The fill loop runs to completion inside a single reconcile, and that is what
	// keeps it from over-filling. Every create it issues wakes this controller
	// again through the instance watch, and a re-reconcile running against a cache
	// that still missed those creates would count the same empty slot twice and
	// fill it twice. The purge below is a backstop for that now, but relying on it
	// would mean routinely building environments only to tear them down, so the
	// property still matters. It does not happen because the creates are
	// round trips and their watch events land while the loop is still running: the
	// cache converges during the burst rather than after it, and the work queue
	// collapses the resulting events into one follow-up reconcile.
	//
	// Two changes would break that and force tracking creates in flight (the
	// "expectations" pattern): returning early with creates outstanding — a refill
	// rate limit is the obvious way to end up there — or issuing the creates off
	// the reconcile goroutine. TestPoolFillsExactlyOnce pins the property, and
	// pool_kind_test.go stresses it against a real API server, where the cache lag
	// is real rather than in-process.
	created := 0
	if isPoolActive(&tmpl) {
		eff, err := operatorconfig.Resolve(ctx, r.Client)
		if err != nil {
			return ctrl.Result{}, err
		}
		ttl := resolveInstanceTTL(&tmpl, eff)
		maxSize := tmpl.Spec.Pool.MaxSize
		for unclaimedSlots+created < tmpl.Spec.Pool.Size {
			if maxSize > 0 && total+created >= maxSize {
				break
			}
			if err := r.createPoolInstance(ctx, &tmpl, ttl); err != nil {
				return ctrl.Result{}, err
			}
			created++
		}
	}

	// The mirror image of the fill loop. Without it, lowering pool.size did
	// nothing at all: nothing else reclaims a warm member, because applyTTL only
	// starts an instance's clock once it is Ready or Claimed and an unclaimed
	// member sits in Available forever.
	deleted, deletedAvailable := 0, 0
	if desired := desiredWarm(&tmpl); unclaimedSlots > desired {
		var err error
		deleted, deletedAvailable, err = r.purgeSurplus(ctx, unclaimed, unclaimedSlots-desired)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	original := tmpl.DeepCopy()
	tmpl.Status.PoolAvailable = availableReady - deletedAvailable
	tmpl.Status.PoolClaimed = claimed
	tmpl.Status.PoolTotal = total + created - deleted
	tmpl.Status.ObservedGeneration = tmpl.Generation
	if err := r.Status().Patch(ctx, &tmpl, client.MergeFrom(original)); err != nil {
		return ctrl.Result{}, fmt.Errorf("patch DployTemplate status: %w", err)
	}

	if isPoolActive(&tmpl) {
		return ctrl.Result{RequeueAfter: poolMaintenanceInterval}, nil
	}
	return ctrl.Result{}, nil
}

func (r *DployTemplateReconciler) createPoolInstance(ctx context.Context, tmpl *dployv1alpha1.DployTemplate, ttl int64) error {
	inst := &dployv1alpha1.DployInstance{}
	inst.GenerateName = tmpl.Name + "-pool-"
	inst.Namespace = tmpl.Namespace
	inst.Labels = map[string]string{
		LabelManaged:  "true",
		LabelTemplate: tmpl.Name,
		LabelPooled:   "true",
	}
	inst.Spec = dployv1alpha1.DployInstanceSpec{
		TemplateRef: tmpl.Name,
		Pooled:      true,
		TTLSeconds:  ttl,
	}
	if err := controllerutil.SetControllerReference(tmpl, inst, r.Scheme); err != nil {
		return fmt.Errorf("set owner on pool instance: %w", err)
	}
	if err := r.Create(ctx, inst); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create pool instance: %w", err)
	}
	return nil
}

// desiredWarm is how many unclaimed members a template should keep.
//
// It is deliberately not keyed on Enabled: disabling a template is usually
// temporary (maintenance, a challenge between rounds), the fill loop already
// declines to top it up, and tearing the warm set down would make re-enabling
// pay the full provisioning cost again. A template that is no longer pooled at
// all is different — acquirePooled only ever runs for MethodPool, so warm
// members left behind by a method switch can never be claimed by anyone and
// would leak until the template is deleted.
func desiredWarm(tmpl *dployv1alpha1.DployTemplate) int {
	if tmpl.Spec.Method != dployv1alpha1.MethodPool || tmpl.Spec.Pool == nil {
		return 0
	}
	return tmpl.Spec.Pool.Size
}

// purgeSurplus deletes up to excess unclaimed members, least useful first, and
// reports how many went and how many of those were warm and ready.
//
// Deletes are conditional on the exact object the reconcile listed: a claimer
// binds by updating the instance's claim-UID label, so if one won a candidate
// between the List above and the Delete here, the resourceVersion has moved and
// the API server rejects the delete as a conflict. That is the same optimistic
// lock acquirePooled relies on, read from the other side — without the
// precondition this would occasionally delete an environment a user had just
// been handed.
//
// A rejected victim is skipped rather than replaced, and that is not a shortfall
// to correct: losing the race means the instance became claimed, so it left the
// unclaimed set on its own and the surplus shrank with it.
func (r *DployTemplateReconciler) purgeSurplus(
	ctx context.Context,
	unclaimed []*dployv1alpha1.DployInstance,
	excess int,
) (deleted, deletedAvailable int, err error) {
	for _, inst := range sortPurgeVictims(unclaimed) {
		if deleted == excess {
			break
		}
		uid, rv := inst.UID, inst.ResourceVersion
		delErr := r.Delete(ctx, inst, client.Preconditions{UID: &uid, ResourceVersion: &rv})
		switch {
		case delErr == nil:
			deleted++
			if inst.Status.Phase == dployv1alpha1.PhaseAvailable {
				deletedAvailable++
			}
		case apierrors.IsConflict(delErr), apierrors.IsNotFound(delErr):
			// Claimed or already gone between the list and here.
			continue
		default:
			return deleted, deletedAvailable, fmt.Errorf("purge pool instance %q: %w", inst.Name, delErr)
		}
	}
	return deleted, deletedAvailable, nil
}

// sortPurgeVictims returns the candidates in the order they should be destroyed.
// It copies rather than sorting in place: the input aliases the reconcile's
// listed items, and reordering those under the caller would be a trap.
func sortPurgeVictims(unclaimed []*dployv1alpha1.DployInstance) []*dployv1alpha1.DployInstance {
	victims := make([]*dployv1alpha1.DployInstance, len(unclaimed))
	copy(victims, unclaimed)
	sort.Slice(victims, func(i, j int) bool {
		if a, b := purgeRank(victims[i]), purgeRank(victims[j]); a != b {
			return a < b
		}
		// Newest first: the older a warm member is, the longer it has been
		// proven healthy, so it is the one worth keeping.
		ti, tj := victims[i].CreationTimestamp, victims[j].CreationTimestamp
		if !ti.Equal(&tj) {
			return tj.Before(&ti)
		}
		// Same-second creations are the norm when a pool fills in one burst, so
		// the name breaks the tie and keeps the choice stable across reconciles.
		return victims[i].Name < victims[j].Name
	})
	return victims
}

// purgeRank orders warm members by how little is lost in destroying them: a
// failed instance is worth nothing, one still provisioning is not usable yet and
// has no user waiting on it, and an Available member is the only kind anyone can
// actually claim right now.
func purgeRank(inst *dployv1alpha1.DployInstance) int {
	switch inst.Status.Phase {
	case dployv1alpha1.PhaseFailed:
		return 0
	case dployv1alpha1.PhaseAvailable:
		return 2
	default:
		return 1
	}
}

func isPoolActive(tmpl *dployv1alpha1.DployTemplate) bool {
	return tmpl.Spec.Enabled &&
		tmpl.Spec.Method == dployv1alpha1.MethodPool &&
		tmpl.Spec.Pool != nil &&
		tmpl.Spec.Pool.Size > 0
}

// resolveInstanceTTL picks the template TTL if set (including -1 for unlimited),
// otherwise the cluster default.
func resolveInstanceTTL(tmpl *dployv1alpha1.DployTemplate, eff operatorconfig.Effective) int64 {
	if tmpl.Spec.TTL != nil && tmpl.Spec.TTL.Seconds != 0 {
		return tmpl.Spec.TTL.Seconds
	}
	return eff.TTLSeconds
}

// SetupWithManager registers the controller with the manager.
//
// Instances are watched by label rather than by ownership: claiming a warm pool
// member hands its controller reference over to the DployInstanceClaim, so an
// Owns() watch would go quiet on exactly the events that should refill the pool.
func (r *DployTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dployv1alpha1.DployTemplate{}).
		Watches(&dployv1alpha1.DployInstance{}, handler.EnqueueRequestsFromMapFunc(templateForInstance)).
		Named("dploytemplate").
		Complete(r)
}

// templateForInstance routes an instance event back to the template it derives from.
func templateForInstance(_ context.Context, obj client.Object) []reconcile.Request {
	inst, ok := obj.(*dployv1alpha1.DployInstance)
	if !ok || inst.Spec.TemplateRef == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{
		Namespace: inst.Namespace,
		Name:      inst.Spec.TemplateRef,
	}}}
}
