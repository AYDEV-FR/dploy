// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package handlers

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
	"github.com/AYDEV-FR/dploy/internal/config"
)

func TestInstanceStatus(t *testing.T) {
	cases := map[dployv1alpha1.InstancePhase]string{
		dployv1alpha1.PhaseProvisioning: "Progressing",
		dployv1alpha1.PhaseFailed:       "Degraded",
		dployv1alpha1.PhaseExpiring:     "Deleting",
		dployv1alpha1.PhasePending:      "pending",
		"":                              "pending",
	}
	for phase, want := range cases {
		inst := &dployv1alpha1.DployInstance{}
		inst.Status.Phase = phase
		if got := instanceStatus(inst); got != want {
			t.Errorf("phase %q: got %q, want %q", phase, got, want)
		}
	}

	// Ready uses reported health when present.
	ready := &dployv1alpha1.DployInstance{}
	ready.Status.Phase = dployv1alpha1.PhaseReady
	ready.Status.Health = "Healthy"
	if got := instanceStatus(ready); got != "Healthy" {
		t.Errorf("ready: got %q", got)
	}
	ready.Status.Health = ""
	if got := instanceStatus(ready); got != "Healthy" {
		t.Errorf("ready without health: got %q", got)
	}
}

func TestInstanceExpiresAt(t *testing.T) {
	inst := &dployv1alpha1.DployInstance{}
	if instanceExpiresAt(inst) != "" {
		t.Error("no expiry should be empty")
	}
	// Spec expiry used before the operator mirrors it to status.
	spec := metav1.NewTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	inst.Spec.ExpiresAt = &spec
	if got := instanceExpiresAt(inst); got != "2026-01-02T03:04:05Z" {
		t.Errorf("spec expiry: got %q", got)
	}
	// Status expiry takes precedence.
	status := metav1.NewTime(time.Date(2027, 6, 7, 8, 9, 10, 0, time.UTC))
	inst.Status.ExpiresAt = &status
	if got := instanceExpiresAt(inst); got != "2027-06-07T08:09:10Z" {
		t.Errorf("status expiry: got %q", got)
	}
}

func TestTemplateTTL(t *testing.T) {
	cfg := &config.Config{DefaultTTL: 86400, ExtendTTL: 7200}

	ttl, extend, maxExt, unlimited := templateTTL(&dployv1alpha1.DployTemplate{}, cfg)
	if ttl != 86400 || extend != 7200 || maxExt != 0 || unlimited {
		t.Errorf("defaults: got ttl=%d extend=%d max=%d unlimited=%v", ttl, extend, maxExt, unlimited)
	}

	tmpl := &dployv1alpha1.DployTemplate{
		Spec: dployv1alpha1.DployTemplateSpec{
			TTL: &dployv1alpha1.TTLSpec{Seconds: 100, ExtendSeconds: 50, MaxExtends: 2},
		},
	}
	ttl, extend, maxExt, unlimited = templateTTL(tmpl, cfg)
	if ttl != 100 || extend != 50 || maxExt != 2 || unlimited {
		t.Errorf("override: got ttl=%d extend=%d max=%d unlimited=%v", ttl, extend, maxExt, unlimited)
	}

	_, _, _, unlimited = templateTTL(withUnlimited(), cfg)
	if !unlimited {
		t.Error("ttl -1 should be unlimited")
	}
}

func withUnlimited() *dployv1alpha1.DployTemplate {
	return &dployv1alpha1.DployTemplate{
		Spec: dployv1alpha1.DployTemplateSpec{TTL: &dployv1alpha1.TTLSpec{Seconds: -1}},
	}
}
