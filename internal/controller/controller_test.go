// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package controller

import (
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"John.Doe@Example.com": "john-doe-example-com",
		"Alice":                "alice",
		"--weird__name!!":      "weirdname",
		"a.b.c":                "a-b-c",
		"":                     "",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorkloadNamespace(t *testing.T) {
	if got := workloadNamespace("John.Doe", "WebTerm", "abc12345"); got != "john-doe-webterm-abc12345" {
		t.Errorf("got %q", got)
	}
	// Unclaimed pool member: empty owner becomes "pool".
	if got := workloadNamespace("", "tmpl", "deadbeef"); got != "pool-tmpl-deadbeef" {
		t.Errorf("got %q", got)
	}
}

func TestIngressHost(t *testing.T) {
	if got := ingressHost("Alice", "abc12345", "env.dploy.dev"); got != "alice-abc12345.env.dploy.dev" {
		t.Errorf("got %q", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "x", "y"); got != "x" {
		t.Errorf("got %q", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestTranslateHelmRelease(t *testing.T) {
	cond := func(s metav1.ConditionStatus) *helmv2.HelmRelease {
		hr := &helmv2.HelmRelease{}
		hr.Status.Conditions = []metav1.Condition{{Type: fluxmeta.ReadyCondition, Status: s, Reason: "R"}}
		return hr
	}
	if st := translateHelmRelease(&helmv2.HelmRelease{}); st.readiness != readinessInProgress {
		t.Errorf("no condition: want inProgress, got %v", st.readiness)
	}
	if st := translateHelmRelease(cond(metav1.ConditionTrue)); st.readiness != readinessReady || st.health != "Healthy" {
		t.Errorf("true: got readiness=%v health=%q", st.readiness, st.health)
	}
	if st := translateHelmRelease(cond(metav1.ConditionFalse)); st.readiness != readinessFailed || st.health != "Degraded" {
		t.Errorf("false: got readiness=%v health=%q", st.readiness, st.health)
	}
}

func TestPhaseFor(t *testing.T) {
	ready := helmReleaseState{readiness: readinessReady}
	failed := helmReleaseState{readiness: readinessFailed}
	prog := helmReleaseState{readiness: readinessInProgress}

	onDemand := &dployv1alpha1.DployInstance{}
	if p := phaseFor(onDemand, ready); p != dployv1alpha1.PhaseReady {
		t.Errorf("on-demand ready: got %q", p)
	}
	if p := phaseFor(onDemand, prog); p != dployv1alpha1.PhaseProvisioning {
		t.Errorf("on-demand progressing: got %q", p)
	}
	if p := phaseFor(onDemand, failed); p != dployv1alpha1.PhaseFailed {
		t.Errorf("failed: got %q", p)
	}

	poolUnclaimed := &dployv1alpha1.DployInstance{Spec: dployv1alpha1.DployInstanceSpec{Pooled: true}}
	if p := phaseFor(poolUnclaimed, ready); p != dployv1alpha1.PhaseAvailable {
		t.Errorf("pool unclaimed ready: got %q", p)
	}

	poolClaimed := &dployv1alpha1.DployInstance{Spec: dployv1alpha1.DployInstanceSpec{Pooled: true, Owner: "alice"}}
	if p := phaseFor(poolClaimed, ready); p != dployv1alpha1.PhaseClaimed {
		t.Errorf("pool claimed ready: got %q", p)
	}
}
