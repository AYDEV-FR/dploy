// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

// Package operatorconfig resolves the cluster-scoped OperatorConfig singleton,
// merging it over baked-in defaults so reconcilers always get a complete view.
package operatorconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

// Baked-in fallbacks, used when no OperatorConfig exists or a field is unset.
const (
	defaultFluxNamespace = "flux-system"
	defaultFluxInterval  = 5 * time.Minute
	defaultTTLSeconds    = int64(86400)
	defaultExtendSeconds = int64(7200)
	defaultMaxPerUser    = 5
)

// Effective is the merged, ready-to-use operator configuration.
type Effective struct {
	DefaultEngine         dployv1alpha1.EngineType
	FluxNamespace         string
	FluxServiceAccount    string
	FluxInterval          time.Duration
	BaseDomain            string
	ConnectionURLTemplate string
	TTLSeconds            int64
	ExtendSeconds         int64
	MaxExtends            int
	MaxInstancesPerUser   int
	Values                map[string]any
}

// Resolve reads the OperatorConfig named "default" and merges it over the
// fallbacks. A missing singleton is not an error — the fallbacks are returned.
func Resolve(ctx context.Context, c client.Client) (Effective, error) {
	eff := Effective{
		DefaultEngine:       dployv1alpha1.EngineFlux,
		FluxNamespace:       defaultFluxNamespace,
		FluxInterval:        defaultFluxInterval,
		TTLSeconds:          defaultTTLSeconds,
		ExtendSeconds:       defaultExtendSeconds,
		MaxInstancesPerUser: defaultMaxPerUser,
		Values:              map[string]any{},
	}

	var cfg dployv1alpha1.OperatorConfig
	err := c.Get(ctx, types.NamespacedName{Name: dployv1alpha1.OperatorConfigName}, &cfg)
	if apierrors.IsNotFound(err) {
		return eff, nil
	}
	if err != nil {
		return eff, fmt.Errorf("get OperatorConfig %q: %w", dployv1alpha1.OperatorConfigName, err)
	}

	spec := cfg.Spec
	if spec.DefaultEngine != "" {
		eff.DefaultEngine = spec.DefaultEngine
	}
	if spec.Flux.Namespace != "" {
		eff.FluxNamespace = spec.Flux.Namespace
	}
	if spec.Flux.ServiceAccountName != "" {
		eff.FluxServiceAccount = spec.Flux.ServiceAccountName
	}
	if spec.Flux.Interval != "" {
		if d, perr := time.ParseDuration(spec.Flux.Interval); perr == nil {
			eff.FluxInterval = d
		}
	}
	if spec.BaseDomain != "" {
		eff.BaseDomain = spec.BaseDomain
	}
	if spec.ConnectionURLTemplate != "" {
		eff.ConnectionURLTemplate = spec.ConnectionURLTemplate
	}
	if spec.Defaults.TTLSeconds != 0 {
		eff.TTLSeconds = spec.Defaults.TTLSeconds
	}
	if spec.Defaults.ExtendSeconds != 0 {
		eff.ExtendSeconds = spec.Defaults.ExtendSeconds
	}
	if spec.Defaults.MaxExtends != 0 {
		eff.MaxExtends = spec.Defaults.MaxExtends
	}
	if spec.Defaults.MaxInstancesPerUser != 0 {
		eff.MaxInstancesPerUser = spec.Defaults.MaxInstancesPerUser
	}
	if spec.Values != nil && len(spec.Values.Raw) > 0 {
		vals := map[string]any{}
		if jerr := json.Unmarshal(spec.Values.Raw, &vals); jerr != nil {
			return eff, fmt.Errorf("decode OperatorConfig.spec.values: %w", jerr)
		}
		eff.Values = vals
	}
	return eff, nil
}
