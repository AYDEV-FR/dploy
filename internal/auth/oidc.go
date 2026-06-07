package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/AYDEV-FR/dploy/internal/config"
	"github.com/AYDEV-FR/dploy/internal/logger"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gofiber/fiber/v2"
	"github.com/gorilla/securecookie"
	"golang.org/x/oauth2"
)

// stateBlob is what we encode + sign into the OAuth2 `state` parameter.
// Carrying the data inside the signed token keeps things stateless — no
// server-side map, no cleanup goroutine — so the only failure mode left
// is "browser took longer than stateTTL to come back", which is intended.
//
// Nonce binds the returned id_token to this specific login attempt (OIDC
// replay defense); PKCEVerifier proves to the IdP that the client redeeming
// the code is the same one that started the flow (defense against an
// intercepted auth code).
type stateBlob struct {
	ReturnURL    string
	Nonce        string
	PKCEVerifier string
	Expiry       int64 // unix seconds
}

// safeRelativePath enforces "/path[?...][#...]" — no scheme, no host, no
// protocol-relative "//host/..." trick. `url.Parse` does the heavy lifting,
// we just inspect the result.
func safeRelativePath(s string) bool {
	if !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/\\") {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "" && u.Host == "" && u.User == nil
}

// OIDCHandler runs the OAuth2 / OIDC Authorization Code flow with the
// configured IdP. Heavy lifting is delegated to golang.org/x/oauth2 (Code
// exchange, AuthCodeURL) and github.com/coreos/go-oidc (discovery). The only
// dploy-specific bits are split-horizon endpoint handling, a one-shot state
// map for CSRF protection, and the post-callback hash-fragment redirect.
type OIDCHandler struct {
	config       *config.Config
	oauth2Config *oauth2.Config
	idVerifier   *oidc.IDTokenVerifier      // verifies id_tokens at /auth/callback
	sc           *securecookie.SecureCookie // signs+encodes the OAuth2 state blob
}

const (
	stateTTL          = 10 * time.Minute
	discoveryTimeout  = 10 * time.Second
	discoveryAttempts = 5
)

// NewOIDCHandler discovers the IdP endpoints, wires an oauth2.Config and
// starts the state-cleanup goroutine. Returns an error only if discovery
// keeps failing after the retry budget — boot-time network races are the
// usual reason and 5 attempts with exponential backoff covers them in
// practice.
func NewOIDCHandler(cfg *config.Config) (*OIDCHandler, error) {
	// Split-horizon: tokens carry the public issuer URL (what the browser
	// sees), but we discover and call the IdP through the in-cluster URL.
	// InsecureIssuerURLContext tells go-oidc which issuer to expect.
	ctx := context.Background()
	if cfg.OIDCPublicIssuer != "" && cfg.OIDCPublicIssuer != cfg.OIDCIssuer {
		ctx = oidc.InsecureIssuerURLContext(ctx, cfg.OIDCPublicIssuer)
	}

	provider, err := discoverWithRetry(ctx, cfg.OIDCIssuer)
	if err != nil {
		return nil, fmt.Errorf("failed to discover OIDC endpoints from %s: %w", cfg.OIDCIssuer, err)
	}
	logger.Info("OIDC discovery completed", "issuer", cfg.OIDCIssuer)

	// Browser redirects must hit the *public* authorization endpoint; backend
	// code exchange stays on the *internal* token endpoint. Discovery gave us
	// both with internal URLs — substitute the public base on AuthURL.
	endpoint := provider.Endpoint()
	authURL := endpoint.AuthURL
	if cfg.OIDCPublicIssuer != "" && cfg.OIDCPublicIssuer != cfg.OIDCIssuer {
		internalBase := extractBaseURL(cfg.OIDCIssuer)
		publicBase := extractBaseURL(cfg.OIDCPublicIssuer)
		authURL = strings.Replace(endpoint.AuthURL, internalBase, publicBase, 1)
		logger.Info("OIDC auth endpoint rebased to public",
			"internal", internalBase, "public", publicBase, "authURL", authURL)
	}

	// Keys are random per process: an in-flight login that straddles a pod
	// restart fails closed (same as the in-memory map this replaces). Stable
	// keys via env/secret would survive restarts — easy follow-up if needed.
	sc := securecookie.New(securecookie.GenerateRandomKey(64), securecookie.GenerateRandomKey(32))
	sc.MaxAge(int(stateTTL.Seconds()))

	// Reuse the provider's KeySet for callback-time id_token verification.
	// Same JWKS + same expected issuer as the request-time JWT validator;
	// catches forged / unsigned id_tokens here instead of trusting them all
	// the way down to the first API call.
	idVerifier := provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID})

	return &OIDCHandler{
		config: cfg,
		oauth2Config: &oauth2.Config{
			ClientID:     cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret,
			RedirectURL:  cfg.OIDCRedirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  authURL,           // public — browser redirects here
				TokenURL: endpoint.TokenURL, // internal — backend POSTs here
			},
		},
		idVerifier: idVerifier,
		sc:         sc,
	}, nil
}

