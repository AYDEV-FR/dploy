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
)
