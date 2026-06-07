// Package auth handles JWT verification and the OIDC login flow for the dploy
// API.
//
// JWT verification (jwt.go) goes through the canonical go-oidc verifier; the
// OIDC login/callback/logout flow (this file) goes through zitadel/oidc's
// RelyingParty, which already handles state cookie, PKCE S256, nonce,
// at_hash, id_token signature/iss/aud/exp/nbf — all the gotchas that used to
// be hand-rolled here. What's left is dploy-specific glue: split-horizon
// AuthURL substitution, returnUrl sanitisation, retry-on-discovery at boot,
// the Fiber adapter, and the final "#token=..." hand-off the SPA expects.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AYDEV-FR/dploy/internal/config"
	"github.com/AYDEV-FR/dploy/internal/logger"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/gorilla/securecookie"
	"github.com/zitadel/oidc/v3/pkg/client/rp"
	httphelper "github.com/zitadel/oidc/v3/pkg/http"
	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// OIDCHandler is a thin wrapper around zitadel/oidc's RelyingParty plus the
// dploy-specific Fiber handlers.
type OIDCHandler struct {
	rp rp.RelyingParty
}

const (
	discoveryTimeout  = 10 * time.Second
	discoveryAttempts = 5
)

// NewOIDCHandler wires zitadel/oidc's RelyingParty (which handles state
// cookie, PKCE, nonce, at_hash + id_token verification) and substitutes the
// AuthorizationEndpoint with its public-issuer equivalent so browser redirects
// land on the user-facing IdP URL while backend code exchange + JWKS stay
// in-cluster.
func NewOIDCHandler(cfg *config.Config) (*OIDCHandler, error) {
	// Cookie security flag mirrors the redirect URL's scheme — HTTP for the
	// dev/CTF cluster, HTTPS for production.
	cookieOpts := []httphelper.CookieHandlerOpt{}
	if strings.HasPrefix(cfg.OIDCRedirectURL, "http://") {
		cookieOpts = append(cookieOpts, httphelper.WithUnsecure())
	}
	// Cookie keys: prefer env-provided secrets so logins survive pod restarts
	// and load-balance across replicas. Falling back to process-random is fine
	// for single-replica dev but logs a loud warning.
	hashKey := []byte(cfg.OIDCCookieHashKey)
	blockKey := []byte(cfg.OIDCCookieBlockKey)
	if len(hashKey) == 0 {
		hashKey = securecookie.GenerateRandomKey(64)
		logger.Warn("OIDC_COOKIE_HASH_KEY not set; using a process-random key — logins will break across pod restarts or replicas")
	}
	if len(blockKey) == 0 {
		blockKey = securecookie.GenerateRandomKey(32)
	}
	cookieHandler := httphelper.NewCookieHandler(hashKey, blockKey, cookieOpts...)

	// Split-horizon: tokens carry the public issuer URL (Dex's configured
	// issuer is fixed regardless of request host), but we discover and call
	// the IdP through the in-cluster URL. zitadel/oidc validates that the
	// discovery doc's `issuer` matches the arg we pass, so we pass the
	// public one and override the fetch URL via WithCustomDiscoveryUrl.
	expectedIssuer := cfg.OIDCIssuer
	internalDiscoveryURL := strings.TrimSuffix(cfg.OIDCIssuer, "/") + "/.well-known/openid-configuration"
	opts := []rp.Option{
		rp.WithCookieHandler(cookieHandler),
		rp.WithPKCE(cookieHandler),
	}
	if cfg.OIDCPublicIssuer != "" && cfg.OIDCPublicIssuer != cfg.OIDCIssuer {
		expectedIssuer = cfg.OIDCPublicIssuer
		opts = append(opts, rp.WithCustomDiscoveryUrl(internalDiscoveryURL))
	}

	relyingParty, err := newRelyingPartyWithRetry(context.Background(),
		expectedIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret, cfg.OIDCRedirectURL,
		[]string{oidc.ScopeOpenID, "email", "profile"},
		opts...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build OIDC RelyingParty: %w", err)
	}

	// Endpoints from discovery are derived by Dex from the request Host
	// header — fetching discovery via the in-cluster URL gives us internal
	// endpoint URLs. Backend code exchange + JWKS stay internal; only the
	// AuthorizationEndpoint must be rebased to public so browsers land on
	// the user-facing IdP URL.
	if cfg.OIDCPublicIssuer != "" && cfg.OIDCPublicIssuer != cfg.OIDCIssuer {
		internalBase := extractBaseURL(cfg.OIDCIssuer)
		publicBase := extractBaseURL(cfg.OIDCPublicIssuer)
		ep := &relyingParty.OAuthConfig().Endpoint
		ep.AuthURL = strings.Replace(ep.AuthURL, internalBase, publicBase, 1)
		logger.Info("OIDC auth endpoint rebased to public",
			"internal", internalBase, "public", publicBase, "authURL", ep.AuthURL)
	}

	logger.Info("OIDC handler initialized", "expectedIssuer", expectedIssuer)
	return &OIDCHandler{rp: relyingParty}, nil
}

