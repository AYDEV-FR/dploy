// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package v1alpha1

// Label keys applied to dploy-managed objects. They form part of the API
// contract between the operator and the dploy API server, so both reference
// these constants rather than string literals.
const (
	// LabelManaged marks resources created by dploy.
	LabelManaged = "dploy.dev/managed"
	// LabelOwner is the sanitized owner of an instance.
	LabelOwner = "dploy.dev/owner"
	// LabelTemplate is the DployTemplate name an instance derives from.
	LabelTemplate = "dploy.dev/template"
	// LabelInstance is the instance's short UUID.
	LabelInstance = "dploy.dev/instance"
	// LabelPooled marks an instance as a warm-pool member.
	LabelPooled = "dploy.dev/pooled"
	// LabelClaim is the name of the DployInstanceClaim an instance is bound to.
	// It is informational — use LabelClaimUID to identify the binding.
	LabelClaim = "dploy.dev/claim"
	// LabelClaimUID is the UID of the DployInstanceClaim an instance is bound to.
	// Writing it is the atomic step that wins a warm pool member: the UID is
	// unique per claim, so a lost race is unambiguous. Names can be recycled;
	// UIDs cannot, which is why the binding key is the UID and not the name.
	LabelClaimUID = "dploy.dev/claim-uid"
)

// Annotation keys applied to dploy-managed objects.
const (
	// AnnotationBoundAt records when an instance was bound to a claim, written in
	// the same update that wins it. It is the durable TTL anchor: the claim's
	// status.boundAt is a second, separate write, and an operator that dies
	// between the two must not restart anyone's clock.
	AnnotationBoundAt = "dploy.dev/bound-at"
)
