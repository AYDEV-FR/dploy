package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type JWK struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type JWKS struct {
	Keys []JWK `json:"keys"`
}

type JWTValidator struct {
	jwksURL       string
	issuer        string
	audience      string
	usernameClaim string
	jwksCache     *JWKS
	cacheMu       sync.RWMutex
	lastFetch     time.Time
}

func NewJWTValidator(jwksURL, issuer, audience, usernameClaim string) *JWTValidator {
	return &JWTValidator{
		jwksURL:       jwksURL,
		issuer:        issuer,
		audience:      audience,
		usernameClaim: usernameClaim,
	}
}

func (v *JWTValidator) fetchJWKS() error {
	resp, err := http.Get(v.jwksURL)
	if err != nil {
		return fmt.Errorf("failed to fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	var jwks JWKS
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("failed to decode JWKS: %w", err)
	}

	v.cacheMu.Lock()
	v.jwksCache = &jwks
	v.lastFetch = time.Now()
	v.cacheMu.Unlock()

	return nil
}

func (v *JWTValidator) getJWKS() (*JWKS, error) {
	v.cacheMu.RLock()
	if v.jwksCache != nil && time.Since(v.lastFetch) < 15*time.Minute {
		jwks := v.jwksCache
		v.cacheMu.RUnlock()
		return jwks, nil
	}
	v.cacheMu.RUnlock()

	if err := v.fetchJWKS(); err != nil {
		return nil, err
	}

	v.cacheMu.RLock()
	jwks := v.jwksCache
	v.cacheMu.RUnlock()

	return jwks, nil
}

func (v *JWTValidator) ValidateToken(tokenString string) (string, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		// Check signing method
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("missing kid in token header")
		}

		jwks, err := v.getJWKS()
		if err != nil {
			return nil, fmt.Errorf("failed to get JWKS: %w", err)
		}

		var jwk *JWK
		for _, k := range jwks.Keys {
			if k.Kid == kid {
				jwk = &k
				break
			}
		}

		if jwk == nil {
			return nil, fmt.Errorf("key %s not found in JWKS (have %d keys)", kid, len(jwks.Keys))
		}

		// Convert JWK to RSA public key
		key, err := jwkToRSAPublicKey(jwk)
		if err != nil {
			return nil, fmt.Errorf("failed to convert JWK to RSA public key: %w", err)
		}

		return key, nil
	})

	if err != nil {
		return "", fmt.Errorf("token parsing failed: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", fmt.Errorf("invalid token claims")
	}

	// Validate issuer
	iss, ok := claims["iss"].(string)
	if !ok {
		return "", fmt.Errorf("missing iss claim")
	}
	if iss != v.issuer {
		return "", fmt.Errorf("invalid issuer: expected %s, got %s", v.issuer, iss)
	}

	// Validate audience
	aud, ok := claims["aud"].(string)
	if !ok {
		return "", fmt.Errorf("missing aud claim")
	}
	if aud != v.audience {
		return "", fmt.Errorf("invalid audience: expected %s, got %s", v.audience, aud)
	}

	// Extract username
	username, ok := claims[v.usernameClaim].(string)
	if !ok {
		return "", fmt.Errorf("missing or invalid %s claim", v.usernameClaim)
	}

	return SanitizeUsername(username), nil
}

// jwkToRSAPublicKey converts a JWK to an RSA public key
func jwkToRSAPublicKey(jwk *JWK) (*rsa.PublicKey, error) {
	// Decode the modulus (n) from base64url
	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("failed to decode modulus: %w", err)
	}

	// Decode the exponent (e) from base64url
	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("failed to decode exponent: %w", err)
	}

	// Convert to big integers
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)

	// Create RSA public key
	publicKey := &rsa.PublicKey{
		N: n,
		E: int(e.Int64()),
	}

	return publicKey, nil
}

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