// newRelyingPartyWithRetry rides out the post-startup network-identity window
// (Cilium et al.) where DNS / egress briefly returns EPERM. Same shape as the
// previous discoverWithRetry: 5 attempts, exponential backoff capped at 4 s.
func newRelyingPartyWithRetry(ctx context.Context, issuer, clientID, clientSecret, redirectURI string, scopes []string, options ...rp.Option) (rp.RelyingParty, error) {
	delay := 500 * time.Millisecond
	var lastErr error
	for i := 1; i <= discoveryAttempts; i++ {
		attemptCtx, cancel := context.WithTimeout(ctx, discoveryTimeout)
		party, err := rp.NewRelyingPartyOIDC(attemptCtx, issuer, clientID, clientSecret, redirectURI, scopes, options...)
		cancel()
		if err == nil {
			if i > 1 {
				logger.Info("OIDC discovery succeeded after retries", "attempts", i)
			}
			return party, nil
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
	u.Fragment = ""
	return u.String(), true
}

// Login wraps zitadel/oidc's AuthURLHandler: it signs+encrypts the state into
// a cookie, hands it to the IdP as ?state=…, and gives it back to us after
// verifying. The state value is "<nonce>:<returnUrl>" — the nonce makes the
// state unguessable (defeats CSRF / session-fixation: an attacker who
// triggers /auth/login on a victim browser still can't predict the value
// stored in the cookie), while returnUrl piggybacks for free.
func (h *OIDCHandler) Login(c *fiber.Ctx) error {
	rawReturn := c.Query("returnUrl", "/")
	returnURL := "/"
	if clean, ok := sanitizeRelativePath(rawReturn); ok {
		returnURL = clean
	} else {
		logger.Warn("OIDC login: rejected unsafe returnUrl, defaulting to /", "returnUrl", rawReturn)
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to mint state nonce"})
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	stateFn := func() string { return nonce + ":" + returnURL }
	return adaptor.HTTPHandler(rp.AuthURLHandler(stateFn, h.rp))(c)
}

// Callback wraps zitadel/oidc's CodeExchangeHandler. The library does state
// cookie verification, PKCE-aware code exchange, and full id_token
// verification (signature, iss, aud, exp, nbf, nonce, at_hash). All we add is
// a defense-in-depth re-sanitisation of state-as-returnUrl and the SPA's
// hash-fragment token hand-off.
func (h *OIDCHandler) Callback(c *fiber.Ctx) error {
	return adaptor.HTTPHandler(rp.CodeExchangeHandler(h.exchangeCallback, h.rp))(c)
}

func (h *OIDCHandler) exchangeCallback(w http.ResponseWriter, r *http.Request, tokens *oidc.Tokens[*oidc.IDTokenClaims], state string, _ rp.RelyingParty) {
	// state format set by Login: "<nonce>:<returnUrl>". The nonce is what
	// zitadel/oidc already validated (cookie state == query state); we just
	// need to recover the returnUrl. Anything else → default to "/".
	returnURL := "/"
	if _, urlPart, ok := strings.Cut(state, ":"); ok {
		if clean, ok := sanitizeRelativePath(urlPart); ok {
			returnURL = clean
		}
	}
	logger.Debug("OIDC callback complete", "returnUrl", returnURL, "tokenLength", len(tokens.IDToken))
	http.Redirect(w, r, fmt.Sprintf("%s#token=%s", returnURL, tokens.IDToken), http.StatusFound)
}

// Logout bounces the browser home — the SPA clears its localStorage token on
// the redirect. End-session at the IdP (RP-initiated logout) would be an
// rp.EndSessionEndpoint roundtrip; add it here when an IdP requires SLO.
func (h *OIDCHandler) Logout(c *fiber.Ctx) error {
	return c.Redirect("/", fiber.StatusFound)
}
