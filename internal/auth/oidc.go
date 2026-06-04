package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/AYDEV-FR/dploy/internal/config"
	"github.com/AYDEV-FR/dploy/internal/logger"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gofiber/fiber/v2"
	"golang.org/x/oauth2"
)

// StateData holds a one-time state token's expiry + the URL to redirect back
// to after a successful callback. In-memory map keyed by state; lost on pod
// restart (in-flight logins fail back to /auth/login).
type StateData struct {
	Expiry    time.Time
	ReturnURL string
}

// OIDCHandler runs the OAuth2 / OIDC Authorization Code flow with the
// configured IdP. Heavy lifting is delegated to golang.org/x/oauth2 (Code
// exchange, AuthCodeURL) and github.com/coreos/go-oidc (discovery). The only
// dploy-specific bits are split-horizon endpoint handling, a one-shot state
// map for CSRF protection, and the post-callback hash-fragment redirect.
type OIDCHandler struct {
	config       *config.Config
	oauth2Config *oauth2.Config

	statesMu sync.RWMutex
	states   map[string]*StateData
}

const (
	stateTTL          = 10 * time.Minute
	stateCleanupEvery = 5 * time.Minute
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

	h := &OIDCHandler{
		config: cfg,
		oauth2Config: &oauth2.Config{
			ClientID:     cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret,
			RedirectURL:  cfg.OIDCRedirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  authURL,            // public — browser redirects here
				TokenURL: endpoint.TokenURL,  // internal — backend POSTs here
			},
		},
		states: make(map[string]*StateData),
	}
	go h.cleanupStates()
	return h, nil
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

// generateState mints a fresh 32-byte CSRF token and stashes the returnURL
// alongside it for the callback to pick up.
func (h *OIDCHandler) generateState(returnURL string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is exceedingly rare; fall back to a time-based
		// state rather than panicking. Lower entropy but still single-use.
		b = []byte(fmt.Sprintf("dploy-state-%d", time.Now().UnixNano()))
	}
	state := base64.URLEncoding.EncodeToString(b)
	h.statesMu.Lock()
	h.states[state] = &StateData{Expiry: time.Now().Add(stateTTL), ReturnURL: returnURL}
	h.statesMu.Unlock()
	return state
}

// consumeState looks up + deletes the state in one critical section.
// Returns (data, true) on a valid first-use, (nil, false) on miss or expiry.
func (h *OIDCHandler) consumeState(state string) (*StateData, bool) {
	h.statesMu.Lock()
	defer h.statesMu.Unlock()
	data, ok := h.states[state]
	if !ok {
		return nil, false
	}
	delete(h.states, state)
	if time.Now().After(data.Expiry) {
		return nil, false
	}
	return data, true
}

func (h *OIDCHandler) cleanupStates() {
	ticker := time.NewTicker(stateCleanupEvery)
	defer ticker.Stop()
	for range ticker.C {
		h.statesMu.Lock()
		now := time.Now()
		for state, data := range h.states {
			if now.After(data.Expiry) {
				delete(h.states, state)
			}
		}
		h.statesMu.Unlock()
	}
}

// Login initiates the Authorization Code flow.
func (h *OIDCHandler) Login(c *fiber.Ctx) error {
	returnURL := c.Query("returnUrl", "/")
	// Open-redirect guard: only relative paths are accepted.
	if !strings.HasPrefix(returnURL, "/") {
		logger.Warn("OIDC login: invalid returnUrl, defaulting to /", "returnUrl", returnURL)
		returnURL = "/"
	}
	state := h.generateState(returnURL)
	logger.Debug("OIDC login redirect", "returnUrl", returnURL)
	return c.Redirect(h.oauth2Config.AuthCodeURL(state), fiber.StatusFound)
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

	token, err := h.oauth2Config.Exchange(c.Context(), code)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("failed to exchange code: %v", err),
		})
	}

	// OIDC flows put the id_token in Token.Extra; OAuth2-only IdPs only return
	// an access_token. We prefer the id_token (it's what the JWT validator
	// downstream expects), fall back to the access_token otherwise.
	tokenToUse, _ := token.Extra("id_token").(string)
	if tokenToUse == "" {
		tokenToUse = token.AccessToken
	}
	if tokenToUse == "" {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "no token received from OIDC provider",
		})
	}

	returnURL := "/"
	if stateData != nil && strings.HasPrefix(stateData.ReturnURL, "/") {
		returnURL = stateData.ReturnURL
	}
	logger.Debug("OIDC callback complete", "returnUrl", returnURL, "tokenLength", len(tokenToUse))
	return c.Redirect(fmt.Sprintf("%s#token=%s", returnURL, tokenToUse), fiber.StatusFound)
}

// Logout currently just bounces the browser home — the SPA clears its
// localStorage token on this redirect. End-session at the IdP is opt-in
// (the previous version didn't do it either; add an EndSessionEndpoint
// roundtrip here when an IdP demands SLO).
func (h *OIDCHandler) Logout(c *fiber.Ctx) error {
	return c.Redirect("/", fiber.StatusFound)
}
