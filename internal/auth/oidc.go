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

// StateData holds OIDC state information for CSRF protection.
type StateData struct {
	Expiry    time.Time
	ReturnURL string
}

// OIDCHandler handles OIDC authentication flow.
type OIDCHandler struct {
	config      *config.Config
	states      map[string]*StateData
	statesMutex sync.RWMutex
	tokenURL    string
	authURL     string
	userInfoURL string
}

// OIDCTokenResponse represents the token response from the OIDC provider.
type OIDCTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token"`
}

// NewOIDCHandler creates a new OIDC handler.
func NewOIDCHandler(cfg *config.Config) (*OIDCHandler, error) {
	// Always use manual construction with internal issuer for backend calls.
	// This avoids issues with OIDC discovery returning public URLs.
	tokenURL := fmt.Sprintf("%s/token", cfg.OIDCIssuer)
	authURL := fmt.Sprintf("%s/auth", cfg.OIDCIssuer)
	userInfoURL := fmt.Sprintf("%s/userinfo", cfg.OIDCIssuer)

	// If public issuer is different, use it for browser redirects (auth URL).
	if cfg.OIDCPublicIssuer != "" && cfg.OIDCPublicIssuer != cfg.OIDCIssuer {
		authURL = fmt.Sprintf("%s/auth", cfg.OIDCPublicIssuer)
	}

	handler := &OIDCHandler{
		config:      cfg,
		states:      make(map[string]*StateData),
		tokenURL:    tokenURL,
		authURL:     authURL,
		userInfoURL: userInfoURL,
	}

	// Start cleanup goroutine for expired states.
	go handler.cleanupStates()

	return handler, nil
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

	authURL := fmt.Sprintf("%s?%s", h.authURL, params.Encode())
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
		h.tokenURL,
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
