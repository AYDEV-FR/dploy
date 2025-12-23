package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/AYDEV-FR/dploy/internal/config"
	"github.com/gofiber/fiber/v2"
)

// OIDCDiscovery represents the OIDC discovery document.
type OIDCDiscovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
	JwksURI               string `json:"jwks_uri"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

// StateData holds OIDC state information for CSRF protection.
type StateData struct {
	Expiry    time.Time
	ReturnURL string
}

// OIDCHandler handles OIDC authentication flow.
type OIDCHandler struct {
	config          *config.Config
	states          map[string]*StateData
	statesMutex     sync.RWMutex
	discovery       *OIDCDiscovery
	publicDiscovery *OIDCDiscovery // For browser redirects (may use different URLs)
}

// OIDCTokenResponse represents the token response from the OIDC provider.
type OIDCTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token"`
}

// NewOIDCHandler creates a new OIDC handler using OIDC discovery.
func NewOIDCHandler(cfg *config.Config) (*OIDCHandler, error) {
	handler := &OIDCHandler{
		config: cfg,
		states: make(map[string]*StateData),
	}

	// Discover OIDC endpoints from internal issuer (for backend calls).
	discovery, err := handler.discoverOIDC(cfg.OIDCIssuer)
	if err != nil {
		return nil, fmt.Errorf("failed to discover OIDC endpoints from %s: %w", cfg.OIDCIssuer, err)
	}
	handler.discovery = discovery
	fmt.Printf("OIDC Discovery (internal): token=%s\n", discovery.TokenEndpoint)

	// If public issuer is different, create public discovery by replacing the server base URL.
	// This handles cases where the pod can't access the public URL directly.
	if cfg.OIDCPublicIssuer != "" && cfg.OIDCPublicIssuer != cfg.OIDCIssuer {
		// Try to discover from public issuer first.
		publicDiscovery, err := handler.discoverOIDC(cfg.OIDCPublicIssuer)
		if err != nil {
			// Extract base URLs (scheme + host) for replacement.
			internalBase := extractBaseURL(cfg.OIDCIssuer)
			publicBase := extractBaseURL(cfg.OIDCPublicIssuer)

			// Construct public URLs by replacing internal base with public base.
			fmt.Printf("Warning: failed to discover OIDC from public issuer %s: %v\n", cfg.OIDCPublicIssuer, err)
			fmt.Printf("Constructing public URLs from internal discovery (replacing %s with %s)...\n", internalBase, publicBase)
			handler.publicDiscovery = &OIDCDiscovery{
				Issuer:                strings.Replace(discovery.Issuer, internalBase, publicBase, 1),
				AuthorizationEndpoint: strings.Replace(discovery.AuthorizationEndpoint, internalBase, publicBase, 1),
				TokenEndpoint:         strings.Replace(discovery.TokenEndpoint, internalBase, publicBase, 1),
				UserInfoEndpoint:      strings.Replace(discovery.UserInfoEndpoint, internalBase, publicBase, 1),
				JwksURI:               strings.Replace(discovery.JwksURI, internalBase, publicBase, 1),
				EndSessionEndpoint:    strings.Replace(discovery.EndSessionEndpoint, internalBase, publicBase, 1),
			}
			fmt.Printf("OIDC Discovery (public, constructed): auth=%s\n", handler.publicDiscovery.AuthorizationEndpoint)
		} else {
			handler.publicDiscovery = publicDiscovery
			fmt.Printf("OIDC Discovery (public): auth=%s\n", publicDiscovery.AuthorizationEndpoint)
		}
	} else {
		handler.publicDiscovery = discovery
	}

	// Start cleanup goroutine for expired states.
	go handler.cleanupStates()

	return handler, nil
}

// extractBaseURL extracts the scheme and host from a URL (e.g., "http://example.com/path" -> "http://example.com").
func extractBaseURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
}

// discoverOIDC fetches the OIDC discovery document from the issuer.
func (h *OIDCHandler) discoverOIDC(issuer string) (*OIDCDiscovery, error) {
	// Standard OIDC discovery URL.
	discoveryURL := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", discoveryURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery endpoint returned status %d", resp.StatusCode)
	}

	var discovery OIDCDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		return nil, fmt.Errorf("failed to decode discovery document: %w", err)
	}

	return &discovery, nil
}

