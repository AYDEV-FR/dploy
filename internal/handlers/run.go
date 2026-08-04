// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package handlers

import (
	"errors"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
	"github.com/AYDEV-FR/dploy/internal/auth"
	"github.com/AYDEV-FR/dploy/internal/config"
	"github.com/AYDEV-FR/dploy/internal/kube"
	"github.com/AYDEV-FR/dploy/internal/logger"
	"github.com/AYDEV-FR/dploy/internal/models"
)

type RunHandler struct {
	kubeClient *kube.Client
	config     *config.Config
}

func NewRunHandler(kubeClient *kube.Client, cfg *config.Config) *RunHandler {
	return &RunHandler{kubeClient: kubeClient, config: cfg}
}

// CreateEnvironment records the request as a DployInstanceClaim and hands back
// whatever the operator has made of it so far. Binding, quota and TTL are the
// operator's decisions; this handler validates the requester and writes one CR.
//
//	@Summary		Create or get environment
//	@Tags			run
//	@Security		BearerAuth
//	@Param			env		path	string	true	"Template name"
//	@Param			wait	query	bool	false	"Wait for a warm pool instance instead of provisioning one on demand (default true)"
//	@Produce		json
//	@Success		200	{object}	models.RunEnvironmentResponse
//	@Router			/run/{env} [get]
func (h *RunHandler) CreateEnvironment(c *fiber.Ctx) error {
	username, ok := c.Locals(auth.UserContextKey).(string)
	if !ok {
		return unauthorized(c)
	}
	envName := c.Params("env")
	logger.Debug("CreateEnvironment request", "user", username, "env", envName)

	tmpl, err := h.kubeClient.GetTemplate(c.Context(), envName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return notFound(c, fmt.Sprintf("environment %q not found", envName))
		}
		return internalError(c, err)
	}
	if !tmpl.Spec.Enabled {
		return notFound(c, fmt.Sprintf("environment %q is disabled", envName))
	}

	// Resolve the owner ("primary key") from the requester's claims, per the
	// template's ownerClaim (username, a group, …).
	owner, ok := kube.ResolveOwner(claimsMap(c), tmpl.Spec.OwnerClaim, username)
	if !ok {
		claimName := tmpl.Spec.OwnerClaim
		if claimName == "" {
			claimName = "username"
		}
		return c.Status(fiber.StatusForbidden).JSON(models.ErrorResponse{
			Error: fmt.Sprintf("your token has no usable %q claim required to own this environment", claimName),
		})
	}

	params, err := h.buildParams(c, tmpl)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse{Error: err.Error()})
	}

	// An empty pool parks the request by default rather than provisioning around
	// it — the point of a pool is that environments start warm. `?wait=false`
	// opts into an on-demand instance instead.
	waitForPool := c.QueryBool("wait", true)

	claim, err := h.kubeClient.EnsureClaim(c.Context(), owner, params, tmpl, waitForPool)
	if err != nil {
		return internalError(c, err)
	}
	if claim.Status.Phase == dployv1alpha1.ClaimRejected {
		return respondRejected(c, claim)
	}

	logger.Info("Requested environment", "user", username, "env", envName, "claim", claim.Name)
	return respondClaim(c, claim)
}

// GetStatus returns the status of a user's environment.
//
//	@Summary		Get environment status
//	@Tags			run
//	@Security		BearerAuth
//	@Param			env	path	string	true	"Template name"
//	@Produce		json
//	@Success		200	{object}	models.StatusResponse
//	@Router			/api/run/{env}/status [get]
func (h *RunHandler) GetStatus(c *fiber.Ctx) error {
	claim, err := h.requireClaim(c)
	if err != nil {
		return err
	}

	return c.JSON(models.StatusResponse{
		UUID:              claim.Status.UUID,
		Status:            claimStatus(claim),
		URL:               claim.Status.ConnectionURL,
		ExpiresAt:         claimExpiresAt(claim),
		Owner:             claim.Spec.Owner,
		Shared:            isShared(c, claim.Spec.Owner),
		Message:           claimMessage(claim),
		ConnectionType:    string(claim.Status.ConnectionType),
		ConnectionMessage: claim.Status.ConnectionMessage,
	})
}

