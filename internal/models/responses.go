package models

type AvailableEnvironmentResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Category    string `json:"category,omitempty"`
	// TTL info
	TTL         int  `json:"ttl"`                   // Initial TTL in seconds (-1 for unlimited)
	ExtendTTL   int  `json:"extendTTL,omitempty"`   // Seconds added per extension (0 = use default)
	MaxExtends  int  `json:"maxExtends,omitempty"`  // Max extensions allowed (0 = unlimited)
	IsUnlimited bool `json:"isUnlimited"`           // True if TTL is unlimited
}

type UserEnvironmentResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	UUID        string `json:"uuid"`
	Status      string `json:"status"`
	URL         string `json:"url"`
	ExpiresAt   string `json:"expiresAt,omitempty"` // Empty for unlimited TTL
	Icon        string `json:"icon"`
	// TTL extension info
	ExtendCount int  `json:"extendCount"`          // Number of times extended
	MaxExtends  int  `json:"maxExtends,omitempty"` // Max extensions allowed (-1 = unlimited, 0 = not set)
	ExtendTTL   int  `json:"extendTTL,omitempty"`  // Seconds added per extension (0 = use default)
	IsUnlimited bool `json:"isUnlimited"`          // True if TTL is unlimited

	Owner  string `json:"owner,omitempty"`  // Resolved owner key (username, group, …)
	Shared bool   `json:"shared,omitempty"` // True when owned by someone other than the requester (team-shared)

	ConnectionType    string `json:"connectionType,omitempty"`    // "web" (default) or "instructions"
	ConnectionMessage string `json:"connectionMessage,omitempty"` // Copyable command when type is "instructions"
}

type UserEnvironmentsListResponse struct {
	Environments []UserEnvironmentResponse `json:"environments"`
	Count        int                       `json:"count"`
	Limit        int                       `json:"limit"`
}

type RunEnvironmentResponse struct {
	UUID      string `json:"uuid"`
	Status    string `json:"status"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expiresAt"`
	Owner     string `json:"owner,omitempty"`
	Shared    bool   `json:"shared,omitempty"`

	ConnectionType    string `json:"connectionType,omitempty"`
	ConnectionMessage string `json:"connectionMessage,omitempty"`
}

type StatusResponse struct {
	UUID      string `json:"uuid"`
	Status    string `json:"status"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expiresAt"`
	Owner     string `json:"owner,omitempty"`
	Shared    bool   `json:"shared,omitempty"`

	ConnectionType    string `json:"connectionType,omitempty"`
	ConnectionMessage string `json:"connectionMessage,omitempty"`
}

type ExtendResponse struct {
	ExpiresAt string `json:"expiresAt"`
}

// UIConfigResponse exposes the API-side feature flags the web UI needs to know
// at bootstrap to hide nav links and skip disabled routes. Public (no auth).
type UIConfigResponse struct {
	CatalogEnabled   bool `json:"catalogEnabled"`
	InstancesEnabled bool `json:"instancesEnabled"`
}

type HealthResponse struct {
	Status string `json:"status"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}
