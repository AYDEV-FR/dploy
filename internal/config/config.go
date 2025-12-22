package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/AYDEV-FR/dploy/internal/models"
	"gopkg.in/yaml.v3"
)

type Config struct {
	// JWT
	JWKSUrl          string
	JWTIssuer        string
	JWTAudience      string
	JWTUsernameClaim string

	// OAuth2/OIDC
	OIDCIssuer       string // Internal issuer (for backend API calls)
	OIDCPublicIssuer string // Public issuer (for browser redirects)
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string

	// Kubernetes
	ArgoCDNamespace string
	ArgoCDProject   string

	// Defaults
	MaxEnvironmentsPerUser int
	DefaultTTL             int // TTL in seconds
	ExtendTTL              int // TTL extension in seconds
	CleanupInterval        int // Cleanup check interval in seconds

	// Ingress
	BaseDomain string

	// Server
	ServerHost string
	ServerPort string

	// Environments
	Environments []models.Environment
}

func Load(environmentsPath string) (*Config, error) {
	cfg := &Config{
		// JWT
		JWKSUrl:          getEnv("JWKS_URL", ""),
		JWTIssuer:        getEnv("JWT_ISSUER", ""),
		JWTAudience:      getEnv("JWT_AUDIENCE", "dploy"),
		JWTUsernameClaim: getEnv("JWT_USERNAME_CLAIM", "name"),

		// OAuth2/OIDC
		OIDCIssuer:       getEnv("OIDC_ISSUER", getEnv("JWT_ISSUER", "")),
		OIDCPublicIssuer: getEnv("OIDC_PUBLIC_ISSUER", getEnv("OIDC_ISSUER", "")),
		OIDCClientID:     getEnv("OIDC_CLIENT_ID", "dploy"),
		OIDCClientSecret: getEnv("OIDC_CLIENT_SECRET", "dploy-secret"),
		OIDCRedirectURL:  getEnv("OIDC_REDIRECT_URL", "http://localhost:8080/auth/callback"),

		// Kubernetes
		ArgoCDNamespace: getEnv("ARGOCD_NAMESPACE", "argocd"),
		ArgoCDProject:   getEnv("ARGOCD_PROJECT", "dploy"),

		// Defaults
		MaxEnvironmentsPerUser: getEnvAsInt("MAX_ENVIRONMENTS_PER_USER", 5),
		DefaultTTL:             getEnvAsInt("DEFAULT_TTL", 86400),      // 24 hours in seconds
		ExtendTTL:              getEnvAsInt("EXTEND_TTL", 7200),        // 2 hours in seconds
		CleanupInterval:        getEnvAsInt("CLEANUP_INTERVAL", 60),    // 1 minute in seconds

		// Ingress
		BaseDomain: getEnv("BASE_DOMAIN", "env.dploy.dev"),

		// Server
		ServerHost: getEnv("SERVER_HOST", "0.0.0.0"),
		ServerPort: getEnv("SERVER_PORT", "8080"),
	}

	if cfg.JWKSUrl == "" {
		return nil, fmt.Errorf("JWKS_URL is required")
	}
	if cfg.JWTIssuer == "" {
		return nil, fmt.Errorf("JWT_ISSUER is required")
	}

	// Load environments from YAML file
	data, err := os.ReadFile(environmentsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read environments file: %w", err)
	}

	var envConfig models.EnvironmentsConfig
	if err := yaml.Unmarshal(data, &envConfig); err != nil {
		return nil, fmt.Errorf("failed to parse environments file: %w", err)
	}

	cfg.Environments = envConfig.Environments

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
