package auth

import (
	"strings"
	"testing"
)

// TestSanitizeRelativePath pins both behaviours in one go: the relative-URL
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

// TestDecodeStateReturnURL pins the "<nonce>:<returnUrl>" wire format. Same
// helper produces (in Login) and consumes (in Callback) this string; if
// either side ever changes the separator, this test fails before the SPA
// login flow does.
func TestDecodeStateReturnURL(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
		want  string
	}{
		{"empty state", "", "/"},
		{"no separator", "abc123", "/"},
		{"nonce only", "abc123:", "/"},                                     // urlPart="" → reject → "/"
		{"happy root", "abc123:/", "/"},
		{"happy path", "abc123:/dashboard", "/dashboard"},
		{"path with query", "abc123:/foo?x=1", "/foo?x=1"},
		{"path with colon — only first ':' splits", "abc123:/foo:bar", "/foo:bar"},
		{"fragment stripped (SPA hash hand-off must stay clean)", "abc123:/foo#section", "/foo"},
		{"open-redirect via protocol-relative URL", "abc123://evil.com/x", "/"},
		{"open-redirect via absolute URL", "abc123:http://evil.com/x", "/"},
		{"backslash trick", "abc123:/\\evil.com/x", "/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeStateReturnURL(tc.state)
			if got != tc.want {
				t.Errorf("decodeStateReturnURL(%q) = %q, want %q", tc.state, got, tc.want)
			}
		})
	}

	// Round-trip sanity: what Login encodes, Callback decodes back.
	for _, returnURL := range []string{"/", "/foo", "/foo?x=1", "/foo:bar"} {
		state := "deadbeef" + ":" + returnURL
		if got := decodeStateReturnURL(state); got != returnURL {
			t.Errorf("round-trip for %q failed: got %q", returnURL, got)
		}
		// And verify nonce is *not* echoed into the return URL.
		if strings.Contains(decodeStateReturnURL(state), "deadbeef") {
			t.Errorf("nonce leaked into returnURL for state %q", state)
		}
	}
}
