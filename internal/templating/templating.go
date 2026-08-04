// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

// Package templating renders the Go (text/template + sprig) templates declared on
// a DployTemplate — the values template and the connection-URL template — against
// a per-instance data context.
package templating

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/Masterminds/sprig/v3"

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
	// Host is the precomputed default `<name>-<uuid>.<baseDomain>` hostname.
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
	// Params is the request context: the parameters the template declares, merged
	// with the JWT claims the API server was configured to forward. It is the only
	// requester-supplied data a template sees — the raw token never leaves the API.
	Params map[string]string
	// Config holds cluster-wide operator config values.
	Config Config
}

// Render parses and executes a single template against data. Missing map keys
// render as their zero value rather than erroring, so optional params are safe
// to reference.
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
