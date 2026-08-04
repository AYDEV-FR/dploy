// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClaimPhase is a high-level summary of a DployInstanceClaim's lifecycle.
// +kubebuilder:validation:Enum=Pending;Bound;Rejected;Expired
type ClaimPhase string

const (
	// ClaimPending means the claim is accepted but not yet holding an instance —
	// typically waiting for a warm pool member to become available.
	ClaimPending ClaimPhase = "Pending"
	// ClaimBound means an instance is bound to the claim and owned by it.
	ClaimBound ClaimPhase = "Bound"
	// ClaimRejected means the claim can never be satisfied as written (unknown or
	// disabled template, owner quota exceeded). It is terminal until the spec changes.
	ClaimRejected ClaimPhase = "Rejected"
	// ClaimExpired means the bound instance outlived its TTL and was torn down.
	// The claim survives as a tombstone and no longer counts against the quota.
	ClaimExpired ClaimPhase = "Expired"
)

// Condition types reported on a DployInstanceClaim.
const (
	// ConditionBound reports whether an instance is currently bound to the claim.
	ConditionBound = "Bound"
	// ConditionReady mirrors the bound instance's readiness.
	ConditionReady = "Ready"
)

// DployInstanceClaimSpec is a request for one environment of a template.
//
// A claim is the only object a caller (the dploy API server, or a human with
// kubectl) needs to create: the operator binds it to a warm pool member or
// provisions one on demand, anchors the TTL, enforces the per-owner quota and
// projects the instance status back onto the claim.
type DployInstanceClaimSpec struct {
	// TemplateRef is the name of the DployTemplate (in the same namespace) to claim from.
	// +kubebuilder:validation:MinLength=1
	TemplateRef string `json:"templateRef"`

	// Owner is the sanitized identity key that owns the environment — a username
	// for personal environments, or a group for team-shared ones, per the
	// template's ownerClaim. It is the quota and listing key.
	// +kubebuilder:validation:MinLength=1
	Owner string `json:"owner"`

	// Params is the request context handed to the template: declared request
	// parameters merged with the JWT claim values the caller chose to forward.
	// The API server filters the token down to this map, so a raw bearer token
	// never reaches etcd. The operator exposes it to value templates as both
	// `.Params` and `.Claims`.
	// +optional
	Params map[string]string `json:"params,omitempty"`

	// TTLSeconds is the requested lifetime, counted from the moment the claim is
	// bound (not from creation). 0 falls back to the template/cluster default and
	// -1 requests an unlimited lifetime. Extending an environment is a patch that
	// raises this value; the operator clamps it to the template's maximum and
	// reports the effective figure in status.
	// +optional
	TTLSeconds int64 `json:"ttlSeconds,omitempty"`

	// WaitForPool controls what happens when a pool template has no warm instance
	// left: true keeps the claim Pending until one frees up, false falls back to
	// provisioning a fresh instance on demand.
	// +optional
	WaitForPool bool `json:"waitForPool,omitempty"`
}

// DployInstanceClaimStatus is the observed state of a claim. It carries a full
// projection of the bound instance so callers only ever watch one object.
type DployInstanceClaimStatus struct {
	// Phase is a high-level lifecycle summary.
	// +optional
	Phase ClaimPhase `json:"phase,omitempty"`

	// InstanceRef is the name of the bound DployInstance in the same namespace.
	// +optional
	InstanceRef string `json:"instanceRef,omitempty"`

	// UUID is the bound instance's short identifier.
	// +optional
	UUID string `json:"uuid,omitempty"`

	// ConnectionURL is the resolved connection target of the bound instance.
	// +optional
	ConnectionURL string `json:"connectionURL,omitempty"`

	// ConnectionType describes how connectionURL/connectionMessage should be
	// presented (web | instructions).
	// +optional
	ConnectionType ConnectionType `json:"connectionType,omitempty"`

	// ConnectionMessage is the rendered connection instructions when
	// connectionType is "instructions".
	// +optional
	ConnectionMessage string `json:"connectionMessage,omitempty"`

	// InstancePhase mirrors the bound instance's phase.
	// +optional
	InstancePhase InstancePhase `json:"instancePhase,omitempty"`

	// Health mirrors the bound instance's engine-reported health.
	// +optional
	Health string `json:"health,omitempty"`

	// BoundAt is when the instance was bound. It anchors the TTL clock.
	// +optional
	BoundAt *metav1.Time `json:"boundAt,omitempty"`

	// ExpiresAt is boundAt + the effective TTL. Empty for an unlimited lifetime
	// or a claim that is not bound yet.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// TTLSeconds is the effective lifetime granted, after clamping the requested
	// value to the template maximum. -1 means unlimited.
	// +optional
	TTLSeconds int64 `json:"ttlSeconds,omitempty"`

	// MaxTTLSeconds is the ceiling spec.ttlSeconds may be raised to, derived from
	// the template's ttl.maxExtends and ttl.extendSeconds. -1 means no ceiling.
	// +optional
	MaxTTLSeconds int64 `json:"maxTTLSeconds,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest observations of the claim's state.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=dclaim
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Template",type=string,JSONPath=`.spec.templateRef`
// +kubebuilder:printcolumn:name="Owner",type=string,JSONPath=`.spec.owner`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Instance",type=string,JSONPath=`.status.instanceRef`
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.status.connectionURL`
// Expires is a string column, not date: kubectl renders a date column as
// time-since (an age), which is negative for a future expiry and prints as
// "<invalid>". A string shows the absolute RFC3339 expiry instead.
// +kubebuilder:printcolumn:name="Expires",type=string,JSONPath=`.status.expiresAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DployInstanceClaim is a request for one environment of a DployTemplate. It
// owns the DployInstance it binds, so deleting the claim tears the environment down.
type DployInstanceClaim struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DployInstanceClaimSpec   `json:"spec,omitempty"`
	Status DployInstanceClaimStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DployInstanceClaimList contains a list of DployInstanceClaim.
type DployInstanceClaimList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DployInstanceClaim `json:"items"`
}

// IsActive reports whether the claim currently counts against its owner's quota:
// it holds, or is still trying to hold, an environment.
func (c *DployInstanceClaim) IsActive() bool {
	switch c.Status.Phase {
	case ClaimRejected, ClaimExpired:
		return false
	default:
		return true
	}
}

func init() {
	SchemeBuilder.Register(&DployInstanceClaim{}, &DployInstanceClaimList{})
}
