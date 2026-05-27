package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// InstancePhase is a high-level summary of a DployInstance's lifecycle.
// +kubebuilder:validation:Enum=Pending;Provisioning;Ready;Available;Claimed;Expiring;Failed
type InstancePhase string

const (
	// PhasePending means the instance has been accepted but not yet reconciled.
	PhasePending InstancePhase = "Pending"
	// PhaseProvisioning means the engine resource has been applied and is converging.
	PhaseProvisioning InstancePhase = "Provisioning"
	// PhaseReady means an on-demand instance is healthy and reachable.
	PhaseReady InstancePhase = "Ready"
	// PhaseAvailable means a pooled instance is warm and unclaimed.
	PhaseAvailable InstancePhase = "Available"
	// PhaseClaimed means a pooled instance has been claimed by a user.
	PhaseClaimed InstancePhase = "Claimed"
	// PhaseExpiring means the instance is past its TTL and being torn down.
	PhaseExpiring InstancePhase = "Expiring"
	// PhaseFailed means provisioning or reconciliation failed.
	PhaseFailed InstancePhase = "Failed"
)

// DployInstanceSpec is the desired state of a single deployed environment.
type DployInstanceSpec struct {
	// TemplateRef is the name of the DployTemplate (in the same namespace) this instance derives from.
	// +kubebuilder:validation:MinLength=1
	TemplateRef string `json:"templateRef"`

	// Owner is the sanitized username that owns this instance. Empty for an unclaimed pool member.
	// +optional
	Owner string `json:"owner,omitempty"`

	// Claims is a snapshot of the requester's JWT claims, exposed to value templates as `.Claims`.
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Claims *runtime.RawExtension `json:"claims,omitempty"`

	// Params holds request-supplied parameter values, exposed to value templates as `.Params`.
	// +optional
	Params map[string]string `json:"params,omitempty"`

	// Pooled marks this instance as a member of a template's warm pool.
	// +optional
	Pooled bool `json:"pooled,omitempty"`

	// TTLSeconds is the resolved initial lifetime in seconds. -1 means unlimited.
	// +optional
	TTLSeconds int64 `json:"ttlSeconds,omitempty"`

	// ExpiresAt is the absolute expiry time. Empty for unlimited TTL or an unclaimed pool member.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
}

// DployInstanceStatus is the observed state of a deployed environment.
type DployInstanceStatus struct {
	// Phase is a high-level lifecycle summary.
	// +optional
	Phase InstancePhase `json:"phase,omitempty"`

	// UUID is the short unique identifier assigned by the operator.
	// +optional
	UUID string `json:"uuid,omitempty"`

	// Namespace is the workload namespace created for this instance.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// URL is the public ingress URL of the instance.
	// +optional
	URL string `json:"url,omitempty"`

	// Engine is the engine that materialized this instance.
	// +optional
	Engine EngineType `json:"engine,omitempty"`

	// EngineRef is the name of the underlying engine resource (Application or HelmRelease).
	// +optional
	EngineRef string `json:"engineRef,omitempty"`

	// Health mirrors the engine-reported health (e.g. Healthy, Progressing, Degraded).
	// +optional
	Health string `json:"health,omitempty"`

	// Sync mirrors the engine-reported sync state (e.g. Synced, OutOfSync).
	// +optional
	Sync string `json:"sync,omitempty"`

	// ExtendCount is the number of times the TTL has been extended.
	// +optional
	ExtendCount int `json:"extendCount,omitempty"`

	// ExpiresAt mirrors the effective expiry observed by the controller.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest observations of the instance's state.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=dinst
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Template",type=string,JSONPath=`.spec.templateRef`
// +kubebuilder:printcolumn:name="Owner",type=string,JSONPath=`.spec.owner`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.status.url`
// +kubebuilder:printcolumn:name="Expires",type=date,JSONPath=`.status.expiresAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DployInstance is a single deployed (or pooled) environment derived from a DployTemplate.
type DployInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DployInstanceSpec   `json:"spec,omitempty"`
	Status DployInstanceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DployInstanceList contains a list of DployInstance.
type DployInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DployInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DployInstance{}, &DployInstanceList{})
}
