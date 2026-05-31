// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

// Package templating renders the Go (text/template + sprig) templates declared on
// a DployTemplate — the values template and the connection-URL template — against
// a per-instance data context.
package templating

import (
	"bytes"
	"encoding/json"
	"fmt"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"k8s.io/apimachinery/pkg/runtime"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

// Config exposes the cluster-wide free-form values to templates as `.Config.Values`.
type Config struct {
	Values map[string]any
}

// Data is the context passed to every template rendered for an instance.
type Data struct {
	// Owner is the sanitized owner; empty for an unclaimed pool member.
	Owner string
	// UUID is the instance's immutable short identifier.
	UUID string
	// BaseDomain is the cluster-wide ingress base domain.
	BaseDomain string
	// Host is the precomputed default `<template>-<uuid>.<baseDomain>` hostname.
	// Routing-neutral (works for Ingress, HTTPRoute, anything host-based).
	// Override per-template via connectionURLTemplate (e.g. to use .Owner instead).
	Host string
	// URL is the resolved public URL (set after the connection-URL template renders).
	URL string
	// ConnectionURL is an alias of URL, available to the connection-message template.
	ConnectionURL string
	// Namespace is the workload namespace the instance deploys into.
	Namespace string
	// Template is the DployTemplate this instance derives from.
	Template *dployv1alpha1.DployTemplate
	// Params are the request-supplied parameters.
	Params map[string]string
	// Claims is the requester's JWT claims snapshot.
	Claims map[string]any
	// Config holds cluster-wide operator config values.
	Config Config
}

// Render parses and executes a single template against data. Missing map keys
// render as their zero value rather than erroring, so optional claims/params are
// safe to reference.
func Render(name, text string, data *Data) (string, error) {
	tmpl, err := template.New(name).
		Funcs(sprig.TxtFuncMap()).
		Option("missingkey=zero").
		Parse(text)
	if err != nil {
		return "", fmt.Errorf("parse %s template: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render %s template: %w", name, err)
	}
	return buf.String(), nil
}

// ClaimsMap decodes a RawExtension claims snapshot into a map. A nil or empty
// snapshot yields an empty (non-nil) map.
func ClaimsMap(raw *runtime.RawExtension) (map[string]any, error) {
	out := map[string]any{}
	if raw == nil || len(raw.Raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw.Raw, &out); err != nil {
		return nil, fmt.Errorf("decode claims: %w", err)
	}
	return out, nil
}
