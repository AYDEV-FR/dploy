package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PoolSpec configures pre-warming for templates using the "pool" method.
type PoolSpec struct {
	// Size is the number of warm, idle instances to keep available for claiming.
	// +kubebuilder:validation:Minimum=0
	Size int `json:"size"`

	// MaxSize caps the total number of instances (idle + claimed). 0 means unlimited.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxSize int `json:"maxSize,omitempty"`

	// Recycle controls whether a claimed instance is destroyed and replaced on release,
	// keeping the pool fresh and preventing state leakage between users. Defaults to true.
	// +kubebuilder:default=true
	// +optional
	Recycle *bool `json:"recycle,omitempty"`
}

// TemplateParameter declares a value a request may supply, exposed to templates as `.Params`.
type TemplateParameter struct {
	// Name is the parameter key.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Description documents the parameter.
	// +optional
	Description string `json:"description,omitempty"`

	// Required rejects requests that omit this parameter when no default is set.
	// +optional
	Required bool `json:"required,omitempty"`

	// Default is used when a request omits the parameter.
	// +optional
	Default string `json:"default,omitempty"`
}

// DployTemplateSpec defines a deployable catalog entry.
type DployTemplateSpec struct {
	// DisplayName is the human-friendly name shown in the UI.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Description is shown in the catalog listing.
	// +optional
	Description string `json:"description,omitempty"`

	// Icon is the UI icon identifier.
	// +optional
	Icon string `json:"icon,omitempty"`

	// Category groups templates in the UI, format "category,subcategory".
	// +optional
	Category string `json:"category,omitempty"`

	// Enabled gates whether instances may be created from this template.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// Visible controls listing in the UI/API. Hidden templates remain runnable by name. Defaults to true.
	// +optional
	Visible *bool `json:"visible,omitempty"`

	// OwnerClaim selects which JWT claim identifies the owner ("primary key") of
	// instances created from this template. The claim value is sanitized and used
	// for the owner label, per-owner quota, instance naming and listing. Examples:
	// "preferred_username" for per-user environments, or "groups" for a team-shared
	// environment that everyone in the group sees and reuses. Empty falls back to
	// the API's configured username claim. Multi-valued claims use their first value.
	// +optional
	OwnerClaim string `json:"ownerClaim,omitempty"`

	// Method selects on-demand provisioning or a pre-warmed pool.
	// +kubebuilder:default=on-demand
	Method TemplateMethod `json:"method,omitempty"`

	// Pool configures pre-warming; required when method is "pool".
	// +optional
	Pool *PoolSpec `json:"pool,omitempty"`

	// Engine overrides OperatorConfig.defaultEngine for this template.
	// +optional
	Engine EngineType `json:"engine,omitempty"`

	// Chart is the Helm chart source.
	Chart ChartSource `json:"chart"`

	// ValuesTemplate is a Go (text/template + sprig) template rendered to Helm values YAML.
	// +optional
	ValuesTemplate string `json:"valuesTemplate,omitempty"`

	// ValueFiles lists values file paths within the chart repository.
	// +optional
	ValueFiles []string `json:"valueFiles,omitempty"`

	// ConnectionURLTemplate overrides OperatorConfig.connectionURLTemplate for this
	// template. Go (text/template) rendered with .Owner, .UUID, .BaseDomain,
	// .Template, .Params and .Claims.
	// +optional
	ConnectionURLTemplate string `json:"connectionURLTemplate,omitempty"`

	// TTL configures instance lifetime; falls back to OperatorConfig defaults when unset.
	// +optional
	TTL *TTLSpec `json:"ttl,omitempty"`

	// MaxInstancesPerUser overrides the per-user quota for this template.
	// +optional
	MaxInstancesPerUser *int `json:"maxInstancesPerUser,omitempty"`

	// Parameters declares request-supplied values exposed to templates as `.Params`.
	// +optional
	Parameters []TemplateParameter `json:"parameters,omitempty"`
}

// DployTemplateStatus reports pool occupancy and readiness.
type DployTemplateStatus struct {
	// PoolAvailable is the number of warm, unclaimed instances ready to be claimed.
	// +optional
	PoolAvailable int `json:"poolAvailable,omitempty"`

	// PoolClaimed is the number of pooled instances currently claimed by users.
	// +optional
	PoolClaimed int `json:"poolClaimed,omitempty"`

	// PoolTotal is the total number of instances derived from this template.
	// +optional
	PoolTotal int `json:"poolTotal,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest observations of the template's state.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=dtpl
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Method",type=string,JSONPath=`.spec.method`
// +kubebuilder:printcolumn:name="Engine",type=string,JSONPath=`.spec.engine`
// +kubebuilder:printcolumn:name="Enabled",type=boolean,JSONPath=`.spec.enabled`
// +kubebuilder:printcolumn:name="Available",type=integer,JSONPath=`.status.poolAvailable`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DployTemplate defines a deployable environment available in the Dploy catalog.
type DployTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DployTemplateSpec   `json:"spec,omitempty"`
	Status DployTemplateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DployTemplateList contains a list of DployTemplate.
type DployTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DployTemplate `json:"items"`
}

// IsVisible reports whether the template should appear in listings. Defaults to true.
func (t *DployTemplate) IsVisible() bool {
	if t.Spec.Visible == nil {
		return true
	}
	return *t.Spec.Visible
}

func init() {
	SchemeBuilder.Register(&DployTemplate{}, &DployTemplateList{})
}
