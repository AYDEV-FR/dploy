package v1alpha1

// EngineType selects the GitOps engine used to materialize an instance.
// +kubebuilder:validation:Enum=argocd;flux
type EngineType string

const (
	// EngineArgoCD renders instances as argoproj.io Applications.
	EngineArgoCD EngineType = "argocd"
	// EngineFlux renders instances as Flux HelmReleases.
	EngineFlux EngineType = "flux"
)

// TemplateMethod controls how instances of a DployTemplate are provisioned.
// +kubebuilder:validation:Enum=on-demand;pool
type TemplateMethod string

const (
	// MethodOnDemand provisions a fresh instance for each request.
	MethodOnDemand TemplateMethod = "on-demand"
	// MethodPool keeps a set of pre-warmed instances ready to claim.
	MethodPool TemplateMethod = "pool"
)

// ChartSourceType distinguishes git-based charts from Helm-repository charts.
// +kubebuilder:validation:Enum=git;helm
type ChartSourceType string

const (
	// ChartSourceGit pulls the chart from a git repository path.
	ChartSourceGit ChartSourceType = "git"
	// ChartSourceHelm pulls the chart from a Helm repository.
	ChartSourceHelm ChartSourceType = "helm"
)

// ChartSource describes where a Helm chart is fetched from. It is engine-neutral:
// the operator maps these fields onto the selected engine's source spec.
type ChartSource struct {
	// Type selects between a git repository (path) or a Helm repository (chart name).
	// +kubebuilder:default=git
	// +optional
	Type ChartSourceType `json:"type,omitempty"`

	// RepoURL is the git repository URL or Helm repository URL.
	// +kubebuilder:validation:MinLength=1
	RepoURL string `json:"repoURL"`

	// Path is the chart path within a git repository (used when type=git).
	// +optional
	Path string `json:"path,omitempty"`

	// Chart is the chart name within a Helm repository (used when type=helm).
	// +optional
	Chart string `json:"chart,omitempty"`

	// TargetRevision is the git ref (branch/tag/sha) or chart version.
	// +kubebuilder:default=main
	// +optional
	TargetRevision string `json:"targetRevision,omitempty"`
}

// TTLSpec configures the lifetime of an instance.
type TTLSpec struct {
	// Seconds is the initial time-to-live in seconds. -1 means unlimited (never expires).
	// +optional
	Seconds int64 `json:"seconds,omitempty"`

	// ExtendSeconds is the number of seconds added per extension. 0 falls back to OperatorConfig defaults.
	// +kubebuilder:validation:Minimum=0
	// +optional
	ExtendSeconds int64 `json:"extendSeconds,omitempty"`

	// MaxExtends is the maximum number of extensions allowed. -1 means unlimited, 0 falls back to defaults.
	// +optional
	MaxExtends int `json:"maxExtends,omitempty"`
}

// IsUnlimited reports whether the TTL never expires.
func (t *TTLSpec) IsUnlimited() bool {
	return t != nil && t.Seconds == -1
}
