// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
	"github.com/AYDEV-FR/dploy/internal/operatorconfig"
	"github.com/AYDEV-FR/dploy/internal/templating"
)

const (
	provisioningRequeue = 15 * time.Second
	failureRequeue      = 30 * time.Second
	deletionRequeue     = 5 * time.Second
)

// DployInstanceReconciler materializes a DployInstance into a Flux source
// (GitRepository/HelmRepository) plus a HelmRelease, projects their observed
// status back onto the instance, and enforces the instance TTL.
type DployInstanceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=dploy.dev,resources=dployinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dploy.dev,resources=dployinstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dploy.dev,resources=dployinstances/finalizers,verbs=update
// +kubebuilder:rbac:groups=dploy.dev,resources=dploytemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups=dploy.dev,resources=operatorconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=helm.toolkit.fluxcd.io,resources=helmreleases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=gitrepositories;helmrepositories;ocirepositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile drives a DployInstance toward its desired state.
func (r *DployInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var inst dployv1alpha1.DployInstance
	if err := r.Get(ctx, req.NamespacedName, &inst); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !inst.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &inst)
	}

	// Ensure the finalizer and management labels in a single metadata update.
	if r.ensureMeta(&inst) {
		if err := r.Update(ctx, &inst); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	original := inst.DeepCopy()

	var tmpl dployv1alpha1.DployTemplate
	if err := r.Get(ctx, types.NamespacedName{Namespace: inst.Namespace, Name: inst.Spec.TemplateRef}, &tmpl); err != nil {
		if apierrors.IsNotFound(err) {
			return r.fail(ctx, original, &inst, "TemplateNotFound",
				fmt.Sprintf("DployTemplate %q not found in namespace %q", inst.Spec.TemplateRef, inst.Namespace))
		}
		return ctrl.Result{}, err
	}

	eff, err := operatorconfig.Resolve(ctx, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Assign the immutable short UUID on first reconcile.
	if inst.Status.UUID == "" {
		inst.Status.UUID = shortUUID()
		inst.Status.Phase = dployv1alpha1.PhasePending
		inst.Status.Engine = dployv1alpha1.EngineFlux
		if err := r.patchStatus(ctx, original, &inst); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	targetNS := workloadNamespace(inst.Spec.Owner, inst.Spec.TemplateRef, inst.Status.UUID)
	inst.Status.Namespace = targetNS
	inst.Status.Engine = dployv1alpha1.EngineFlux

	data, err := r.buildData(&inst, &tmpl, eff, targetNS)
	if err != nil {
		return r.fail(ctx, original, &inst, "ClaimsDecodeError", err.Error())
	}

	// Resolve the connection URL: template override → config default → fallback host.
	url := "https://" + data.IngressHost
	if urlTmpl := firstNonEmpty(tmpl.Spec.ConnectionURLTemplate, eff.ConnectionURLTemplate); urlTmpl != "" {
		rendered, rerr := templating.Render("connectionURL", urlTmpl, data)
		if rerr != nil {
			return r.fail(ctx, original, &inst, "ConnectionURLTemplateError", rerr.Error())
		}
		url = strings.TrimSpace(rendered)
	}
	inst.Status.URL = url
	data.URL = url

	// Render Helm values (YAML → JSON) when a values template is declared.
	var valuesJSON []byte
	if strings.TrimSpace(tmpl.Spec.ValuesTemplate) != "" {
		rendered, rerr := templating.Render("values", tmpl.Spec.ValuesTemplate, data)
		if rerr != nil {
			return r.fail(ctx, original, &inst, "ValuesTemplateError", rerr.Error())
		}
		valuesJSON, rerr = yaml.YAMLToJSON([]byte(rendered))
		if rerr != nil {
			return r.fail(ctx, original, &inst, "ValuesYAMLError", rerr.Error())
		}
	}

	// Materialize: workload namespace → Flux source → HelmRelease.
	if err := r.ensureNamespace(ctx, targetNS, &inst); err != nil {
		return r.fail(ctx, original, &inst, "NamespaceError", err.Error())
	}
	srcKind, srcName, err := r.ensureSource(ctx, &inst, &tmpl, eff)
	if err != nil {
		return r.fail(ctx, original, &inst, "SourceError", err.Error())
	}
	if err := r.ensureHelmRelease(ctx, &inst, &tmpl, eff, srcKind, srcName, targetNS, valuesJSON); err != nil {
		return r.fail(ctx, original, &inst, "HelmReleaseError", err.Error())
	}
	inst.Status.EngineRef = engineResourceName(&inst)

	// Project the HelmRelease's observed status onto the instance.
	var hr helmv2.HelmRelease
	if err := r.Get(ctx, types.NamespacedName{Namespace: inst.Namespace, Name: engineResourceName(&inst)}, &hr); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	state := translateHelmRelease(&hr)
	inst.Status.Health = state.health
	apimeta.SetStatusCondition(&inst.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             state.status,
		Reason:             state.reason,
		Message:            state.message,
		ObservedGeneration: inst.Generation,
	})
	inst.Status.Phase = phaseFor(&inst, state)

	// Enforce TTL (may delete the instance).
	requeueAfter, expired, terr := r.applyTTL(ctx, original, &inst)
	if terr != nil {
		return ctrl.Result{}, terr
	}
	if expired {
		return ctrl.Result{}, nil
	}

	if err := r.patchStatus(ctx, original, &inst); err != nil {
		return ctrl.Result{}, err
	}

	res := ctrl.Result{RequeueAfter: requeueAfter}
	if state.readiness == readinessInProgress {
		if res.RequeueAfter == 0 || res.RequeueAfter > provisioningRequeue {
			res.RequeueAfter = provisioningRequeue
		}
	}
	return res, nil
}

// reconcileDelete removes the HelmRelease, waits for helm-controller to finish
// the uninstall, then deletes the workload namespace and the finalizer. The Flux
// source is owner-referenced and garbage-collected when the finalizer is removed.
func (r *DployInstanceReconciler) reconcileDelete(ctx context.Context, inst *dployv1alpha1.DployInstance) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(inst, InstanceFinalizer) {
		return ctrl.Result{}, nil
	}
	original := inst.DeepCopy()
	inst.Status.Phase = dployv1alpha1.PhaseExpiring

	name := engineResourceName(inst)
	var hr helmv2.HelmRelease
	err := r.Get(ctx, types.NamespacedName{Namespace: inst.Namespace, Name: name}, &hr)
	switch {
	case err == nil:
		if hr.DeletionTimestamp.IsZero() {
			if derr := r.Delete(ctx, &hr); derr != nil && !apierrors.IsNotFound(derr) {
				return ctrl.Result{}, fmt.Errorf("delete HelmRelease: %w", derr)
			}
		}
		_ = r.patchStatus(ctx, original, inst)
		return ctrl.Result{RequeueAfter: deletionRequeue}, nil
	case !apierrors.IsNotFound(err):
		return ctrl.Result{}, fmt.Errorf("get HelmRelease: %w", err)
	}

	if inst.Status.Namespace != "" {
		ns := &corev1.Namespace{}
		ns.Name = inst.Status.Namespace
		if derr := r.Delete(ctx, ns); derr != nil && !apierrors.IsNotFound(derr) {
			return ctrl.Result{}, fmt.Errorf("delete namespace: %w", derr)
		}
	}

	controllerutil.RemoveFinalizer(inst, InstanceFinalizer)
	if err := r.Update(ctx, inst); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// ensureMeta adds the finalizer and management labels, returning true if the
// object's metadata changed and must be persisted.
func (r *DployInstanceReconciler) ensureMeta(inst *dployv1alpha1.DployInstance) bool {
	changed := false
	if !controllerutil.ContainsFinalizer(inst, InstanceFinalizer) {
		controllerutil.AddFinalizer(inst, InstanceFinalizer)
		changed = true
	}
	if inst.Labels == nil {
		inst.Labels = map[string]string{}
	}
	want := map[string]string{
		LabelManaged:  "true",
		LabelTemplate: inst.Spec.TemplateRef,
	}
	if inst.Spec.Pooled {
		want[LabelPooled] = "true"
	}
	if o := sanitize(inst.Spec.Owner); o != "" {
		want[LabelOwner] = o
	}
	for k, v := range want {
		if inst.Labels[k] != v {
			inst.Labels[k] = v
			changed = true
		}
	}
	return changed
}

func (r *DployInstanceReconciler) buildData(inst *dployv1alpha1.DployInstance, tmpl *dployv1alpha1.DployTemplate, eff operatorconfig.Effective, targetNS string) (*templating.Data, error) {
	claims, err := templating.ClaimsMap(inst.Spec.Claims)
	if err != nil {
		return nil, err
	}
	return &templating.Data{
		Owner:       sanitize(inst.Spec.Owner),
		UUID:        inst.Status.UUID,
		BaseDomain:  eff.BaseDomain,
		IngressHost: ingressHost(inst.Spec.Owner, inst.Status.UUID, eff.BaseDomain),
		Namespace:   targetNS,
		Template:    tmpl,
		Params:      inst.Spec.Params,
		Claims:      claims,
		Config:      templating.Config{Values: eff.Values},
	}, nil
}

// applyTTL resolves the effective expiry, stamps status.ExpiresAt, and deletes
// the instance once expired. Returns the duration until expiry to requeue on.
func (r *DployInstanceReconciler) applyTTL(ctx context.Context, original, inst *dployv1alpha1.DployInstance) (time.Duration, bool, error) {
	active := inst.Status.Phase == dployv1alpha1.PhaseReady || inst.Status.Phase == dployv1alpha1.PhaseClaimed
	switch {
	case inst.Spec.TTLSeconds == -1:
		inst.Status.ExpiresAt = nil
		return 0, false, nil
	case inst.Spec.ExpiresAt != nil:
		// API-authoritative expiry (e.g. set at creation or on a TTL extend).
		inst.Status.ExpiresAt = inst.Spec.ExpiresAt
	case inst.Spec.TTLSeconds > 0 && active:
		// Anchor the clock at the moment the instance first becomes active.
		if inst.Status.ExpiresAt == nil {
			inst.Status.ExpiresAt = &metav1.Time{Time: time.Now().Add(time.Duration(inst.Spec.TTLSeconds) * time.Second)}
		}
	default:
		return 0, false, nil
	}

	exp := inst.Status.ExpiresAt
	if exp == nil {
		return 0, false, nil
	}
	if time.Now().Before(exp.Time) {
		return time.Until(exp.Time), false, nil
	}

	inst.Status.Phase = dployv1alpha1.PhaseExpiring
	if err := r.patchStatus(ctx, original, inst); err != nil {
		return 0, false, err
	}
	if err := r.Delete(ctx, inst); err != nil && !apierrors.IsNotFound(err) {
		return 0, false, fmt.Errorf("delete expired instance: %w", err)
	}
	return 0, true, nil
}

func phaseFor(inst *dployv1alpha1.DployInstance, state helmReleaseState) dployv1alpha1.InstancePhase {
	switch state.readiness {
	case readinessFailed:
		return dployv1alpha1.PhaseFailed
	case readinessReady:
		if inst.Spec.Pooled {
			if inst.Spec.Owner == "" {
				return dployv1alpha1.PhaseAvailable
			}
			return dployv1alpha1.PhaseClaimed
		}
		return dployv1alpha1.PhaseReady
	default:
		return dployv1alpha1.PhaseProvisioning
	}
}

func (r *DployInstanceReconciler) fail(ctx context.Context, original, inst *dployv1alpha1.DployInstance, reason, message string) (ctrl.Result, error) {
	inst.Status.Phase = dployv1alpha1.PhaseFailed
	apimeta.SetStatusCondition(&inst.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: inst.Generation,
	})
	if err := r.patchStatus(ctx, original, inst); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: failureRequeue}, nil
}

func (r *DployInstanceReconciler) patchStatus(ctx context.Context, original, inst *dployv1alpha1.DployInstance) error {
	inst.Status.ObservedGeneration = inst.Generation
	if err := r.Status().Patch(ctx, inst, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("patch DployInstance status: %w", err)
	}
	return nil
}

// SetupWithManager registers the controller with the manager.
func (r *DployInstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dployv1alpha1.DployInstance{}).
		Owns(&helmv2.HelmRelease{}).
		Owns(&sourcev1.GitRepository{}).
		Owns(&sourcev1.HelmRepository{}).
		Named("dployinstance").
		Complete(r)
}
