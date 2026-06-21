// Package auth handles JWT verification and the OIDC login flow for the dploy
// API.
//
// Both JWT verification (jwt.go) and the OIDC login flow (this file) follow
// the canonical Go OIDC pattern used by Kubernetes, Argo CD and the official
// coreos/go-oidc README example: go-oidc for discovery + ID token
// verification, golang.org/x/oauth2 for the Authorization Code + PKCE flow,
// and four short-lived HttpOnly cookies to carry state/verifier/nonce/returnUrl
// across the browser bounce. No framework, no signed-cookie key management,
// nothing dploy-specific beyond the optional split-horizon issuer support
// and the SPA's "#token=..." hand-off.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/AYDEV-FR/dploy/internal/config"
	"github.com/AYDEV-FR/dploy/internal/logger"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gofiber/fiber/v2"
	"golang.org/x/oauth2"
)

// OIDCHandler wires the canonical go-oidc + oauth2 pair into Fiber handlers.
type OIDCHandler struct {
	oauth2Config *oauth2.Config
	verifier     *oidc.IDTokenVerifier
	secureCookie bool
}

const (
	discoveryTimeout  = 10 * time.Second
	discoveryAttempts = 5

	// flowCookieMaxAge bounds how long a user has to complete the IdP
	// bounce. 10 min covers slow MFA prompts without leaving stale state
	// cookies indefinitely.
	flowCookieMaxAge = 10 * 60

	cookieState    = "dploy_oidc_state"
	cookieVerifier = "dploy_oidc_verifier"
	cookieNonce    = "dploy_oidc_nonce"
	cookieReturn   = "dploy_oidc_return"
)

// NewOIDCHandler builds the RP from OIDC discovery. Optional split-horizon
// support: when OIDCPublicIssuer differs from OIDCIssuer, discovery is fetched
// via the in-cluster URL but the expected `iss` is the public one, and the
// browser-facing AuthURL is rebased to the public host. Single-URL setups
// leave OIDCPublicIssuer empty and the split-horizon branches are no-ops.
func NewOIDCHandler(cfg *config.Config) (*OIDCHandler, error) {
	ctx := context.Background()
	expectedIssuer := cfg.OIDCIssuer
	splitHorizon := cfg.OIDCPublicIssuer != "" && cfg.OIDCPublicIssuer != cfg.OIDCIssuer
	if splitHorizon {
		// Tell go-oidc to expect tokens with iss == publicIssuer even
		// though we hit the internal URL for the discovery doc.
		ctx = oidc.InsecureIssuerURLContext(ctx, cfg.OIDCPublicIssuer)
		expectedIssuer = cfg.OIDCPublicIssuer
	}

	provider, err := newProviderWithRetry(ctx, cfg.OIDCIssuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery against %s: %w", cfg.OIDCIssuer, err)
	}

	endpoint := provider.Endpoint()
	if splitHorizon {
		// What the discovery doc advertises depends on the IdP. Dex bakes
		// its configured `issuer` into every endpoint, so fetching the doc
		// via the internal URL still yields *public* endpoints — nothing to
		// rebase (the backend reaches the public token endpoint via
		// hostAliases/DNS). IdPs that derive endpoints from the request
		// Host instead return internal URLs; for those, the browser-facing
		// AuthURL must be rebased onto the public host.
		internalBase := extractBaseURL(cfg.OIDCIssuer)
		publicBase := extractBaseURL(cfg.OIDCPublicIssuer)
		switch {
		case strings.HasPrefix(endpoint.AuthURL, publicBase):
			logger.Info("OIDC auth endpoint already public, no rebase needed", "authURL", endpoint.AuthURL)
		case strings.HasPrefix(endpoint.AuthURL, internalBase):
			endpoint.AuthURL = strings.Replace(endpoint.AuthURL, internalBase, publicBase, 1)
			logger.Info("OIDC auth endpoint rebased to public",
				"internal", internalBase, "public", publicBase, "authURL", endpoint.AuthURL)
		default:
			logger.Warn("OIDC auth endpoint matches neither issuer base; leaving as-is",
				"authURL", endpoint.AuthURL, "internal", internalBase, "public", publicBase)
		}
	}

	h := &OIDCHandler{
		oauth2Config: &oauth2.Config{
			ClientID:     cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret,
			RedirectURL:  cfg.OIDCRedirectURL,
			Endpoint:     endpoint,
			Scopes:       cfg.OIDCScopes,
		},
		verifier:     provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID}),
		secureCookie: !strings.HasPrefix(cfg.OIDCRedirectURL, "http://"),
	}
	logger.Info("OIDC handler initialized", "expectedIssuer", expectedIssuer, "secureCookie", h.secureCookie)
	return h, nil
}

