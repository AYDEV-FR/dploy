// Package auth handles JWT verification and the OIDC login flow for the dploy
// API. JWT verification delegates to the canonical github.com/coreos/go-oidc
// stack — RemoteKeySet handles the JWKS cache, refresh-on-unknown-kid and key
// algorithm flexibility; IDTokenVerifier handles signature, iss / aud / exp /
// nbf and (optional) nonce checks.
package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/AYDEV-FR/dploy/internal/logger"
	"github.com/coreos/go-oidc/v3/oidc"
)

// JWTValidator verifies ID tokens against the configured JWKS and turns the
// resolved subject into the sanitized owner key the rest of dploy uses.
//
// JWKS source is decoupled from the expected issuer (NewRemoteKeySet takes the
// URL directly) so we can keep pointing at the in-cluster Dex service while the
// tokens carry the public issuer URL — a classic split-horizon setup.
type JWTValidator struct {
	verifier      *oidc.IDTokenVerifier
	usernameClaim string
}

// NewJWTValidator wires a RemoteKeySet (with built-in cache + refresh on cache
// miss) and a Verifier (signature + iss + aud + exp). audience may be empty,
// in which case the aud check is skipped — useful for IdPs that don't issue
// API tokens with a stable audience.
func NewJWTValidator(jwksURL, issuer, audience, usernameClaim string) *JWTValidator {
	keySet := oidc.NewRemoteKeySet(context.Background(), jwksURL)
	verifier := oidc.NewVerifier(issuer, keySet, &oidc.Config{
		ClientID:          audience,
		SkipClientIDCheck: audience == "",
	})
	logger.Debug("JWT validator using go-oidc RemoteKeySet+IDTokenVerifier",
		"jwksURL", jwksURL, "issuer", issuer, "audience", audience, "usernameClaim", usernameClaim)
	return &JWTValidator{verifier: verifier, usernameClaim: usernameClaim}
}

// Validate verifies the token and returns the sanitized username plus the raw
// claims. All cryptographic and standard-claim checks live inside Verify; the
// only dploy-specific work is pulling the configured username claim and
// sanitizing it for use as a Kubernetes label.
func (v *JWTValidator) Validate(tokenString string) (username string, claims map[string]any, err error) {
	idToken, err := v.verifier.Verify(context.Background(), tokenString)
	if err != nil {
		return "", nil, fmt.Errorf("token parsing failed: %w", err)
	}
	claims = map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		return "", nil, fmt.Errorf("decode claims: %w", err)
	}
	rawUsername, _ := claims[v.usernameClaim].(string)
	if rawUsername == "" {
		return "", nil, fmt.Errorf("missing or invalid %s claim", v.usernameClaim)
	}
	sanitized := SanitizeUsername(rawUsername)
	logger.Debug("Token validated", "user", sanitized, "rawUser", rawUsername)
	return sanitized, claims, nil
}

// ValidateToken is a convenience wrapper returning only the sanitized owner key.
func (v *JWTValidator) ValidateToken(tokenString string) (string, error) {
	user, _, err := v.Validate(tokenString)
	return user, err
}

// SanitizeUsername normalises a raw claim value into a Kubernetes-label-safe
// owner key: lowercase, "." and "@" replaced with "-", anything outside
// [a-z0-9-] dropped.
func SanitizeUsername(username string) string {
	username = strings.ToLower(username)
	username = strings.ReplaceAll(username, ".", "-")
	username = strings.ReplaceAll(username, "@", "-")
	var result strings.Builder
	for _, r := range username {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		}
	}
	return result.String()
}
