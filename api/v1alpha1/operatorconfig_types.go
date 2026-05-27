package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// OperatorConfigName is the only name the operator honors for an OperatorConfig.
// The singleton is cluster-scoped; objects with any other name are ignored.
const OperatorConfigName = "default"

// ArgoCDConfig holds defaults for the ArgoCD engine.
type ArgoCDConfig struct {
	// Namespace is where Application resources are created.
	// +kubebuilder:default=argocd
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Project is the ArgoCD AppProject applied to created Applications.
	// +kubebuilder:default=dploy
	// +optional
	Project string `json:"project,omitempty"`

	// Server is the destination cluster API server.
	// +kubebuilder:default="https://kubernetes.default.svc"
	// +optional
	Server string `json:"server,omitempty"`
}

// FluxConfig holds defaults for the Flux engine.
type FluxConfig struct {
	// Namespace is where HelmRelease and source resources are created.
	// +kubebuilder:default=flux-system
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// ServiceAccountName is the SA Flux impersonates when applying releases.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Interval is the Flux reconcile interval (e.g. "5m").
	// +kubebuilder:default="5m"
	// +optional
	Interval string `json:"interval,omitempty"`
}

// InstanceDefaults are applied to instances whose template does not specify them.
type InstanceDefaults struct {
	// TTLSeconds is the default initial lifetime in seconds. -1 means unlimited.
	// +kubebuilder:default=86400
	// +optional
	TTLSeconds int64 `json:"ttlSeconds,omitempty"`

	// ExtendSeconds is the default seconds added per TTL extension.
	// +kubebuilder:default=7200
	// +optional
	ExtendSeconds int64 `json:"extendSeconds,omitempty"`

	// MaxExtends is the default maximum number of extensions. -1 means unlimited.
	// +optional
	MaxExtends int `json:"maxExtends,omitempty"`

	// MaxInstancesPerUser is the default per-user quota.
	// +kubebuilder:default=5
	// +optional
	MaxInstancesPerUser int `json:"maxInstancesPerUser,omitempty"`
}

// OperatorConfigSpec defines cluster-wide defaults for Dploy.
type OperatorConfigSpec struct {
	// DefaultEngine selects the engine used when a template does not override it.
	// +kubebuilder:default=flux
	// +optional
	DefaultEngine EngineType `json:"defaultEngine,omitempty"`

	// ArgoCD holds defaults for the ArgoCD engine.
	// +optional
	ArgoCD ArgoCDConfig `json:"argocd,omitempty"`

	// Flux holds defaults for the Flux engine.
	// +optional
	Flux FluxConfig `json:"flux,omitempty"`

	// BaseDomain is used to build per-instance ingress hostnames (<uuid>.<baseDomain>).
	// +optional
	BaseDomain string `json:"baseDomain,omitempty"`

	// ConnectionURLTemplate is the cluster-wide default Go (text/template) for an
	// instance's public URL. A DployTemplate may override it. Rendered with .Owner,
	// .UUID, .BaseDomain, .Template, .Params and .Claims. When empty the operator
	// falls back to "<uuid>.<baseDomain>".
	// +optional
	ConnectionURLTemplate string `json:"connectionURLTemplate,omitempty"`

	// Defaults are applied to instances when their template omits the value.
	// +optional
	Defaults InstanceDefaults `json:"defaults,omitempty"`

	// Values is a free-form map exposed to value templates as `.Config.Values`.
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Values *runtime.RawExtension `json:"values,omitempty"`
}

// OperatorConfigStatus reports the observed state of the operator configuration.
type OperatorConfigStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest observations of the config's state.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=opcfg
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Engine",type=string,JSONPath=`.spec.defaultEngine`
// +kubebuilder:printcolumn:name="BaseDomain",type=string,JSONPath=`.spec.baseDomain`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OperatorConfig is the cluster-scoped singleton holding Dploy operator defaults.
type OperatorConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OperatorConfigSpec   `json:"spec,omitempty"`
	Status OperatorConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OperatorConfigList contains a list of OperatorConfig.
type OperatorConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OperatorConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OperatorConfig{}, &OperatorConfigList{})
}
