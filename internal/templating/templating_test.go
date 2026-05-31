// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package templating

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestRender(t *testing.T) {
	data := &Data{
		Owner:      "alice",
		UUID:       "abc12345",
		BaseDomain: "env.dploy.dev",
		Host:       "vscode-abc12345.env.dploy.dev",
		Params:     map[string]string{"size": "large"},
		Claims:     map[string]any{"email": "a@b.c"},
	}
	out, err := Render("t",
		`{{ .Owner }}|{{ .UUID }}|{{ .Params.size }}|{{ .Claims.email }}|{{ upper "x" }}|{{ .Host }}`,
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
	if _, err := Render("t", `{{ .Claims.absent }}`, &Data{Claims: map[string]any{}}); err != nil {
		t.Errorf("missing key should not error, got %v", err)
	}
}

func TestRenderParseError(t *testing.T) {
	if _, err := Render("t", `{{ .Owner `, &Data{}); err == nil {
		t.Error("expected parse error for malformed template")
	}
}

func TestClaimsMap(t *testing.T) {
	m, err := ClaimsMap(&runtime.RawExtension{Raw: []byte(`{"email":"a@b.c","groups":["x","y"]}`)})
	if err != nil {
		t.Fatalf("ClaimsMap: %v", err)
	}
	if m["email"] != "a@b.c" {
		t.Errorf("email = %v", m["email"])
	}
	// nil snapshot yields a usable empty map.
	empty, err := ClaimsMap(nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("nil claims: got %v, err %v", empty, err)
	}
}
