// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"fmt"
	"strings"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
	"github.com/AYDEV-FR/dploy/internal/operatorconfig"
)

const maxReleaseNameLen = 53

// readiness is the coarse outcome of a HelmRelease's Ready condition.
type readiness int

const (
	readinessInProgress readiness = iota
	readinessReady
	readinessFailed
)

// helmReleaseState is the projection of a HelmRelease's Ready condition.
type helmReleaseState struct {
	readiness readiness
	status    metav1.ConditionStatus
	reason    string
	message   string
	health    string
}

// translateHelmRelease maps a HelmRelease's Flux Ready condition onto a coarse
// state, using the canonical Flux readiness constant.
func translateHelmRelease(hr *helmv2.HelmRelease) helmReleaseState {
	ready := apimeta.FindStatusCondition(hr.Status.Conditions, fluxmeta.ReadyCondition)
	if ready == nil {
		return helmReleaseState{
			readiness: readinessInProgress,
			status:    metav1.ConditionUnknown,
			reason:    "HelmReleasePending",
			message:   "HelmRelease has not been reconciled by helm-controller yet.",
			health:    "Progressing",
		}
	}
	reason := firstNonEmpty(ready.Reason, "HelmReleaseProgressing")
	message := firstNonEmpty(ready.Message, "HelmRelease reported no message.")
	switch ready.Status {
	case metav1.ConditionTrue:
		return helmReleaseState{readinessReady, metav1.ConditionTrue, reason, message, "Healthy"}
	case metav1.ConditionFalse:
		return helmReleaseState{readinessFailed, metav1.ConditionFalse, reason, message, "Degraded"}
	default:
		return helmReleaseState{readinessInProgress, metav1.ConditionUnknown, reason, message, "Progressing"}
	}
}

// engineResourceName is the name shared by the instance's Flux source and
// HelmRelease, created in the instance's own namespace (so owner refs are valid).
func engineResourceName(inst *dployv1alpha1.DployInstance) string {
	return inst.Name
}

func managedLabels(inst *dployv1alpha1.DployInstance) map[string]string {
	l := map[string]string{
		LabelManaged:  "true",
		LabelTemplate: inst.Spec.TemplateRef,
		LabelInstance: inst.Status.UUID,
	}
	if o := sanitize(inst.Spec.Owner); o != "" {
		l[LabelOwner] = o
	}
	return l
}

// ensureNamespace creates (or relabels) the per-instance workload namespace. It
// carries no owner reference — a namespaced CR cannot own a cluster-scoped
// Namespace — so the finalizer deletes it explicitly.
func (r *DployInstanceReconciler) ensureNamespace(ctx context.Context, name string, inst *dployv1alpha1.DployInstance) error {
	ns := &corev1.Namespace{}
	ns.Name = name
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ns, func() error {
		if ns.Labels == nil {
			ns.Labels = map[string]string{}
		}
		for k, v := range managedLabels(inst) {
			ns.Labels[k] = v
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("ensure namespace %s: %w", name, err)
	}
	return nil
}

// ensureSource reconciles the Flux source backing the instance's chart and
// returns the source Kind and name to wire into the HelmRelease. dploy only
// exposes git/helm charts, so the source is a GitRepository or HelmRepository
// (OCI Helm registries are a HelmRepository of type "oci").
func (r *DployInstanceReconciler) ensureSource(ctx context.Context, inst *dployv1alpha1.DployInstance, tmpl *dployv1alpha1.DployTemplate, eff operatorconfig.Effective) (kind, name string, err error) {
	cs := tmpl.Spec.Chart
	name = engineResourceName(inst)
	interval := metav1.Duration{Duration: eff.FluxInterval}

	if cs.Type == dployv1alpha1.ChartSourceHelm {
		repoType := sourcev1.HelmRepositoryTypeDefault
		if strings.HasPrefix(cs.RepoURL, "oci://") {
			repoType = sourcev1.HelmRepositoryTypeOCI
		}
		repo := &sourcev1.HelmRepository{}
		repo.Name, repo.Namespace = name, inst.Namespace
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, repo, func() error {
			repo.Labels = managedLabels(inst)
			repo.Spec.URL = cs.RepoURL
			repo.Spec.Type = repoType
			repo.Spec.Interval = interval
			return controllerutil.SetControllerReference(inst, repo, r.Scheme)
		})
		if err != nil {
			return "", "", fmt.Errorf("ensure HelmRepository: %w", err)
		}
		return sourcev1.HelmRepositoryKind, name, nil
	}

	// Default: git source.
	repo := &sourcev1.GitRepository{}
	repo.Name, repo.Namespace = name, inst.Namespace
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, repo, func() error {
		repo.Labels = managedLabels(inst)
		repo.Spec.URL = cs.RepoURL
		repo.Spec.Reference = &sourcev1.GitRepositoryRef{Branch: firstNonEmpty(cs.TargetRevision, "main")}
		repo.Spec.Interval = interval
		return controllerutil.SetControllerReference(inst, repo, r.Scheme)
	})
	if err != nil {
		return "", "", fmt.Errorf("ensure GitRepository: %w", err)
	}
	return sourcev1.GitRepositoryKind, name, nil
}

// ensureHelmRelease reconciles the HelmRelease that installs the chart into the
// instance's workload namespace.
func (r *DployInstanceReconciler) ensureHelmRelease(
	ctx context.Context,
	inst *dployv1alpha1.DployInstance,
	tmpl *dployv1alpha1.DployTemplate,
	eff operatorconfig.Effective,
	sourceKind, sourceName, targetNS string,
	valuesJSON []byte,
) error {
	cs := tmpl.Spec.Chart
	// For git the chart lives at a path in the repo; for helm it's the chart name.
	chart := firstNonEmpty(cs.Path, cs.Chart)
	version := ""
	if cs.Type == dployv1alpha1.ChartSourceHelm {
		version = firstNonEmpty(cs.TargetRevision, "*")
	}

	hr := &helmv2.HelmRelease{}
	hr.Name, hr.Namespace = engineResourceName(inst), inst.Namespace
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, hr, func() error {
		hr.Labels = managedLabels(inst)
		hr.Spec.Chart = &helmv2.HelmChartTemplate{
			Spec: helmv2.HelmChartTemplateSpec{
				Chart:       chart,
				Version:     version,
				ValuesFiles: tmpl.Spec.ValueFiles,
				SourceRef: helmv2.CrossNamespaceObjectReference{
					Kind: sourceKind,
					Name: sourceName,
				},
			},
		}
		hr.Spec.ReleaseName = truncate(inst.Name, maxReleaseNameLen)
		hr.Spec.TargetNamespace = targetNS
		hr.Spec.StorageNamespace = targetNS
		hr.Spec.Interval = metav1.Duration{Duration: eff.FluxInterval}
		hr.Spec.Install = &helmv2.Install{
			Remediation: &helmv2.InstallRemediation{Retries: 3},
		}
		if len(valuesJSON) > 0 {
			hr.Spec.Values = &apiextensionsv1.JSON{Raw: valuesJSON}
		} else {
			hr.Spec.Values = nil
		}
		return controllerutil.SetControllerReference(inst, hr, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("ensure HelmRelease: %w", err)
	}
	return nil
}
