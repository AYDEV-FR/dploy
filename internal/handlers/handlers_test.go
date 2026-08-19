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

func TestClaimStatus(t *testing.T) {
	// A claim that isn't bound reports its own phase; the UI has no instance to
	// look at, which is the point of projecting everything onto the claim.
	claimPhases := map[dployv1alpha1.ClaimPhase]string{
		dployv1alpha1.ClaimPending:  "pending",
		dployv1alpha1.ClaimRejected: "Degraded",
		dployv1alpha1.ClaimExpired:  "Deleting",
		"":                          "pending",
	}
	for phase, want := range claimPhases {
		c := &dployv1alpha1.DployInstanceClaim{}
		c.Status.Phase = phase
		if got := claimStatus(c); got != want {
			t.Errorf("claim phase %q: got %q, want %q", phase, got, want)
		}
	}

	// Once bound, the instance's phase drives the status.
	instancePhases := map[dployv1alpha1.InstancePhase]string{
		dployv1alpha1.PhaseProvisioning: "Progressing",
		dployv1alpha1.PhaseFailed:       "Degraded",
		dployv1alpha1.PhaseExpiring:     "Deleting",
		dployv1alpha1.PhasePending:      "pending",
		"":                              "pending",
	}
	for phase, want := range instancePhases {
		c := &dployv1alpha1.DployInstanceClaim{}
		c.Status.Phase = dployv1alpha1.ClaimBound
		c.Status.InstancePhase = phase
		if got := claimStatus(c); got != want {
			t.Errorf("instance phase %q: got %q, want %q", phase, got, want)
		}
	}

	// Ready uses the engine-reported health when present.
	ready := &dployv1alpha1.DployInstanceClaim{}
	ready.Status.Phase = dployv1alpha1.ClaimBound
	ready.Status.InstancePhase = dployv1alpha1.PhaseClaimed
	ready.Status.Health = "Degraded"
	if got := claimStatus(ready); got != "Degraded" {
		t.Errorf("claimed with health: got %q", got)
	}
	ready.Status.Health = ""
	if got := claimStatus(ready); got != "Healthy" {
		t.Errorf("claimed without health: got %q", got)
	}
}

func TestClaimExpiresAt(t *testing.T) {
	claim := &dployv1alpha1.DployInstanceClaim{}
	if claimExpiresAt(claim) != "" {
		t.Error("an unbound claim has no expiry")
	}
	exp := metav1.NewTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	claim.Status.ExpiresAt = &exp
	if got := claimExpiresAt(claim); got != "2026-01-02T03:04:05Z" {
		t.Errorf("expiry: got %q", got)
	}
}

func TestClaimMessage(t *testing.T) {
	claim := &dployv1alpha1.DployInstanceClaim{}
	claim.Status.Phase = dployv1alpha1.ClaimPending
	claim.Status.Conditions = []metav1.Condition{{
		Type:    dployv1alpha1.ConditionBound,
		Status:  metav1.ConditionFalse,
		Reason:  "PoolExhausted",
		Message: "waiting for a warm instance",
	}}
	if got := claimMessage(claim); got != "waiting for a warm instance" {
		t.Errorf("pending message: got %q", got)
	}

	// A running environment has nothing to explain.
	claim.Status.Phase = dployv1alpha1.ClaimBound
	if got := claimMessage(claim); got != "" {
		t.Errorf("bound claim should have no message, got %q", got)
	}

	if got := claimMessage(&dployv1alpha1.DployInstanceClaim{}); got != "" {
		t.Errorf("no condition should yield no message, got %q", got)
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
