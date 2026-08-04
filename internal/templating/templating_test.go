// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package templating

import (
	"testing"
)

func TestRender(t *testing.T) {
	data := &Data{
		Owner:      "alice",
		UUID:       "abc12345",
		BaseDomain: "env.dploy.dev",
		Host:       "vscode-abc12345.env.dploy.dev",
		Params:     map[string]string{"size": "large", "email": "a@b.c"},
	}
	out, err := Render("t",
		`{{ .Owner }}|{{ .UUID }}|{{ .Params.size }}|{{ .Params.email }}|{{ upper "x" }}|{{ .Host }}`,
		data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "alice|abc12345|large|a@b.c|X|vscode-abc12345.env.dploy.dev"
	if out != want {
		t.Errorf("Render = %q, want %q", out, want)
	}
}

func TestRenderMissingKeyDoesNotError(t *testing.T) {
	// A template may reference an optional parameter (or an identity claim the
	// deployment doesn't forward) without failing the whole instance.
	if _, err := Render("t", `{{ .Params.absent }}`, &Data{Params: map[string]string{}}); err != nil {
		t.Errorf("missing key should not error, got %v", err)
	}
}

func TestRenderParseError(t *testing.T) {
	if _, err := Render("t", `{{ .Owner `, &Data{}); err == nil {
		t.Error("expected parse error for malformed template")
	}
}

// TestRenderRejectsRemovedClaimsVariable pins the removal of `.Claims`: request
// context now arrives as a single `.Params` map, so a template still reaching for
// the old variable must fail loudly rather than silently render an empty string.
func TestRenderRejectsRemovedClaimsVariable(t *testing.T) {
	if _, err := Render("t", `{{ .Claims.email }}`, &Data{}); err == nil {
		t.Error("expected an error for a template referencing the removed .Claims")
	}
}