// newProviderWithRetry rides out the post-startup network-identity window
// (Cilium et al.) where DNS / egress briefly returns EPERM. 5 attempts with
// exponential backoff capped at 4 s.
func newProviderWithRetry(ctx context.Context, issuer string) (*oidc.Provider, error) {
	delay := 500 * time.Millisecond
	var lastErr error
	for i := 1; i <= discoveryAttempts; i++ {
		attemptCtx, cancel := context.WithTimeout(ctx, discoveryTimeout)
		provider, err := oidc.NewProvider(attemptCtx, issuer)
		cancel()
		if err == nil {
			if i > 1 {
				logger.Info("OIDC discovery succeeded after retries", "attempts", i)
			}
			return provider, nil
		}
		lastErr = err
		if i == discoveryAttempts {
			break
		}
		logger.Info("OIDC discovery attempt failed, retrying",
			"attempt", i, "nextDelay", delay, "error", err.Error())
		time.Sleep(delay)
		if delay < 4*time.Second {
			delay *= 2
		}
	}
	return nil, lastErr
}

func extractBaseURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
}

// sanitizeRelativePath validates that s is a safe relative URL (no scheme,
// host, userinfo, no protocol-relative or backslash trick) and returns the
// canonical form with any user-supplied #fragment dropped. The fragment-strip
// is required because the SPA's consumeHashToken() expects the final
// redirect's hash to be exclusively "#token=..." — a leftover "/foo#section"
// would otherwise produce "/foo#section#token=..." which can't be parsed.
func sanitizeRelativePath(s string) (string, bool) {
	if !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/\\") {
		return "", false
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "", false
	}
	// A percent-encoded backslash (e.g. "/%5cevil.com") survives the literal
	// "/\\" prefix check above but decodes into u.Path; user agents that
	// normalize "\" to "/" would then read "//evil.com" as protocol-relative.
	// Reject any backslash in the decoded path to close that bypass.
	if strings.Contains(u.Path, "\\") {
		return "", false
	}
	u.Fragment = ""
	return u.String(), true
}

func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (h *OIDCHandler) setFlowCookie(c *fiber.Ctx, name, value string) {
	c.Cookie(&fiber.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/auth",
		HTTPOnly: true,
		Secure:   h.secureCookie,
		SameSite: "Lax",
		MaxAge:   flowCookieMaxAge,
	})
}

func (h *OIDCHandler) clearFlowCookie(c *fiber.Ctx, name string) {
	c.Cookie(&fiber.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/auth",
		HTTPOnly: true,
		Secure:   h.secureCookie,
		SameSite: "Lax",
		MaxAge:   -1,
	})
}

// Login follows the official go-oidc example: random state + PKCE verifier,
// each stored in an HttpOnly cookie, plus the AuthCodeURL redirect. The
// returnUrl piggybacks as a third cookie — no state encoding/decoding
// gymnastics needed.
func (h *OIDCHandler) Login(c *fiber.Ctx) error {
	returnURL := "/"
	rawReturn := c.Query("returnUrl", "/")
	if clean, ok := sanitizeRelativePath(rawReturn); ok {
		returnURL = clean
	} else {
		logger.Warn("OIDC login: rejected unsafe returnUrl, defaulting to /", "returnUrl", rawReturn)
	}

	state, err := randomURLSafe(24)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to mint state"})
	}
	nonce, err := randomURLSafe(16)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to mint nonce"})
	}
	verifier := oauth2.GenerateVerifier()

	h.setFlowCookie(c, cookieState, state)
	h.setFlowCookie(c, cookieVerifier, verifier)
	h.setFlowCookie(c, cookieNonce, nonce)
	h.setFlowCookie(c, cookieReturn, returnURL)

	return c.Redirect(
		h.oauth2Config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)),
		fiber.StatusFound,
	)
}

