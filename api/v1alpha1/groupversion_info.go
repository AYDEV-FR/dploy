// Package v1alpha1 contains API Schema definitions for the dploy.dev v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=dploy.dev
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "dploy.dev", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	//nolint:staticcheck // scheme.Builder is the kubebuilder-scaffolded pattern; SA1019 is aimed at hand-written api packages.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
