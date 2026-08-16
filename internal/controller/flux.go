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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
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

// engineResourceName is the instance's HelmRelease, created in the instance's
// own namespace (so owner refs are valid). The Flux source it points at is not
// per-instance — see templateSourceName.
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

// templateSourceName is the Flux source shared by every instance of a template.
// It lives beside the instances, in the template's namespace, so the HelmRelease
// reference stays same-namespace.
func templateSourceName(tmpl *dployv1alpha1.DployTemplate) string {
	return tmpl.Name + "-src"
}

// templateLabels marks an object that belongs to a template rather than to any
// one instance — so it carries no instance UUID and no owner.
func templateLabels(tmpl *dployv1alpha1.DployTemplate) map[string]string {
	return map[string]string{
		LabelManaged:  "true",
		LabelTemplate: tmpl.Name,
	}
}

// ensureSource reconciles the Flux source backing the instance's chart and
// returns the source Kind and name to wire into the HelmRelease. dploy only
// exposes git/helm charts, so the source is a GitRepository or HelmRepository
// (OCI Helm registries are a HelmRepository of type "oci").
//
// One source per *template*, not per instance. Every instance of a template
// resolves the same URL at the same revision, so a copy per instance had
// source-controller cloning one repository once per environment and re-fetching
// each copy on its own interval: at 300 environments on a 5m interval that is a
// fetch a second against the forge for content that never differs, plus 300
// artifacts and 300 objects to reconcile. Measured on kind, a 50-environment
// burst produced 60 byte-identical GitRepositories.
//
// The template owns it, which is where its lifetime belongs — the source
// describes the catalog entry, not any one environment. The consequence worth
// knowing: deleting a template removes the source from under any claimed
// instance still running off it. The workload keeps serving (Helm has already
// installed it) but Flux can no longer upgrade or re-render that release.
//
// Every instance computes an identical spec, so CreateOrUpdate writes on the
// first instance and is a no-op for the rest: a burst causes no write
// amplification on the shared object.
func (r *DployInstanceReconciler) ensureSource(ctx context.Context, tmpl *dployv1alpha1.DployTemplate, eff operatorconfig.Effective) (kind, name string, err error) {
	cs := tmpl.Spec.Chart
	name = templateSourceName(tmpl)
	interval := metav1.Duration{Duration: eff.FluxInterval}

	if cs.Type == dployv1alpha1.ChartSourceHelm {
		repoType := sourcev1.HelmRepositoryTypeDefault
		if strings.HasPrefix(cs.RepoURL, "oci://") {
			repoType = sourcev1.HelmRepositoryTypeOCI
		}
		repo := &sourcev1.HelmRepository{}
		repo.Name, repo.Namespace = name, tmpl.Namespace
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, repo, func() error {
			repo.Labels = templateLabels(tmpl)
			repo.Spec.URL = cs.RepoURL
			repo.Spec.Type = repoType
			repo.Spec.Interval = interval
			return controllerutil.SetControllerReference(tmpl, repo, r.Scheme)
		})
		if err != nil {
			return "", "", fmt.Errorf("ensure HelmRepository: %w", err)
		}
		return sourcev1.HelmRepositoryKind, name, nil
	}

	// Default: git source.
	repo := &sourcev1.GitRepository{}
	repo.Name, repo.Namespace = name, tmpl.Namespace
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, repo, func() error {
		repo.Labels = templateLabels(tmpl)
		repo.Spec.URL = cs.RepoURL
		repo.Spec.Reference = &sourcev1.GitRepositoryRef{Branch: firstNonEmpty(cs.TargetRevision, "main")}
		repo.Spec.Interval = interval
		return controllerutil.SetControllerReference(tmpl, repo, r.Scheme)
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
