// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

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

	var unclaimedSlots, availableReady, claimed, total int
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
			unclaimedSlots++
			if inst.Status.Phase == dployv1alpha1.PhaseAvailable {
				availableReady++
			}
		} else {
			claimed++
		}
	}

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

	original := tmpl.DeepCopy()
	tmpl.Status.PoolAvailable = availableReady
	tmpl.Status.PoolClaimed = claimed
	tmpl.Status.PoolTotal = total + created
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
func (r *DployTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dployv1alpha1.DployTemplate{}).
		Owns(&dployv1alpha1.DployInstance{}).
		Named("dploytemplate").
		Complete(r)
}
