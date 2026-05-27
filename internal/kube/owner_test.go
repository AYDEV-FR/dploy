// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package kube

import (
	"strings"
	"testing"
)

func TestSanitizeOwner(t *testing.T) {
	cases := map[string]string{
		"Team-A":          "team-a",
		"alice@corp.com":  "alice-corp-com",
		"--Group_X!!":     "groupx",
		"platform.admins": "platform-admins",
		"":                "",
	}
	for in, want := range cases {
		if got := sanitizeOwner(in); got != want {
			t.Errorf("sanitizeOwner(%q) = %q, want %q", in, got, want)
		}
	}
	if got := sanitizeOwner(strings.Repeat("a", 80)); len(got) != maxOwnerLen {
		t.Errorf("expected truncation to %d, got %d", maxOwnerLen, len(got))
	}
}

func TestClaimValues(t *testing.T) {
	if v := claimValues("alice"); len(v) != 1 || v[0] != "alice" {
		t.Errorf("string: %v", v)
	}
	if v := claimValues([]any{"a", 1, "", "b"}); len(v) != 2 || v[0] != "a" || v[1] != "b" {
		t.Errorf("array: %v", v)
	}
	if v := claimValues(nil); v != nil {
		t.Errorf("nil: %v", v)
	}
	if v := claimValues([]string{"x", "y"}); len(v) != 2 {
		t.Errorf("[]string: %v", v)
	}
}

func TestResolveOwner(t *testing.T) {
	claims := map[string]any{
		"preferred_username": "Alice",
		"groups":             []any{"Team-A", "team-b"},
	}

	// Empty claim falls back to the username.
	if o, ok := ResolveOwner(claims, "", "Alice"); !ok || o != "alice" {
		t.Errorf("fallback: got %q ok=%v", o, ok)
	}
	// String claim.
	if o, ok := ResolveOwner(claims, "preferred_username", "ignored"); !ok || o != "alice" {
		t.Errorf("string claim: got %q ok=%v", o, ok)
	}
	// Multi-valued claim uses the first value.
	if o, ok := ResolveOwner(claims, "groups", "alice"); !ok || o != "team-a" {
		t.Errorf("array claim: got %q ok=%v", o, ok)
	}
	// Missing claim is not resolvable.
	if o, ok := ResolveOwner(claims, "absent", "alice"); ok || o != "" {
		t.Errorf("missing claim: got %q ok=%v", o, ok)
	}
}

func TestIdentities(t *testing.T) {
	claims := map[string]any{
		"groups": []any{"Team-A", "team-b"},
	}
	ids := Identities(claims, []string{"groups", "groups", ""}, "Alice")
	// username + both groups, deduped.
	want := map[string]bool{"alice": true, "team-a": true, "team-b": true}
	if len(ids) != len(want) {
		t.Fatalf("identities = %v, want keys %v", ids, want)
	}
	for _, id := range ids {
		if !want[id] {
			t.Errorf("unexpected identity %q", id)
		}
	}
}
