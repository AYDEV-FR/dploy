// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
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

	// APIReader bypasses the informer cache. purgeSurplus re-reads every victim
	// through it: a listed ResourceVersion is stale the moment anything writes
	// the instance — a phase transition, a URL, a Flux status — so a delete
	// precondition built on it is rejected on every attempt against a live
	// cluster and the pool never shrinks. envtest hides this, because there the
	// instance controller is stubbed and nothing writes the instances at all.
	APIReader client.Reader
}

// reader returns the uncached reader, falling back to the cached client when
// the reconciler was built without one.
func (r *DployTemplateReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// +kubebuilder:rbac:groups=dploy.dev,resources=dploytemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dploy.dev,resources=dploytemplates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dploy.dev,resources=dploytemplates/finalizers,verbs=update
// +kubebuilder:rbac:groups=dploy.dev,resources=dployinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=gitrepositories;helmrepositories;helmcharts,verbs=get;list;watch;create;update;patch;delete

// Reconcile keeps the template's warm pool at the desired size and updates occupancy.
func (r *DployTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var tmpl dployv1alpha1.DployTemplate
	if err := r.Get(ctx, req.NamespacedName, &tmpl); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// The template owns its Flux source, and it owns the verdict on whether that
	// source — and the chart reference inside it — actually resolves. Instances
	// refuse to apply a HelmRelease until this says yes, so an unresolvable chart
	// costs one failing probe instead of one failing HelmChart per instance. A
	// handful of those is enough to starve helm-controller for the whole cluster.
	eff, err := operatorconfig.Resolve(ctx, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	srcReady, srcKnown, srcReason, srcMessage, err := r.syncSource(ctx, &tmpl, eff)
	if err != nil {
		return ctrl.Result{}, err
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
	// Filling a pool whose chart is known not to resolve only manufactures
	// environments that can never come up. An unknown verdict is not a refusal:
	// the members wait, and no HelmRelease is applied until the probe says yes.
	srcBroken := srcKnown && !srcReady
	if isPoolActive(&tmpl) && !srcBroken {
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
	sourceStatus := metav1.ConditionFalse
	if srcReady {
		sourceStatus = metav1.ConditionTrue
	}
	apimeta.SetStatusCondition(&tmpl.Status.Conditions, metav1.Condition{
		Type:               dployv1alpha1.ConditionSourceReady,
		Status:             sourceStatus,
		Reason:             srcReason,
		Message:            srcMessage,
		ObservedGeneration: tmpl.Generation,
	})
	tmpl.Status.PoolAvailable = availableReady - deletedAvailable
	tmpl.Status.PoolClaimed = claimed
	tmpl.Status.PoolTotal = total + created - deleted
	tmpl.Status.ObservedGeneration = tmpl.Generation
	if err := r.Status().Patch(ctx, &tmpl, client.MergeFrom(original)); err != nil {
		return ctrl.Result{}, fmt.Errorf("patch DployTemplate status: %w", err)
	}

	if isPoolActive(&tmpl) || !srcReady {
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
// syncSource reconciles the template's Flux source and its single chart probe,
// and reports whether an instance may now safely apply a HelmRelease.
func (r *DployTemplateReconciler) syncSource(
	ctx context.Context,
	tmpl *dployv1alpha1.DployTemplate,
	eff operatorconfig.Effective,
) (ready, known bool, reason, message string, err error) {
	if chartRef(tmpl) == "" {
		return false, true, "ChartNotConfigured", "template has neither chart.path nor chart.chart set", nil
	}
	srcKind, srcName, err := ensureSource(ctx, r.Client, r.Scheme, tmpl, eff)
	if err != nil {
		if isCreateRace(err) {
			return false, false, "SourcePending", "another reconcile is creating the source", nil
		}
		if fluxAbsent(err) {
			return false, false, "FluxUnavailable", "the Flux source CRDs are not installed on this cluster", nil
		}
		return false, false, "", "", err
	}
	hc, herr := ensureChartProbe(ctx, r.Client, r.Scheme, tmpl, srcKind, srcName, eff)
	if herr != nil {
		if isCreateRace(herr) {
			return false, false, "ChartProbePending", "another reconcile is creating the chart probe", nil
		}
		if fluxAbsent(herr) {
			return false, false, "FluxUnavailable", "the Flux HelmChart CRD is not installed on this cluster", nil
		}
		return false, false, "", "", herr
	}
	ready, known, reason, message = translateChartProbe(hc)
	return ready, known, reason, message, nil
}

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
		// Re-read uncached. The listed ResourceVersion cannot be trusted as a
		// claim detector: it moves on any write, so on a live cluster it is
		// almost always stale and every delete below would be refused.
		var fresh dployv1alpha1.DployInstance
		if err := r.reader().Get(ctx, client.ObjectKeyFromObject(inst), &fresh); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return deleted, deletedAvailable, fmt.Errorf("re-read pool instance %q: %w", inst.Name, err)
		}
		// The question the ResourceVersion precondition was standing in for,
		// asked directly and against fresh state.
		if !isPurgeable(&fresh) {
			continue
		}
		uid, rv := fresh.UID, fresh.ResourceVersion
		delErr := r.Delete(ctx, &fresh, client.Preconditions{UID: &uid, ResourceVersion: &rv})
		switch {
		case delErr == nil:
			deleted++
			if fresh.Status.Phase == dployv1alpha1.PhaseAvailable {
				deletedAvailable++
			}
		case apierrors.IsConflict(delErr), apierrors.IsNotFound(delErr):
			// A genuine race now, not a stale cache. Say so: a purge that skips
			// every victim in silence is exactly how this stayed invisible.
			logf.FromContext(ctx).V(1).Info("pool purge victim raced, skipping",
				"instance", fresh.Name, "reason", delErr)
			continue
		default:
			return deleted, deletedAvailable, fmt.Errorf("purge pool instance %q: %w", inst.Name, delErr)
		}
	}
	return deleted, deletedAvailable, nil
}

// isPurgeable reports whether an unclaimed member may be destroyed. Unlike
// isClaimable it does not require PhaseAvailable: a member still Provisioning,
// or one that landed in Failed, is precisely the kind worth reclaiming.
func isPurgeable(inst *dployv1alpha1.DployInstance) bool {
	return inst.DeletionTimestamp.IsZero() &&
		inst.Spec.Owner == "" &&
		inst.Labels[LabelClaimUID] == ""
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
