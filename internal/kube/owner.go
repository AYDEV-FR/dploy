// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package kube

import (
	"fmt"
	"strconv"
	"strings"
)

// maxOwnerLen bounds an owner key to a valid DNS-1123 label value.
const maxOwnerLen = 63

// sanitizeOwner normalizes a claim value into a label-safe owner key. It MUST
// stay in sync with the operator's sanitize (internal/controller) so the labels
// the API sets and the operator reconciles agree.
func sanitizeOwner(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.ReplaceAll(s, "@", "-")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > maxOwnerLen {
		out = strings.Trim(out[:maxOwnerLen], "-")
	}
	return out
}

// claimValues extracts the string value(s) of a JWT claim, which may be a scalar
// or an array (e.g. "groups").
func claimValues(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case float64:
		return []string{strconv.FormatFloat(t, 'f', -1, 64)}
	case bool:
		return []string{strconv.FormatBool(t)}
	default:
		return []string{fmt.Sprintf("%v", t)}
	}
}

// ResolveOwner resolves the single owner key for a template from the requester's
// claims. An empty claim falls back to the (already-sanitized) username. For a
// multi-valued claim the first usable value is used. ok is false when no usable
// value is present.
func ResolveOwner(claims map[string]any, claim, fallbackUsername string) (string, bool) {
	if claim == "" {
		o := sanitizeOwner(fallbackUsername)
		return o, o != ""
	}
	for _, v := range claimValues(claims[claim]) {
		if o := sanitizeOwner(v); o != "" {
			return o, true
		}
	}
	return "", false
}

// Identities returns every owner key the requester maps to: the username plus all
// value(s) of each owner claim used across templates. It lets the API list every
// environment a user owns when ownership keys vary per template (personal +
// team-shared).
func Identities(claims map[string]any, ownerClaims []string, fallbackUsername string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(s string) {
		o := sanitizeOwner(s)
		if o == "" {
			return
		}
		if _, dup := seen[o]; dup {
			return
		}
		seen[o] = struct{}{}
		out = append(out, o)
	}
	add(fallbackUsername)
	for _, c := range ownerClaims {
		if c == "" {
			continue
		}
		for _, v := range claimValues(claims[c]) {
			add(v)
		}
	}
	return out
}
