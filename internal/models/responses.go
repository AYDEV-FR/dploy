package models

type AvailableEnvironmentResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Category    string `json:"category,omitempty"`
	// TTL info
	TTL         int  `json:"ttl"`                  // Initial TTL in seconds (-1 for unlimited)
	ExtendTTL   int  `json:"extendTTL,omitempty"`  // Seconds added per extension (0 = use default)
	MaxExtends  int  `json:"maxExtends,omitempty"` // Max extensions allowed (0 = unlimited)
	IsUnlimited bool `json:"isUnlimited"`          // True if TTL is unlimited
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
	ManagerEnabled   bool `json:"managerEnabled"`
}

// MeResponse carries the authenticated requester's view of themselves —
// resolved username, owner key (after sanitization) and whether their claims
// mark them as admin under the configured admin claim/value. Used by the web
// UI to decide whether to show the Manager link.
type MeResponse struct {
	Username string `json:"username"`
	Owner    string `json:"owner"`
	Admin    bool   `json:"admin"`
}

// AdminInstanceRow is the per-row shape served by GET /api/admin/instances —
// shaped like `kubectl get dployinstance` for the Manager view, so it's not
// the same as UserEnvironmentResponse (no template description, no quota
// fields, and the instance's metadata.name + creationTimestamp are first-class).
type AdminInstanceRow struct {
	Name        string `json:"name"`        // DployInstance metadata.name
	Template    string `json:"template"`    // spec.templateRef
	Owner       string `json:"owner"`       // spec.owner — empty for unclaimed pool members
	Phase       string `json:"phase"`       // status.phase as the operator reports it
	URL         string `json:"url"`         // status.url
	ExpiresAt   string `json:"expiresAt"`   // RFC3339, empty for unlimited / unclaimed pool
	CreatedAt   string `json:"createdAt"`   // metadata.creationTimestamp, RFC3339
	Namespace   string `json:"namespace"`   // status.namespace (the workload ns)
	UUID        string `json:"uuid"`        // status.uuid
	IsUnlimited bool   `json:"isUnlimited"` // spec.ttlSeconds == -1
}

// AdminInstancesListResponse is the envelope around AdminInstanceRow.
type AdminInstancesListResponse struct {
	Instances []AdminInstanceRow `json:"instances"`
	Count     int                `json:"count"`
}

// AdminTemplateRow is the per-row shape served by GET /api/admin/templates —
// shaped like `kubectl get dploytemplate -o wide` for the Manager view. Pool
// counters are zero for on-demand templates.
type AdminTemplateRow struct {
	Name        string `json:"name"`        // metadata.name
	DisplayName string `json:"displayName"` // spec.displayName
	Method      string `json:"method"`      // "on-demand" or "pool"
	Enabled     bool   `json:"enabled"`     // spec.enabled
	Visible     bool   `json:"visible"`     // resolved IsVisible (defaults true)
	PoolSize    int    `json:"poolSize"`    // spec.pool.size, 0 for on-demand
	Available   int    `json:"available"`   // status.poolAvailable
	Claimed     int    `json:"claimed"`     // status.poolClaimed
	ChartType   string `json:"chartType"`   // "git" or "helm"
	ChartRepo   string `json:"chartRepo"`   // spec.chart.repoURL
	ChartRef    string `json:"chartRef"`    // chart name (helm) or path (git)
	Revision    string `json:"revision"`    // spec.chart.targetRevision
	CreatedAt   string `json:"createdAt"`   // metadata.creationTimestamp, RFC3339
}

// AdminTemplatesListResponse is the envelope around AdminTemplateRow.
type AdminTemplatesListResponse struct {
	Templates []AdminTemplateRow `json:"templates"`
	Count     int                `json:"count"`
}

type HealthResponse struct {
	Status string `json:"status"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}
