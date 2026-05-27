// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

// Package kube is the dploy API server's Kubernetes client. It reads the catalog
// (DployTemplate) and creates/claims/extends/deletes environments (DployInstance).
// It deliberately never touches Flux or workload resources — that is the
// operator's job. The API only ever writes dploy.dev custom resources.
package kube

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
	"github.com/AYDEV-FR/dploy/internal/config"
	"github.com/AYDEV-FR/dploy/internal/logger"
)

// annotationExtendCount tracks TTL extensions. It lives in instance metadata
// (API-owned) so the API never has to write the operator-owned status.
const annotationExtendCount = "dploy.dev/extend-count"

const maxInstanceNameLen = 253

// ErrPoolExhausted is returned when a pool template has no warm instance to claim.
var ErrPoolExhausted = errors.New("no pooled instance available, try again shortly")

// ErrUnlimitedTTL is returned when extending an instance that never expires.
var ErrUnlimitedTTL = errors.New("environment has unlimited TTL, no extension needed")

// ErrMaxExtends is returned when an instance has reached its extension limit.
var ErrMaxExtends = errors.New("maximum extensions reached")

type Client struct {
	c         client.Client
	namespace string
	config    *config.Config
}

// GetConfig returns the API server configuration.
func (c *Client) GetConfig() *config.Config { return c.config }

// Namespace is where the catalog and instances live.
func (c *Client) Namespace() string { return c.namespace }

func NewClient(cfg *config.Config) (*Client, error) {
	restConfig, err := loadRestConfig()
	if err != nil {
		return nil, err
	}

	scheme := runtime.NewScheme()
	if err := dployv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register scheme: %w", err)
	}

	cl, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	return &Client{c: cl, namespace: cfg.Namespace, config: cfg}, nil
}

func loadRestConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		logger.Debug("Using in-cluster config for Kubernetes connection")
		return cfg, nil
	}
	logger.Debug("In-cluster config not available, falling back to kubeconfig")
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
	cfg, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes config: %w", err)
	}
	return cfg, nil
}

// Ready performs a lightweight catalog read to confirm Kubernetes connectivity.
func (c *Client) Ready(ctx context.Context) error {
	var list dployv1alpha1.DployTemplateList
	return c.c.List(ctx, &list, client.InNamespace(c.namespace), client.Limit(1))
}

// --- Catalog (DployTemplate) ---

// ListTemplates returns every template in the catalog namespace.
func (c *Client) ListTemplates(ctx context.Context) ([]dployv1alpha1.DployTemplate, error) {
	var list dployv1alpha1.DployTemplateList
	if err := c.c.List(ctx, &list, client.InNamespace(c.namespace)); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// ListVisibleTemplates returns the enabled, visible catalog entries.
func (c *Client) ListVisibleTemplates(ctx context.Context) ([]dployv1alpha1.DployTemplate, error) {
	all, err := c.ListTemplates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]dployv1alpha1.DployTemplate, 0, len(all))
	for i := range all {
		if all[i].Spec.Enabled && all[i].IsVisible() {
			out = append(out, all[i])
		}
	}
	return out, nil
}