func (h *OIDCHandler) cleanupStates() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		h.statesMutex.Lock()
		now := time.Now()
		for state, data := range h.states {
			if now.After(data.Expiry) {
				delete(h.states, state)
			}
		}
		h.statesMutex.Unlock()
	}
}

func (h *OIDCHandler) generateState(returnURL string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based state if crypto/rand fails.
		b = []byte(fmt.Sprintf("%d", time.Now().UnixNano()))
	}
	state := base64.URLEncoding.EncodeToString(b)

	h.statesMutex.Lock()
	h.states[state] = &StateData{
		Expiry:    time.Now().Add(10 * time.Minute),
		ReturnURL: returnURL,
	}
	h.statesMutex.Unlock()

	return state
}

func (h *OIDCHandler) validateState(state string) (*StateData, bool) {
	h.statesMutex.RLock()
	data, exists := h.states[state]
	h.statesMutex.RUnlock()

	if !exists {
		return nil, false
	}

	if time.Now().After(data.Expiry) {
		h.statesMutex.Lock()
		delete(h.states, state)
		h.statesMutex.Unlock()
		return nil, false
	}

	// Remove state after validation (one-time use).
	h.statesMutex.Lock()
	delete(h.states, state)
	h.statesMutex.Unlock()

	return data, true
}

// Login initiates the OIDC Authorization Code flow.
func (h *OIDCHandler) Login(c *fiber.Ctx) error {
	// Get returnUrl from query parameter (optional).
	returnURL := c.Query("returnUrl", "/")

	state := h.generateState(returnURL)

	params := url.Values{}
	params.Set("client_id", h.config.OIDCClientID)
	params.Set("redirect_uri", h.config.OIDCRedirectURL)
	params.Set("response_type", "code")
	params.Set("scope", "openid email profile")
	params.Set("state", state)

	// Use public discovery for browser redirects.
	authURL := fmt.Sprintf("%s?%s", h.publicDiscovery.AuthorizationEndpoint, params.Encode())
	return c.Redirect(authURL, fiber.StatusFound)
}

// Callback handles the OIDC callback after user authentication.
func (h *OIDCHandler) Callback(c *fiber.Ctx) error {
	code := c.Query("code")
	state := c.Query("state")
	errorParam := c.Query("error")

	if errorParam != "" {
		errorDesc := c.Query("error_description", errorParam)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": errorDesc,
		})
	}

	if code == "" || state == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "missing code or state parameter",
		})
	}

	stateData, valid := h.validateState(state)
	if !valid {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid or expired state parameter",
		})
	}

	// Exchange code for token.
	token, err := h.exchangeCode(code)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("failed to exchange code: %v", err),
		})
	}

	// Use ID token if available (OIDC), otherwise fall back to access token.
	tokenToUse := token.IDToken
	if tokenToUse == "" {
		tokenToUse = token.AccessToken
	}

	if tokenToUse == "" {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "no token received from OIDC provider",
		})
	}

	// Get return URL from state (defaults to "/" if not specified).
	returnURL := "/"
	if stateData != nil && stateData.ReturnURL != "" {
		returnURL = stateData.ReturnURL
	}

	// Redirect with token in hash fragment (client-side only, not sent to server).
	// This is more secure than query params and prevents token leakage in server logs.
	redirectURL := fmt.Sprintf("%s#token=%s", returnURL, tokenToUse)

	fmt.Printf("OIDC callback: redirecting to %s with token in hash (length: %d)\n", returnURL, len(tokenToUse))

	return c.Redirect(redirectURL, fiber.StatusFound)
}

// Logout redirects to home (token is cleared client-side).
func (h *OIDCHandler) Logout(c *fiber.Ctx) error {
	return c.Redirect("/", fiber.StatusFound)
}

func (h *OIDCHandler) exchangeCode(code string) (*OIDCTokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", h.config.OIDCRedirectURL)
	data.Set("client_id", h.config.OIDCClientID)
	data.Set("client_secret", h.config.OIDCClientSecret)

	req, err := http.NewRequestWithContext(
		context.Background(),
		"POST",
		h.discovery.TokenEndpoint, // Use internal discovery for backend calls.
		strings.NewReader(data.Encode()),
	)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Try to read error details.
		var errorBody map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&errorBody); err != nil {
			return nil, fmt.Errorf("token endpoint returned status %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("token endpoint returned status %d: %v", resp.StatusCode, errorBody)
	}

	var tokenResp OIDCTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	return &tokenResp, nil
}
