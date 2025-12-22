package models

import "strings"

type Environment struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Chart       string `yaml:"chart" json:"chart"`                                 // Format: "charts/webterm@main"
	ExtraValues string `yaml:"extraValues,omitempty" json:"extraValues,omitempty"` // Additional Helm values (supports ${username}, ${uuid}, ${ingressHost})
	Enabled     bool   `yaml:"enabled" json:"enabled"`
	Icon        string `yaml:"icon" json:"icon"`
	TTL         *int   `yaml:"ttl,omitempty" json:"ttl,omitempty"` // TTL in seconds
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