// ExtendTTL grants a user's environment more time by raising the claim's
// requested lifetime; the operator recomputes the expiry from the binding.
//
//	@Summary		Extend environment TTL
//	@Tags			run
//	@Security		BearerAuth
//	@Param			env	path	string	true	"Template name"
//	@Produce		json
//	@Success		200	{object}	models.ExtendResponse
//	@Router			/api/run/{env}/extend [post]
func (h *RunHandler) ExtendTTL(c *fiber.Ctx) error {
	claim, err := h.requireClaim(c)
	if err != nil {
		return err
	}
	envName := c.Params("env")

	extendSeconds := h.config.ExtendTTL
	if tmpl, terr := h.kubeClient.GetTemplate(c.Context(), envName); terr == nil &&
		tmpl.Spec.TTL != nil && tmpl.Spec.TTL.ExtendSeconds > 0 {
		extendSeconds = int(tmpl.Spec.TTL.ExtendSeconds)
	}

	newExpires, err := h.kubeClient.ExtendClaim(c.Context(), claim, extendSeconds)
	switch {
	case errors.Is(err, kube.ErrUnlimitedTTL), errors.Is(err, kube.ErrNotBound):
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse{Error: err.Error()})
	case errors.Is(err, kube.ErrMaxExtends):
		return c.Status(fiber.StatusConflict).JSON(models.ErrorResponse{Error: err.Error()})
	case err != nil:
		return internalError(c, err)
	}

	logger.Info("Extended TTL", "env", envName, "claim", claim.Name, "newExpires", newExpires)
	return c.JSON(models.ExtendResponse{ExpiresAt: newExpires.UTC().Format(time.RFC3339)})
}

// DeleteEnvironment deletes a user's environment by deleting its claim. The
// claim owns the instance, so the cluster cascades the teardown.
//
//	@Summary		Delete environment
//	@Tags			run
//	@Security		BearerAuth
//	@Param			env	path	string	true	"Template name"
//	@Success		204	"No Content"
//	@Router			/api/run/{env} [delete]
func (h *RunHandler) DeleteEnvironment(c *fiber.Ctx) error {
	claim, err := h.requireClaim(c)
	if err != nil {
		return err
	}

	if derr := h.kubeClient.DeleteClaim(c.Context(), claim); derr != nil {
		return internalError(c, derr)
	}

	logger.Info("Deleted environment", "env", c.Params("env"), "claim", claim.Name)
	return c.SendStatus(fiber.StatusNoContent)
}

// requireClaim resolves the requester's claim for the path's environment,
// returning a ready-to-send fiber error when there is none.
func (h *RunHandler) requireClaim(c *fiber.Ctx) (*dployv1alpha1.DployInstanceClaim, error) {
	username, ok := c.Locals(auth.UserContextKey).(string)
	if !ok {
		return nil, unauthorized(c)
	}
	envName := c.Params("env")

	owner, ok := h.resolveOwner(c, envName, username)
	if !ok {
		return nil, notFound(c, fmt.Sprintf("environment %q not found", envName))
	}
	claim, err := h.kubeClient.GetOwnerClaim(c.Context(), owner, envName)
	if err != nil {
		return nil, internalError(c, err)
	}
	if claim == nil {
		return nil, notFound(c, fmt.Sprintf("environment %q not found", envName))
	}
	return claim, nil
}

// resolveOwner resolves the owner key for an environment using the template's
// ownerClaim (falling back to the username when the template is absent or unset).
func (h *RunHandler) resolveOwner(c *fiber.Ctx, env, username string) (string, bool) {
	claim := ""
	if tmpl, err := h.kubeClient.GetTemplate(c.Context(), env); err == nil {
		claim = tmpl.Spec.OwnerClaim
	}
	return kube.ResolveOwner(claimsMap(c), claim, username)
}

// buildParams assembles the request context handed to the operator: the JWT
// claims this deployment forwards, overlaid with the template's declared
// parameters. Declared parameters win — an explicit request beats an identity
// attribute that happens to share its name.
func (h *RunHandler) buildParams(c *fiber.Ctx, tmpl *dployv1alpha1.DployTemplate) (map[string]string, error) {
	params := kube.FilterClaims(claimsMap(c), h.config.ForwardedClaims)
	if params == nil {
		params = map[string]string{}
	}
	for _, p := range tmpl.Spec.Parameters {
		v := c.Query(p.Name)
		if v == "" {
			v = p.Default
		}
		if v == "" && p.Required {
			return nil, fmt.Errorf("missing required parameter %q", p.Name)
		}
		if v != "" {
			params[p.Name] = v
		}
	}
	if len(params) == 0 {
		return nil, nil
	}
	return params, nil
}

// claimsMap returns the requester's JWT claims from the request context.
func claimsMap(c *fiber.Ctx) map[string]any {
	if m, ok := c.Locals(auth.ClaimsContextKey).(map[string]any); ok && m != nil {
		return m
	}
	return map[string]any{}
}

