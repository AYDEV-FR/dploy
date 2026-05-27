// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package controller

import (
	"context"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

// OperatorConfigReconciler observes the cluster-scoped OperatorConfig singleton,
// records its observed generation, and reports readiness.
type OperatorConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=dploy.dev,resources=operatorconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dploy.dev,resources=operatorconfigs/status,verbs=get;update;patch

// Reconcile validates the operator configuration and records its status.
func (r *OperatorConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cfg dployv1alpha1.OperatorConfig
	if err := r.Get(ctx, req.NamespacedName, &cfg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	original := cfg.DeepCopy()
	cfg.Status.ObservedGeneration = cfg.Generation

	reason, message := "Active", "Operator configuration is in effect."
	if cfg.Name != dployv1alpha1.OperatorConfigName {
		reason = "Ignored"
		message = "Only the OperatorConfig named \"default\" is honored; this object is ignored."
	}
	apimeta.SetStatusCondition(&cfg.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cfg.Generation,
	})

	if err := r.Status().Patch(ctx, &cfg, client.MergeFrom(original)); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager registers the controller with the manager.
func (r *OperatorConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dployv1alpha1.OperatorConfig{}).
		Named("operatorconfig").
		Complete(r)
}
