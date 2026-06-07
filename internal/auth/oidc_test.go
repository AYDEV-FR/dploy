package auth

import "testing"

func TestStripFragment(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"/foo", "/foo"},
		{"/foo?x=1", "/foo?x=1"},
		{"/foo#frag", "/foo"},
		{"/foo?x=1#frag", "/foo?x=1"},
		{"#frag", ""},
		{"", ""},
	} {
		if got := stripFragment(tc.in); got != tc.want {
			t.Errorf("stripFragment(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSafeRelativePath covers the open-redirect surface a CTF participant is
// likely to probe through ?returnUrl=. Anything that isn't a plain
// "/path[?…][#…]" must be rejected before it reaches c.Redirect, where the
// browser would otherwise treat protocol-relative or backslash-prefixed
// inputs as cross-origin.
func TestSafeRelativePath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// happy paths
		{"/", true},
		{"/foo", true},
		{"/foo/bar?x=1#frag", true},

		// classic open-redirect tricks — all must be rejected
		{"//evil.com/x", false},       // protocol-relative URL
		{"/\\evil.com/x", false},      // backslash-prefixed (some browsers)
		{"http://evil.com/x", false},  // absolute URL
		{"https://evil.com/x", false}, // absolute URL https
		{"javascript:alert(1)", false},
		{"data:text/html,x", false},
		{"//user@evil.com/x", false}, // userinfo trick
		{"", false},                  // empty
		{"foo/bar", false},           // no leading slash
		{" /foo", false},             // leading whitespace (Go parser doesn't strip it; we'd happily redirect)
	}
	for _, tc := range cases {
		got := safeRelativePath(tc.in)
		if got != tc.want {
			t.Errorf("safeRelativePath(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
