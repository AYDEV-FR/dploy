// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package controller

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

// Label keys are defined on the api package (shared with the API server); these
// aliases keep the controller code terse.
const (
	LabelManaged  = dployv1alpha1.LabelManaged
	LabelOwner    = dployv1alpha1.LabelOwner
	LabelTemplate = dployv1alpha1.LabelTemplate
	LabelInstance = dployv1alpha1.LabelInstance
	LabelPooled   = dployv1alpha1.LabelPooled

	// InstanceFinalizer guards teardown of an instance's HelmRelease and namespace.
	InstanceFinalizer = "dploy.dev/instance-cleanup"
)

const maxSegment = 20

// sanitize lowercases s, maps '.' and '@' to '-', drops every other
// non-[a-z0-9-] rune, and trims leading/trailing dashes — yielding a string
// safe to use inside a DNS-1123 label. Mirrors the pre-operator SanitizeUsername.
func sanitize(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.ReplaceAll(s, "@", "-")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-")
}

// shortUUID returns the first 8 hex chars of a random UUID, matching the
// pre-operator identifier format.
func shortUUID() string {
	return strings.ReplaceAll(uuid.New().String(), "-", "")[:8]
}

func truncate(s string, n int) string {
	if len(s) > n {
		return strings.Trim(s[:n], "-")
	}
	return s
}

// workloadNamespace builds the per-instance namespace name as
// `<owner>-<name>-<uuid>` (owner "pool" for unclaimed pool members),
// matching the pre-operator `<username>-<env>-<uuid>` convention.
func workloadNamespace(owner, template, uid string) string {
	o := sanitize(owner)
	if o == "" {
		o = "pool"
	}
	return fmt.Sprintf("%s-%s-%s", truncate(o, maxSegment), truncate(sanitize(template), maxSegment), uid)
}

// defaultHost builds the default `<name>-<uuid>.<baseDomain>` hostname.
// The template name identifies the kind of environment (e.g. `vscode-abc.example`)
// and stays consistent across on-demand and pool instances; the (random) UUID
// disambiguates concurrent ones. Override per-template with connectionURLTemplate
// when you need owner-based or otherwise structured hostnames.
func defaultHost(templateName, uid, baseDomain string) string {
	t := sanitize(templateName)
	if t == "" {
		t = "env"
	}
	return fmt.Sprintf("%s-%s.%s", t, uid, baseDomain)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
