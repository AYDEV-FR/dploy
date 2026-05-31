package auth

import "testing"

func TestIsAdmin(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims map[string]any
		claim  string
		value  string
		want   bool
	}{
		{"bool true matches true", map[string]any{"is_admin": true}, "is_admin", "true", true},
		{"bool true rejected when expecting false", map[string]any{"is_admin": true}, "is_admin", "false", false},
		{"bool false matches false", map[string]any{"is_admin": false}, "is_admin", "false", true},
		{"bool false rejected when expecting true", map[string]any{"is_admin": false}, "is_admin", "true", false},
		{"string role equality", map[string]any{"role": "admin"}, "role", "admin", true},
		{"string role mismatch", map[string]any{"role": "user"}, "role", "admin", false},
		{"groups []any contains admin", map[string]any{"groups": []any{"dev", "admin"}}, "groups", "admin", true},
		{"groups []any does not contain admin", map[string]any{"groups": []any{"dev"}}, "groups", "admin", false},
		{"groups []string contains admin", map[string]any{"groups": []string{"admin"}}, "groups", "admin", true},
		{"missing claim", map[string]any{}, "is_admin", "true", false},
		{"nil claims", nil, "is_admin", "true", false},
		{"empty claim name", map[string]any{"is_admin": true}, "", "true", false},
		{"unsupported type (number) → deny", map[string]any{"is_admin": 1}, "is_admin", "true", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAdmin(tc.claims, tc.claim, tc.value); got != tc.want {
				t.Errorf("IsAdmin(%v, %q, %q) = %v, want %v", tc.claims, tc.claim, tc.value, got, tc.want)
			}
		})
	}
}