// GetTemplate fetches a single template by name.
func (c *Client) GetTemplate(ctx context.Context, name string) (*dployv1alpha1.DployTemplate, error) {
	var t dployv1alpha1.DployTemplate
	if err := c.c.Get(ctx, types.NamespacedName{Namespace: c.namespace, Name: name}, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// --- Instances (DployInstance) ---

// ListUserInstances returns the instances owned by a user.
func (c *Client) ListUserInstances(ctx context.Context, owner string) ([]dployv1alpha1.DployInstance, error) {
	var list dployv1alpha1.DployInstanceList
	if err := c.c.List(ctx, &list,
		client.InNamespace(c.namespace),
		client.MatchingLabels{dployv1alpha1.LabelOwner: owner},
	); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// ListOwnedInstances lists instances whose owner label is one of the given keys.
// Used to show every environment a requester owns when ownership keys differ per
// template (e.g. a user's personal instances plus their teams' shared instances).
func (c *Client) ListOwnedInstances(ctx context.Context, owners []string) ([]dployv1alpha1.DployInstance, error) {
	if len(owners) == 0 {
		return nil, nil
	}
	req, err := labels.NewRequirement(dployv1alpha1.LabelOwner, selection.In, owners)
	if err != nil {
		return nil, err
	}
	var list dployv1alpha1.DployInstanceList
	if err := c.c.List(ctx, &list,
		client.InNamespace(c.namespace),
		client.MatchingLabelsSelector{Selector: labels.NewSelector().Add(*req)},
	); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// GetUserInstance returns the user's instance of a template, or nil if none.
func (c *Client) GetUserInstance(ctx context.Context, owner, templateRef string) (*dployv1alpha1.DployInstance, error) {
	var list dployv1alpha1.DployInstanceList
	if err := c.c.List(ctx, &list,
		client.InNamespace(c.namespace),
		client.MatchingLabels{dployv1alpha1.LabelOwner: owner, dployv1alpha1.LabelTemplate: templateRef},
	); err != nil {
		return nil, err
	}
	for i := range list.Items {
		if list.Items[i].DeletionTimestamp.IsZero() {
			return &list.Items[i], nil
		}
	}
	return nil, nil
}

// CreateOrClaim provisions an environment: it claims a warm instance for pool
// templates, or creates a fresh on-demand instance otherwise.
func (c *Client) CreateOrClaim(ctx context.Context, owner string, claims []byte, params map[string]string, tmpl *dployv1alpha1.DployTemplate) (*dployv1alpha1.DployInstance, error) {
	ttl := resolveTTL(tmpl, c.config.DefaultTTL)
	expiresAt := computeExpiry(ttl)

	if tmpl.Spec.Method == dployv1alpha1.MethodPool {
		return c.claimPooled(ctx, owner, claims, params, tmpl, ttl, expiresAt)
	}

	inst := &dployv1alpha1.DployInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instanceName(owner, tmpl.Name),
			Namespace: c.namespace,
			Labels: map[string]string{
				dployv1alpha1.LabelManaged:  "true",
				dployv1alpha1.LabelOwner:    owner,
				dployv1alpha1.LabelTemplate: tmpl.Name,
			},
			Annotations: map[string]string{annotationExtendCount: "0"},
		},
		Spec: dployv1alpha1.DployInstanceSpec{
			TemplateRef: tmpl.Name,
			Owner:       owner,
			Claims:      rawExtension(claims),
			Params:      params,
			TTLSeconds:  ttl,
			ExpiresAt:   expiresAt,
		},
	}
	if err := c.c.Create(ctx, inst); err != nil {
		return nil, err
	}
	return inst, nil
}

// claimPooled hands the user a warm, unclaimed pool member by stamping ownership
// onto it. Returns ErrPoolExhausted if none are available.
func (c *Client) claimPooled(ctx context.Context, owner string, claims []byte, params map[string]string, tmpl *dployv1alpha1.DployTemplate, ttl int64, expiresAt *metav1.Time) (*dployv1alpha1.DployInstance, error) {
	var list dployv1alpha1.DployInstanceList
	if err := c.c.List(ctx, &list,
		client.InNamespace(c.namespace),
		client.MatchingLabels{dployv1alpha1.LabelTemplate: tmpl.Name, dployv1alpha1.LabelPooled: "true"},
	); err != nil {
		return nil, err
	}

	for i := range list.Items {
		inst := &list.Items[i]
		if inst.Spec.Owner != "" || !inst.DeletionTimestamp.IsZero() {
			continue
		}
		if inst.Status.Phase != dployv1alpha1.PhaseAvailable {
			continue
		}
		patch := client.MergeFrom(inst.DeepCopy())
		inst.Spec.Owner = owner
		inst.Spec.Claims = rawExtension(claims)
		inst.Spec.Params = params
		inst.Spec.TTLSeconds = ttl
		inst.Spec.ExpiresAt = expiresAt
		if inst.Labels == nil {
			inst.Labels = map[string]string{}
		}
		inst.Labels[dployv1alpha1.LabelOwner] = owner
		if inst.Annotations == nil {
			inst.Annotations = map[string]string{}
		}
		inst.Annotations[annotationExtendCount] = "0"
		if err := c.c.Patch(ctx, inst, patch); err != nil {
			// Lost the race to another claimer; try the next candidate.
			logger.Debug("Pool claim race, retrying next candidate", "instance", inst.Name, "error", err)
			continue
		}
		return inst, nil
	}
	return nil, ErrPoolExhausted
}

// ExtendInstance pushes an instance's expiry forward by extendSeconds, enforcing
// maxExtends (<= 0 means unlimited). Returns the new expiry time.
func (c *Client) ExtendInstance(ctx context.Context, inst *dployv1alpha1.DployInstance, extendSeconds, maxExtends int) (time.Time, error) {
	if inst.Spec.TTLSeconds == -1 || inst.Spec.ExpiresAt == nil {
		return time.Time{}, ErrUnlimitedTTL
	}
	count := ExtendCount(inst)
	if maxExtends > 0 && count >= maxExtends {
		return time.Time{}, fmt.Errorf("%w (%d)", ErrMaxExtends, maxExtends)
	}

	newExpires := inst.Spec.ExpiresAt.Time.Add(time.Duration(extendSeconds) * time.Second)
	patch := client.MergeFrom(inst.DeepCopy())
	t := metav1.NewTime(newExpires)
	inst.Spec.ExpiresAt = &t
	if inst.Annotations == nil {
		inst.Annotations = map[string]string{}
	}
	inst.Annotations[annotationExtendCount] = strconv.Itoa(count + 1)
	if err := c.c.Patch(ctx, inst, patch); err != nil {
		return time.Time{}, err
	}
	return newExpires, nil
}

// DeleteInstance deletes the CR; the operator's finalizer tears down the
// HelmRelease, source and workload namespace.
func (c *Client) DeleteInstance(ctx context.Context, inst *dployv1alpha1.DployInstance) error {
	return c.c.Delete(ctx, inst)
}

// --- helpers ---

// ExtendCount reads the API-managed extend counter from instance metadata.
func ExtendCount(inst *dployv1alpha1.DployInstance) int {
	if v := inst.Annotations[annotationExtendCount]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}

// resolveTTL picks the template TTL if set (including -1 for unlimited),
// otherwise the API default.
func resolveTTL(tmpl *dployv1alpha1.DployTemplate, defaultSeconds int) int64 {
	if tmpl.Spec.TTL != nil && tmpl.Spec.TTL.Seconds != 0 {
		return tmpl.Spec.TTL.Seconds
	}
	return int64(defaultSeconds)
}

func computeExpiry(ttlSeconds int64) *metav1.Time {
	if ttlSeconds <= 0 { // unlimited (-1) or unset
		return nil
	}
	t := metav1.NewTime(time.Now().Add(time.Duration(ttlSeconds) * time.Second))
	return &t
}

func rawExtension(b []byte) *runtime.RawExtension {
	if len(b) == 0 {
		return nil
	}
	return &runtime.RawExtension{Raw: b}
}

// instanceName builds the deterministic, one-per-(owner,template) CR name.
func instanceName(owner, template string) string {
	name := fmt.Sprintf("%s-%s", owner, template)
	if len(name) > maxInstanceNameLen {
		name = strings.Trim(name[:maxInstanceNameLen], "-")
	}
	return name
}
