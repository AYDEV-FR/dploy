// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

// Package kube is the dploy API server's Kubernetes client. It reads the catalog
// (DployTemplate) and creates, extends and deletes environment requests
// (DployInstanceClaim). Its only other access is *read-only* on DployInstances,
// for the Manager UI's cluster-wide view. It never writes an instance and never
// touches Flux or workloads: the operator owns binding, TTL and teardown, and the
// API's job is to turn an authenticated request into a claim and read back what
// the operator made of it.
package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

const maxClaimNameLen = 253

// ErrUnlimitedTTL is returned when extending an environment that never expires.
var ErrUnlimitedTTL = errors.New("environment has unlimited TTL, no extension needed")

// ErrMaxExtends is returned when an environment has reached the lifetime its
// template allows.
var ErrMaxExtends = errors.New("maximum extensions reached")

// ErrNotBound is returned when extending an environment that has no lifetime yet
// because the operator has not bound it.
var ErrNotBound = errors.New("environment is not running yet, nothing to extend")

type Client struct {
	c         client.Client
	namespace string
	config    *config.Config
}

// GetConfig returns the API server configuration.
func (c *Client) GetConfig() *config.Config { return c.config }

// Namespace is where the catalog and claims live.
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

// --- Instances (DployInstance), read-only ---

// ListAllInstances returns every DployInstance in the configured namespace,
// including unclaimed warm pool members. It exists for the Manager UI's
// cluster-wide view and is the *only* place the API touches an instance — the
// API's Role grants get/list/watch and nothing more, so it can observe what the
// operator built but never alter it.
func (c *Client) ListAllInstances(ctx context.Context) ([]dployv1alpha1.DployInstance, error) {
	var list dployv1alpha1.DployInstanceList
	if err := c.c.List(ctx, &list, client.InNamespace(c.namespace)); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// --- Environment requests (DployInstanceClaim) ---

// GetOwnerClaim returns an owner's live claim for a template, or nil. Terminal
// claims (Rejected, Expired) are reported too: the caller decides whether to
// surface the outcome or replace the request.
func (c *Client) GetOwnerClaim(ctx context.Context, owner, templateRef string) (*dployv1alpha1.DployInstanceClaim, error) {
	var claim dployv1alpha1.DployInstanceClaim
	err := c.c.Get(ctx, types.NamespacedName{Namespace: c.namespace, Name: ClaimName(owner, templateRef)}, &claim)
	switch {
	case apierrors.IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, err
	case !claim.DeletionTimestamp.IsZero():
		return nil, nil
	}
	return &claim, nil
}

// ListOwnedClaims lists the claims whose owner label is one of the given keys.
// Ownership keys differ per template (a user's own environments plus the ones
// their teams share), so listing takes every identity the requester maps to.
func (c *Client) ListOwnedClaims(ctx context.Context, owners []string) ([]dployv1alpha1.DployInstanceClaim, error) {
	if len(owners) == 0 {
		return nil, nil
	}
	req, err := labels.NewRequirement(dployv1alpha1.LabelOwner, selection.In, owners)
	if err != nil {
		return nil, err
	}
	var list dployv1alpha1.DployInstanceClaimList
	if err := c.c.List(ctx, &list,
		client.InNamespace(c.namespace),
		client.MatchingLabelsSelector{Selector: labels.NewSelector().Add(*req)},
	); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// EnsureClaim returns the owner's live claim for a template, creating one if
// there is none. The claim name is derived from (owner, template), so asking
// twice yields the same environment rather than a second one.
//
// An expired claim left over from a previous session is replaced: it is a
// tombstone, and the user is asking for a new environment. A *rejected* claim is
// handed back untouched so the caller can report why the request was refused —
// silently retrying it would turn a clear "you are over quota" into a
// pending environment that never arrives.
func (c *Client) EnsureClaim(
	ctx context.Context,
	owner string,
	params map[string]string,
	tmpl *dployv1alpha1.DployTemplate,
	waitForPool bool,
) (*dployv1alpha1.DployInstanceClaim, error) {
	existing, err := c.GetOwnerClaim(ctx, owner, tmpl.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.Status.Phase != dployv1alpha1.ClaimExpired {
			return existing, nil
		}
		logger.Debug("Replacing expired claim", "claim", existing.Name)
		uid := existing.UID
		if derr := c.c.Delete(ctx, existing, client.Preconditions{UID: &uid}); derr != nil && !apierrors.IsNotFound(derr) {
			return nil, derr
		}
	}

	claim := &dployv1alpha1.DployInstanceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ClaimName(owner, tmpl.Name),
			Namespace: c.namespace,
			Labels: map[string]string{
				dployv1alpha1.LabelManaged:  "true",
				dployv1alpha1.LabelOwner:    owner,
				dployv1alpha1.LabelTemplate: tmpl.Name,
			},
		},
		Spec: dployv1alpha1.DployInstanceClaimSpec{
			TemplateRef: tmpl.Name,
			Owner:       owner,
			Params:      params,
			WaitForPool: waitForPool,
			// TTL is left at 0 on purpose: the operator resolves it from the
			// template and the cluster defaults, and anchors the clock when it
			// binds. The API has no say in how long an environment lives.
		},
	}
	if err := c.c.Create(ctx, claim); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Another request for the same owner and template beat us to it.
			return c.GetOwnerClaim(ctx, owner, tmpl.Name)
		}
		return nil, err
	}
	return claim, nil
}

