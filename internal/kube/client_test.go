// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package kube

import (
	"testing"
	"time"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

func TestInstanceName(t *testing.T) {
	if got := instanceName("alice", "webterm"); got != "alice-webterm" {
		t.Errorf("instanceName = %q", got)
	}
	long := instanceName("alice", string(make([]byte, 300)))
	if len(long) > maxInstanceNameLen {
		t.Errorf("name too long: %d", len(long))
	}
}

func TestResolveTTL(t *testing.T) {
	cases := []struct {
		name string
		tmpl *dployv1alpha1.DployTemplate
		want int64
	}{
		{"no template ttl uses default", &dployv1alpha1.DployTemplate{}, 86400},
		{"template ttl wins", withTTL(100), 100},
		{"unlimited (-1) honored", withTTL(-1), -1},
	}
	for _, tc := range cases {
		if got := resolveTTL(tc.tmpl, 86400); got != tc.want {
			t.Errorf("%s: resolveTTL = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func withTTL(seconds int64) *dployv1alpha1.DployTemplate {
	return &dployv1alpha1.DployTemplate{
		Spec: dployv1alpha1.DployTemplateSpec{TTL: &dployv1alpha1.TTLSpec{Seconds: seconds}},
	}
}

func TestComputeExpiry(t *testing.T) {
	if computeExpiry(-1) != nil {
		t.Error("unlimited TTL should yield nil expiry")
	}
	if computeExpiry(0) != nil {
		t.Error("zero TTL should yield nil expiry")
	}
	exp := computeExpiry(3600)
	if exp == nil {
		t.Fatal("positive TTL should yield an expiry")
	}
	if d := time.Until(exp.Time); d < 59*time.Minute || d > 61*time.Minute {
		t.Errorf("expiry ~1h off: %s", d)
	}
}

func TestExtendCount(t *testing.T) {
	inst := &dployv1alpha1.DployInstance{}
	if ExtendCount(inst) != 0 {
		t.Error("missing annotation should be 0")
	}
	inst.Annotations = map[string]string{annotationExtendCount: "3"}
	if ExtendCount(inst) != 3 {
		t.Error("expected 3")
	}
	inst.Annotations[annotationExtendCount] = "garbage"
	if ExtendCount(inst) != 0 {
		t.Error("unparseable annotation should be 0")
	}
}