// Callback exchanges the code for tokens, verifies the ID token, and bounces
// the browser to returnUrl with "#token=<id_token>" so the SPA can pick it up.
// CSRF protection comes from the state match (cookie vs query); replay
// protection comes from PKCE.
func (h *OIDCHandler) Callback(c *fiber.Ctx) error {
	state := c.Cookies(cookieState)
	verifier := c.Cookies(cookieVerifier)
	nonce := c.Cookies(cookieNonce)
	returnURL := c.Cookies(cookieReturn)
	// One-shot: clear cookies before any failure path so a stale flow can't
	// be retried by replaying the callback URL.
	h.clearFlowCookie(c, cookieState)
	h.clearFlowCookie(c, cookieVerifier)
	h.clearFlowCookie(c, cookieNonce)
	h.clearFlowCookie(c, cookieReturn)

	// IdP-side failure (user canceled, consent denied, scope rejected, …)
	// arrives as ?error=<code>&error_description=<text> per OAuth 2.0
	// §4.1.2.1. Surface it as a clean 400 rather than letting the request
	// fall through to a misleading "state mismatch" or token-exchange error.
	if idpErr := c.Query("error"); idpErr != "" {
		logger.Warn("OIDC callback: IdP returned error",
			"error", idpErr, "description", c.Query("error_description"))
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error":             "identity provider rejected the login: " + idpErr,
			"error_description": c.Query("error_description"),
		})
	}

	if state == "" || verifier == "" || nonce == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing or expired login session"})
	}
	if subtle.ConstantTimeCompare([]byte(c.Query("state")), []byte(state)) != 1 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "state mismatch"})
	}
	code := c.Query("code")
	if code == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing authorization code"})
	}

	token, err := h.oauth2Config.Exchange(c.Context(), code, oauth2.VerifierOption(verifier))
	if err != nil {
		// Detail stays server-side: oauth2 errors embed the token endpoint
		// URL, which would map internal cluster topology for the caller.
		logger.Error("OIDC callback: token exchange failed", "error", err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "token exchange failed"})
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "no id_token in token response"})
	}
	idToken, err := h.verifier.Verify(c.Context(), rawIDToken)
	if err != nil {
		logger.Error("OIDC callback: id_token verification failed", "error", err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "id_token verification failed"})
	}
	// Nonce binds this ID token to *our* login attempt: even if both the
	// auth code and PKCE verifier are intercepted, the IdP would return a
	// token carrying the attacker's nonce, not ours. Constant-time compare
	// because the nonce stays a one-shot secret until the cookie clears.
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonce)) != 1 {
		// Client-side/session issue or an attack, not an upstream failure —
		// mirror the state-mismatch path with a 400 rather than a 502.
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "nonce mismatch"})
	}

	// Re-sanitize the returnUrl from the cookie as defense-in-depth, even
	// though Login already vetted it before setting the cookie.
	if clean, ok := sanitizeRelativePath(returnURL); ok {
		returnURL = clean
	} else {
		returnURL = "/"
	}
	logger.Debug("OIDC callback complete", "returnUrl", returnURL, "tokenLength", len(rawIDToken))
	return c.Redirect(fmt.Sprintf("%s#token=%s", returnURL, rawIDToken), fiber.StatusFound)
}

// Logout bounces home — the SPA clears its localStorage token on the
// redirect. RP-initiated logout against the IdP would be an extra round-trip;
// add it when an IdP requires SLO.
func (h *OIDCHandler) Logout(c *fiber.Ctx) error {
	return c.Redirect("/", fiber.StatusFound)
}
