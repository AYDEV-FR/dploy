package models

import (
	"strconv"
	"strings"
)

type Environment struct {
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description" json:"description"`
	Chart       string   `yaml:"chart" json:"chart"`                                 // Format: "charts/webterm@main"
	ExtraValues string   `yaml:"extraValues,omitempty" json:"extraValues,omitempty"` // Additional Helm values (supports ${username}, ${uuid}, ${ingressHost})
	ValueFiles  []string `yaml:"valueFiles,omitempty" json:"valueFiles,omitempty"`   // List of values files paths in the chart repository
	Enabled     bool     `yaml:"enabled" json:"enabled"`
	Visible     *bool    `yaml:"visible,omitempty" json:"visible,omitempty"` // Visible in UI/API listing (default: true). Hidden environments are still accessible via /run/{env}
	Icon        string   `yaml:"icon" json:"icon"`
	TTL         string   `yaml:"ttl,omitempty" json:"ttl,omitempty"`           // TTL format: "seconds" or "seconds,extendSeconds,maxExtends". Use -1 for unlimited.
	Category    string   `yaml:"category,omitempty" json:"category,omitempty"` // Category in format "category,subcategory" (e.g., "learning,linux")
}

// TTLConfig holds parsed TTL configuration.
type TTLConfig struct {
	TTL        int  // Initial TTL in seconds (-1 for unlimited)
	ExtendTTL  int  // TTL extension in seconds (0 = use default)
	MaxExtends int  // Maximum number of extensions (-1 = unlimited, 0 = use default)
	HasExtend  bool // Whether ExtendTTL was explicitly set
	HasMax     bool // Whether MaxExtends was explicitly set
}

// ParseTTL parses the TTL string and returns TTL configuration.
// Format: "seconds" or "seconds,extendSeconds" or "seconds,extendSeconds,maxExtends"
// Returns nil if TTL is not set (use defaults).
func (e *Environment) ParseTTL() *TTLConfig {
	if e.TTL == "" {
		return nil
	}

	parts := strings.Split(e.TTL, ",")
	config := &TTLConfig{
		TTL:        0,
		ExtendTTL:  0,
		MaxExtends: 0,
		HasExtend:  false,
		HasMax:     false,
	}

	// Parse TTL (required)
	if len(parts) >= 1 {
		if ttl, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil {
			config.TTL = ttl
		}
	}

	// Parse ExtendTTL (optional)
	if len(parts) >= 2 {
		if extendTTL, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
			config.ExtendTTL = extendTTL
			config.HasExtend = true
		}
	}

	// Parse MaxExtends (optional)
	if len(parts) >= 3 {
		if maxExtends, err := strconv.Atoi(strings.TrimSpace(parts[2])); err == nil {
			config.MaxExtends = maxExtends
			config.HasMax = true
		}
	}

	return config
}

// IsUnlimited returns true if TTL is set to unlimited (-1).
func (c *TTLConfig) IsUnlimited() bool {
	return c != nil && c.TTL == -1
}

// IsVisible returns whether the environment should be shown in listings.
// Defaults to true if not specified.
func (e *Environment) IsVisible() bool {
	if e.Visible == nil {
		return true
	}
	return *e.Visible
}

// ParseChart parses the chart string in format "github.com/org/repo/path/to/chart@revision".
// Returns (repoURL, path, revision).
func (e *Environment) ParseChart() (repoURL, chartPath, revision string) {
	// Split by @ to separate path from revision
	parts := strings.Split(e.Chart, "@")
	revision = "main"
	fullPath := e.Chart

	if len(parts) == 2 {
		fullPath = parts[0]
		revision = parts[1]
	}

	// Parse the full path to extract repo and chart path
	// Format: github.com/org/repo/charts/webterm or github.com/org/repo/webterm
	pathParts := strings.Split(fullPath, "/")

	if len(pathParts) < 4 {
		// Invalid format, return as-is
		repoURL = "https://" + fullPath
		return repoURL, "", revision
	}

	// Extract repo URL (first 3 parts: github.com/org/repo)
	repoURL = "https://" + strings.Join(pathParts[:3], "/")

	// Extract chart path (everything after the repo)
	chartPath = strings.Join(pathParts[3:], "/")

	return repoURL, chartPath, revision
}

type EnvironmentsConfig struct {
	Environments []Environment `yaml:"environments"`
}
