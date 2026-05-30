package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	// JWT
	JWKSUrl          string
	JWTIssuer        string
	JWTAudience      string
	JWTUsernameClaim string

	// OIDC
	OIDCIssuer       string // Internal issuer (for backend API calls)
	OIDCPublicIssuer string // Public issuer (for browser redirects)
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string

	// Kubernetes: the namespace where DployTemplate and DployInstance CRs live.
	Namespace string

	// Defaults — fallbacks used only when a DployTemplate omits the value.
	MaxEnvironmentsPerUser int
	DefaultTTL             int // initial TTL in seconds
	ExtendTTL              int // TTL extension in seconds

	// Server
	ServerHost string
	ServerPort string

	// UI feature flags. When false, the corresponding nav link is hidden in the
	// web UI AND the matching API endpoint is not registered (returns 404).
	// Disabling both yields a run-only deployment: login + /run/:env still work.
	CatalogEnabled       bool
	InstancesListEnabled bool

	// Debug
	Debug bool
}

// Load reads configuration from the environment. The catalog and instance state
// now live in Kubernetes (DployTemplate/DployInstance CRs), so there is no longer
// an environments file to parse.
func Load() (*Config, error) {
	cfg := &Config{
		// JWT
		JWKSUrl:          getEnv("JWKS_URL", ""),
		JWTIssuer:        getEnv("JWT_ISSUER", ""),
		JWTAudience:      getEnv("JWT_AUDIENCE", "dploy"),
		JWTUsernameClaim: getEnv("JWT_USERNAME_CLAIM", "name"),

		// OIDC
		OIDCIssuer:       getEnv("OIDC_ISSUER", getEnv("JWT_ISSUER", "")),
		OIDCPublicIssuer: getEnv("OIDC_PUBLIC_ISSUER", getEnv("OIDC_ISSUER", "")),
		OIDCClientID:     getEnv("OIDC_CLIENT_ID", "dploy"),
		OIDCClientSecret: getEnv("OIDC_CLIENT_SECRET", "dploy-secret"),
		OIDCRedirectURL:  getEnv("OIDC_REDIRECT_URL", "http://localhost:8080/auth/callback"),

		// Kubernetes
		Namespace: getEnv("DPLOY_NAMESPACE", "dploy-system"),

		// Defaults
		MaxEnvironmentsPerUser: getEnvAsInt("MAX_ENVIRONMENTS_PER_USER", 5),
		DefaultTTL:             getEnvAsInt("DEFAULT_TTL", 86400), // 24h
		ExtendTTL:              getEnvAsInt("EXTEND_TTL", 7200),   // 2h

		// Server
		ServerHost: getEnv("SERVER_HOST", "0.0.0.0"),
		ServerPort: getEnv("SERVER_PORT", "8080"),

		// UI feature flags
		CatalogEnabled:       getEnvAsBool("CATALOG_ENABLED", true),
		InstancesListEnabled: getEnvAsBool("INSTANCES_LIST_ENABLED", true),

		// Debug
		Debug: getEnvAsBool("DEBUG", false),
	}

	if cfg.JWKSUrl == "" {
		return nil, fmt.Errorf("JWKS_URL is required")
	}
	if cfg.JWTIssuer == "" {
		return nil, fmt.Errorf("JWT_ISSUER is required")
	}

	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

func getEnvAsBool(key string, defaultValue bool) bool {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := strconv.ParseBool(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}