// discoverWithRetry rides out the post-startup network-identity window
// (Cilium et al.) where DNS / egress briefly returns EPERM. Same shape as the
// pre-refactor discoverOIDCWithRetry: 5 attempts, exponential backoff capped
// at 4 s.
func discoverWithRetry(ctx context.Context, issuer string) (*oidc.Provider, error) {
	delay := 500 * time.Millisecond
	var lastErr error
	for i := 1; i <= discoveryAttempts; i++ {
		attemptCtx, cancel := context.WithTimeout(ctx, discoveryTimeout)
		provider, err := oidc.NewProvider(attemptCtx, issuer)
		cancel()
		if err == nil {
			if i > 1 {
				logger.Info("OIDC discovery succeeded after retries", "issuer", issuer, "attempts", i)
			}
			return provider, nil
		}
		lastErr = err
		if i == discoveryAttempts {
			break
		}
		logger.Info("OIDC discovery attempt failed, retrying",
			"issuer", issuer, "attempt", i, "nextDelay", delay, "error", err.Error())
		time.Sleep(delay)
		if delay < 4*time.Second {
			delay *= 2
		}
	}
	return nil, lastErr
}

// extractBaseURL keeps only scheme + host (no path), so a public issuer with a
// trailing path component still substitutes cleanly.
func extractBaseURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
}

// generateState signs the full blob (returnURL, nonce, PKCE verifier, expiry)
// into a self-contained OAuth2 state parameter. The signing key is
// process-random — an attacker can't forge a valid blob, so the CSRF
// guarantee holds without any server-side bookkeeping.
func (h *OIDCHandler) generateState(returnURL, nonce, pkceVerifier string) (string, error) {
	return h.sc.Encode("dploy-state", stateBlob{
		ReturnURL:    returnURL,
		Nonce:        nonce,
		PKCEVerifier: pkceVerifier,
		Expiry:       time.Now().Add(stateTTL).Unix(),
	})
}

// consumeState verifies the signature, decodes the blob and checks the
// embedded expiry. Replay within the TTL is theoretically possible (no
// nonce store), but mitigated by the IdP's own one-time-code semantics
// and the short TTL — acceptable for the threat model.
func (h *OIDCHandler) consumeState(state string) (*stateBlob, bool) {
	var blob stateBlob
	if err := h.sc.Decode("dploy-state", state, &blob); err != nil {
		return nil, false
	}
	if time.Now().Unix() > blob.Expiry {
		return nil, false
	}
	return &blob, true
}

// Login initiates the Authorization Code flow with state + nonce + PKCE.
// Defense layering:
//   - state    — CSRF + carries the (signed) returnURL across the flow
//   - nonce    — binds the returned id_token to *this* login attempt
//   - PKCE S256 — proves at token-exchange time that we're the same client
//     that initiated the flow (defense against an intercepted auth code)
//
// returnURL is hardened against the protocol-relative open redirect
// (`//evil.com/x` would otherwise sail past a naive HasPrefix("/") check).
func (h *OIDCHandler) Login(c *fiber.Ctx) error {
	returnURL := c.Query("returnUrl", "/")
	if !safeRelativePath(returnURL) {
		logger.Warn("OIDC login: rejected unsafe returnUrl, defaulting to /", "returnUrl", returnURL)
		returnURL = "/"
	}
	nonce, err := randomToken()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to mint nonce"})
	}
	pkceVerifier := oauth2.GenerateVerifier()
	state, err := h.generateState(returnURL, nonce, pkceVerifier)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to mint state"})
	}
	authURL := h.oauth2Config.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(pkceVerifier),
	)
	logger.Debug("OIDC login redirect", "returnUrl", returnURL)
	return c.Redirect(authURL, fiber.StatusFound)
}

