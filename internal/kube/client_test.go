// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package kube

import (
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

func TestClaimName(t *testing.T) {
	if got := ClaimName("alice", "webterm"); got != "alice-webterm" {
		t.Errorf("ClaimName = %q", got)
	}
	long := ClaimName("alice", string(make([]byte, 300)))
	if len(long) > maxClaimNameLen {
		t.Errorf("name too long: %d", len(long))
	}
}

func TestExtendCount(t *testing.T) {
	claim := func(granted int64) *dployv1alpha1.DployInstanceClaim {
		c := &dployv1alpha1.DployInstanceClaim{}
		c.Status.TTLSeconds = granted
		return c
	}
	cases := []struct {
		name                 string
		granted, base, extra int64
		want                 int
	}{
		{"never extended", 3600, 3600, 1800, 0},
		{"one extension", 5400, 3600, 1800, 1},
		{"three extensions", 9000, 3600, 1800, 3},
		{"unlimited has no count", -1, 3600, 1800, 0},
		{"not bound yet", 0, 3600, 1800, 0},
		{"no extend step configured", 5400, 3600, 0, 0},
		{"granted below base (template shrank)", 1800, 3600, 1800, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtendCount(claim(tc.granted), tc.base, tc.extra); got != tc.want {
				t.Errorf("ExtendCount = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestFilterClaims(t *testing.T) {
	claims := map[string]any{
		"sub":                "abc-123",
		"email":              "alice@example.com",
		"groups":             []any{"devs", "admins"},
		"unlisted_secret":    "do-not-forward",
		"empty":              "",
		"preferred_username": "alice",
	}
	got := FilterClaims(claims, []string{"sub", "email", "groups", "empty", "absent"})

	want := map[string]string{
		"sub":    "abc-123",
		"email":  "alice@example.com",
		"groups": "devs,admins", // multi-valued claims are flattened
	}
	if len(got) != len(want) {
		t.Fatalf("FilterClaims = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("FilterClaims[%q] = %q, want %q", k, got[k], v)
		}
	}
	// The whole point: an unlisted claim never reaches the cluster.
	if _, leaked := got["unlisted_secret"]; leaked {
		t.Error("an unlisted claim was forwarded")
	}

	if FilterClaims(claims, nil) != nil {
		t.Error("no allowlist should forward nothing")
	}
	if FilterClaims(nil, []string{"sub"}) != nil {
		t.Error("no claims should forward nothing")
	}
	if FilterClaims(map[string]any{"a": "b"}, []string{"absent"}) != nil {
		t.Error("no matching claim should yield nil, not an empty map")
	}
}

func TestClaimIsActive(t *testing.T) {
	for phase, want := range map[dployv1alpha1.ClaimPhase]bool{
		dployv1alpha1.ClaimPending:  true,
		dployv1alpha1.ClaimBound:    true,
		dployv1alpha1.ClaimRejected: false,
		dployv1alpha1.ClaimExpired:  false,
		"":                          true, // not reconciled yet — assume it will hold one
	} {
		c := &dployv1alpha1.DployInstanceClaim{}
		c.Status.Phase = phase
		if got := c.IsActive(); got != want {
			t.Errorf("phase %q: IsActive = %v, want %v", phase, got, want)
		}
	}
}

// boundClaim builds a claim the operator has already bound, with the given
// granted and maximum lifetimes.
func boundClaim(granted, maxTTL int64) *dployv1alpha1.DployInstanceClaim {
	c := &dployv1alpha1.DployInstanceClaim{}
	c.Status.Phase = dployv1alpha1.ClaimBound
	now := metav1.Now()
	c.Status.BoundAt = &now
	c.Status.TTLSeconds = granted
	c.Status.MaxTTLSeconds = maxTTL
	return c
}

func TestExtendClaimPreconditions(t *testing.T) {
	// These are the checks that don't need a cluster: everything that decides
	// whether the patch is even legal.
	cases := []struct {
		name  string
		claim *dployv1alpha1.DployInstanceClaim
		want  error
	}{
		{"unlimited needs no extension", boundClaim(-1, -1), ErrUnlimitedTTL},
		{"unbound has no clock to extend", &dployv1alpha1.DployInstanceClaim{}, ErrNotBound},
		{"already at the template ceiling", boundClaim(1200, 1200), ErrMaxExtends},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A nil client is safe here: every case must fail before any API call.
			c := &Client{}
			if _, err := c.ExtendClaim(t.Context(), tc.claim, 600); !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}
