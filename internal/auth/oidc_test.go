package auth

import "testing"

// TestSanitizeRelativePath pins both behaviors in one go: the relative-URL
// safety check (open-redirect surface) and the fragment-stripping canonical
// form the SPA's consumeHashToken() relies on. Both come out of one
// net/url.Parse pass — no string fiddling.
func TestSanitizeRelativePath(t *testing.T) {
	for _, tc := range []struct {
		in     string
		want   string
		wantOK bool
	}{
		// happy paths — exact passthrough or fragment dropped
		{"/", "/", true},
		{"/foo", "/foo", true},
		{"/foo?x=1", "/foo?x=1", true},
		{"/foo?x=1#frag", "/foo?x=1", true}, // fragment stripped
		{"/foo#frag", "/foo", true},
		{"/#frag", "/", true},

		// classic open-redirect tricks — all rejected
		{"//evil.com/x", "", false},       // protocol-relative URL
		{"/\\evil.com/x", "", false},      // backslash-prefixed (some browsers)
		{"http://evil.com/x", "", false},  // absolute URL
		{"https://evil.com/x", "", false}, // absolute URL https
		{"javascript:alert(1)", "", false},
		{"data:text/html,x", "", false},
		{"//user@evil.com/x", "", false}, // userinfo trick
		{"", "", false},                  // empty
		{"foo/bar", "", false},           // no leading slash
		{" /foo", "", false},             // leading whitespace
	} {
		got, ok := sanitizeRelativePath(tc.in)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("sanitizeRelativePath(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

// TestExtractBaseURL guards the split-horizon AuthURL rebase: if the helper
// strips path/query as expected, strings.Replace(authURL, internalBase, ...)
// hits exactly one match. The test cases mirror the two real-world Issuers
// (in-cluster Service URL with port, public URL via ingress).
func TestExtractBaseURL(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"http://dex.dex.svc.cluster.local:5556", "http://dex.dex.svc.cluster.local:5556"},
		{"http://dex.dex.svc.cluster.local:5556/", "http://dex.dex.svc.cluster.local:5556"},
		{"https://dex.dploy.ctf.local", "https://dex.dploy.ctf.local"},
		{"https://dex.dploy.ctf.local/dex", "https://dex.dploy.ctf.local"},
		{"https://dex.dploy.ctf.local/dex/auth?x=1", "https://dex.dploy.ctf.local"},
	} {
		if got := extractBaseURL(tc.in); got != tc.want {
			t.Errorf("extractBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