// randomToken returns a 32-byte URL-safe random string suitable for use as
// an OIDC nonce. crypto/rand is the source of truth — fall over loudly if
// the kernel can't provide entropy rather than degrade to a guessable value.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Callback exchanges the code for tokens and bounces the browser to the
// stashed returnURL with the id_token in the URL hash (client-side only —
// never appears in server logs).
func (h *OIDCHandler) Callback(c *fiber.Ctx) error {
	if errorParam := c.Query("error"); errorParam != "" {
		errorDesc := c.Query("error_description", errorParam)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": errorDesc})
	}
	code := c.Query("code")
	state := c.Query("state")
	if code == "" || state == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing code or state parameter"})
	}
	stateData, valid := h.consumeState(state)
	if !valid {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid or expired state parameter"})
	}

	// Exchange the code with the PKCE verifier — the IdP rejects the call if
	// it doesn't match the challenge we sent at Login.
	token, err := h.oauth2Config.Exchange(c.Context(), code, oauth2.VerifierOption(stateData.PKCEVerifier))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("failed to exchange code: %v", err),
		})
	}

	rawIDToken, _ := token.Extra("id_token").(string)
	if rawIDToken == "" {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "no id_token in OIDC response",
		})
	}

	// Verify the id_token server-side (signature, iss, aud, exp, nbf) before
	// handing it to the browser. Catches forged / unsigned id_tokens here
	// instead of trusting them as far as the next API call.
	idToken, err := h.idVerifier.Verify(c.Context(), rawIDToken)
	if err != nil {
		logger.Warn("OIDC callback: id_token verification failed", "error", err.Error())
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "id_token verification failed",
		})
	}
	// Replay defense: the nonce in the returned id_token must equal the one
	// we baked into the (signed) state. Without this, an attacker who steals
	// any valid id_token for our client_id can replay it through callback.
	if idToken.Nonce != stateData.Nonce {
		logger.Warn("OIDC callback: nonce mismatch — replay rejected")
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "nonce mismatch"})
	}
	// at_hash binding: when the IdP set it (Dex does for the code flow),
	// confirm the id_token we just verified was issued together with this
	// access_token. Defends against pairing a stolen access_token with a
	// forged id_token. Optional in the spec, so we skip — with a warn — when
	// the claim is absent.
	if idToken.AccessTokenHash != "" {
		if err := idToken.VerifyAccessToken(token.AccessToken); err != nil {
			logger.Warn("OIDC callback: at_hash mismatch — token-pairing rejected", "error", err.Error())
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "at_hash mismatch"})
		}
	} else {
		logger.Warn("OIDC callback: id_token has no at_hash, skipping access-token binding check")
	}

	returnURL := "/"
	if safeRelativePath(stateData.ReturnURL) {
		returnURL = stateData.ReturnURL
	}
	// Strip any fragment the user smuggled in via returnUrl (e.g. "/foo#section"):
	// the SPA's consumeHashToken() expects "#token=..." to be the only hash
	// on the final URL. Without this strip we'd produce "/foo#section#token=...",
	// which the SPA fails to parse.
	returnURL = stripFragment(returnURL)
	logger.Debug("OIDC callback complete", "returnUrl", returnURL, "tokenLength", len(rawIDToken))
	return c.Redirect(fmt.Sprintf("%s#token=%s", returnURL, rawIDToken), fiber.StatusFound)
}

// stripFragment returns the input URL without its #fragment, if any. Used at
// the callback to make sure the only hash on the final SPA redirect URL is
// the one carrying the token.
func stripFragment(s string) string {
	before, _, _ := strings.Cut(s, "#")
	return before
}

// Logout currently just bounces the browser home — the SPA clears its
// localStorage token on this redirect. End-session at the IdP is opt-in
// (the previous version didn't do it either; add an EndSessionEndpoint
// roundtrip here when an IdP demands SLO).
func (h *OIDCHandler) Logout(c *fiber.Ctx) error {
	return c.Redirect("/", fiber.StatusFound)
}
