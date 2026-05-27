// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package operatorconfig

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := dployv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func TestResolveMissingReturnsDefaults(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
	eff, err := Resolve(context.Background(), c)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if eff.DefaultEngine != dployv1alpha1.EngineFlux {
		t.Errorf("DefaultEngine = %q, want flux", eff.DefaultEngine)
	}
	if eff.TTLSeconds != defaultTTLSeconds {
		t.Errorf("TTLSeconds = %d, want %d", eff.TTLSeconds, defaultTTLSeconds)
	}
	if eff.MaxInstancesPerUser != defaultMaxPerUser {
		t.Errorf("MaxInstancesPerUser = %d, want %d", eff.MaxInstancesPerUser, defaultMaxPerUser)
	}
}

func TestResolveMergesSingleton(t *testing.T) {
	cfg := &dployv1alpha1.OperatorConfig{
		ObjectMeta: metav1.ObjectMeta{Name: dployv1alpha1.OperatorConfigName},
		Spec: dployv1alpha1.OperatorConfigSpec{
			BaseDomain:            "x.dev",
			ConnectionURLTemplate: "{{ .UUID }}.x.dev",
			Defaults: dployv1alpha1.InstanceDefaults{
				TTLSeconds:          100,
				MaxInstancesPerUser: 9,
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(cfg).Build()
	eff, err := Resolve(context.Background(), c)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if eff.BaseDomain != "x.dev" {
		t.Errorf("BaseDomain = %q", eff.BaseDomain)
	}
	if eff.ConnectionURLTemplate != "{{ .UUID }}.x.dev" {
		t.Errorf("ConnectionURLTemplate = %q", eff.ConnectionURLTemplate)
	}
	if eff.TTLSeconds != 100 {
		t.Errorf("TTLSeconds = %d, want 100", eff.TTLSeconds)
	}
	if eff.MaxInstancesPerUser != 9 {
		t.Errorf("MaxInstancesPerUser = %d, want 9", eff.MaxInstancesPerUser)
	}
	// Unset engine still falls back to the default.
	if eff.DefaultEngine != dployv1alpha1.EngineFlux {
		t.Errorf("DefaultEngine = %q, want flux", eff.DefaultEngine)
	}
}