// --- shared handler helpers ---

func respondClaim(c *fiber.Ctx, claim *dployv1alpha1.DployInstanceClaim) error {
	return c.JSON(models.RunEnvironmentResponse{
		UUID:              claim.Status.UUID,
		Status:            claimStatus(claim),
		URL:               claim.Status.ConnectionURL,
		ExpiresAt:         claimExpiresAt(claim),
		Owner:             claim.Spec.Owner,
		Shared:            isShared(c, claim.Spec.Owner),
		Message:           claimMessage(claim),
		ConnectionType:    string(claim.Status.ConnectionType),
		ConnectionMessage: claim.Status.ConnectionMessage,
	})
}

// respondRejected turns the operator's verdict into an HTTP status. The reason
// on the Bound condition is the contract: it says whether the request was
// refused because of the requester (quota) or the catalog (missing, disabled).
func respondRejected(c *fiber.Ctx, claim *dployv1alpha1.DployInstanceClaim) error {
	msg := claimMessage(claim)
	if msg == "" {
		msg = "the request was rejected"
	}
	status := fiber.StatusConflict
	if cond := apimeta.FindStatusCondition(claim.Status.Conditions, dployv1alpha1.ConditionBound); cond != nil {
		switch cond.Reason {
		case "QuotaExceeded":
			status = fiber.StatusForbidden
		case "TemplateNotFound", "TemplateDisabled":
			status = fiber.StatusNotFound
		}
	}
	return c.Status(status).JSON(models.ErrorResponse{Error: msg})
}

// isShared reports whether the environment is owned by an identity other than
// the requester's personal one (i.e. a team/group-owned, shared environment).
func isShared(c *fiber.Ctx, owner string) bool {
	if owner == "" {
		return false
	}
	username, _ := c.Locals(auth.UserContextKey).(string)
	self, _ := kube.ResolveOwner(claimsMap(c), "", username)
	return owner != self
}

// claimStatus maps a claim onto the status strings the web UI already
// understands. A bound claim reports its instance's health; the other phases are
// states the instance never had a name for.
func claimStatus(claim *dployv1alpha1.DployInstanceClaim) string {
	switch claim.Status.Phase {
	case dployv1alpha1.ClaimBound:
		return instancePhaseStatus(claim.Status.InstancePhase, claim.Status.Health)
	case dployv1alpha1.ClaimRejected:
		return "Degraded"
	case dployv1alpha1.ClaimExpired:
		return "Deleting"
	default: // Pending or not reconciled yet
		return "pending"
	}
}

// instancePhaseStatus maps an instance phase/health pair onto a UI status string.
func instancePhaseStatus(phase dployv1alpha1.InstancePhase, health string) string {
	switch phase {
	case dployv1alpha1.PhaseReady, dployv1alpha1.PhaseClaimed, dployv1alpha1.PhaseAvailable:
		if health != "" {
			return health
		}
		return "Healthy"
	case dployv1alpha1.PhaseProvisioning:
		return "Progressing"
	case dployv1alpha1.PhaseFailed:
		return "Degraded"
	case dployv1alpha1.PhaseExpiring:
		return "Deleting"
	default: // Pending or empty
		return "pending"
	}
}

// claimExpiresAt formats the operator-anchored expiry. Empty means unlimited, or
// not bound yet.
func claimExpiresAt(claim *dployv1alpha1.DployInstanceClaim) string {
	if claim.Status.ExpiresAt == nil {
		return ""
	}
	return claim.Status.ExpiresAt.UTC().Format(time.RFC3339)
}

// claimMessage surfaces why an environment is not (or no longer) running, so a
// pending or refused request explains itself instead of just spinning.
func claimMessage(claim *dployv1alpha1.DployInstanceClaim) string {
	if claim.Status.Phase == dployv1alpha1.ClaimBound {
		return ""
	}
	if cond := apimeta.FindStatusCondition(claim.Status.Conditions, dployv1alpha1.ConditionBound); cond != nil {
		return cond.Message
	}
	return ""
}

func unauthorized(c *fiber.Ctx) error {
	return c.Status(fiber.StatusUnauthorized).JSON(models.ErrorResponse{Error: "unauthorized: missing user context"})
}

func notFound(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusNotFound).JSON(models.ErrorResponse{Error: msg})
}

func internalError(c *fiber.Ctx, err error) error {
	return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{Error: err.Error()})
}