// ExtendClaim grants an environment more time by raising the claim's requested
// lifetime. There is no extend verb and no new deadline to compute: the operator
// derives the expiry from the binding anchor, so an extension is one patch.
func (c *Client) ExtendClaim(ctx context.Context, claim *dployv1alpha1.DployInstanceClaim, extendSeconds int) (time.Time, error) {
	granted := claim.Status.TTLSeconds
	if granted == -1 {
		return time.Time{}, ErrUnlimitedTTL
	}
	if claim.Status.BoundAt == nil || granted == 0 {
		return time.Time{}, ErrNotBound
	}

	maxTTL := claim.Status.MaxTTLSeconds
	if maxTTL > 0 && granted >= maxTTL {
		return time.Time{}, fmt.Errorf("%w (%s)", ErrMaxExtends, (time.Duration(maxTTL) * time.Second).String())
	}
	wanted := granted + int64(extendSeconds)
	if maxTTL > 0 && wanted > maxTTL {
		wanted = maxTTL
	}

	patch := client.MergeFrom(claim.DeepCopy())
	claim.Spec.TTLSeconds = wanted
	if err := c.c.Patch(ctx, claim, patch); err != nil {
		return time.Time{}, err
	}
	return claim.Status.BoundAt.Add(time.Duration(wanted) * time.Second), nil
}

// DeleteClaim removes the request. The claim owns its DployInstance, so the
// cluster garbage-collects the environment and the operator's finalizer unwinds
// the workload behind it.
func (c *Client) DeleteClaim(ctx context.Context, claim *dployv1alpha1.DployInstanceClaim) error {
	return c.c.Delete(ctx, claim)
}

// --- helpers ---

// ClaimName builds the deterministic, one-per-(owner, template) claim name. It is
// what makes "give me this environment" idempotent.
func ClaimName(owner, template string) string {
	name := fmt.Sprintf("%s-%s", owner, template)
	if len(name) > maxClaimNameLen {
		name = strings.Trim(name[:maxClaimNameLen], "-")
	}
	return name
}

// ExtendCount reconstructs how many extensions an environment has had, from the
// granted lifetime and the template's extend step. The operator tracks a
// lifetime, not a counter, so this exists purely to keep the UI's "3/5 extends"
// affordance meaningful.
func ExtendCount(claim *dployv1alpha1.DployInstanceClaim, baseTTL, extendSeconds int64) int {
	granted := claim.Status.TTLSeconds
	if granted <= 0 || extendSeconds <= 0 || granted <= baseTTL {
		return 0
	}
	return int((granted - baseTTL) / extendSeconds)
}

// FilterClaims narrows a JWT down to the claims a deployment chose to forward,
// flattening multi-valued ones (e.g. groups) into a comma-separated string.
// Anything unlisted never leaves the API server.
func FilterClaims(claims map[string]any, allow []string) map[string]string {
	if len(claims) == 0 || len(allow) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, key := range allow {
		vals := claimValues(claims[key])
		if len(vals) == 0 {
			continue
		}
		out[key] = strings.Join(vals, ",")
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
