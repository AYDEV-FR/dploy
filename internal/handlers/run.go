// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

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

// CreateEnvironment provisions an environment from a template, or returns the
// user's existing one. Pool templates claim a warm instance; others create one.
//
//	@Summary		Create or get environment
//	@Tags			run
//	@Security		BearerAuth
//	@Param			env	path	string	true	"Template name"
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

	// Return the existing instance if this owner already runs this template.
	existing, err := h.kubeClient.GetUserInstance(c.Context(), owner, envName)
	if err != nil {
		return internalError(c, err)
	}
	if existing != nil {
		return respondInstance(c, existing)
	}

	// Enforce the per-owner quota.
	insts, err := h.kubeClient.ListUserInstances(c.Context(), owner)
	if err != nil {
		return internalError(c, err)
	}
	limit := h.userLimit(tmpl)
	if len(insts) >= limit {
		return c.Status(fiber.StatusForbidden).JSON(models.ErrorResponse{
			Error: fmt.Sprintf("Maximum %d environments allowed", limit),
		})
	}

	params, err := buildParams(c, tmpl)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse{Error: err.Error()})
	}

	inst, err := h.kubeClient.CreateOrClaim(c.Context(), owner, claimsJSON(c), params, tmpl)
	if err != nil {
		if errors.Is(err, kube.ErrPoolExhausted) {
			return c.Status(fiber.StatusServiceUnavailable).JSON(models.ErrorResponse{Error: err.Error()})
		}
		return internalError(c, err)
	}

	logger.Info("Provisioned environment", "user", username, "env", envName, "method", tmpl.Spec.Method)
	return respondInstance(c, inst)
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
	username, ok := c.Locals(auth.UserContextKey).(string)
	if !ok {
		return unauthorized(c)
	}
	envName := c.Params("env")

	owner, ok := h.resolveOwner(c, envName, username)
	if !ok {
		return notFound(c, fmt.Sprintf("environment %q not found", envName))
	}
	inst, err := h.kubeClient.GetUserInstance(c.Context(), owner, envName)
	if err != nil {
		return internalError(c, err)
	}
	if inst == nil {
		return notFound(c, fmt.Sprintf("environment %q not found", envName))
	}

	return c.JSON(models.StatusResponse{
		UUID:      inst.Status.UUID,
		Status:    instanceStatus(inst),
		URL:       inst.Status.URL,
		ExpiresAt: instanceExpiresAt(inst),
		Owner:     inst.Spec.Owner,
		Shared:    isShared(c, inst.Spec.Owner),
	})
}

// ExtendTTL pushes a user's environment expiry forward by the configured amount.
//
//	@Summary		Extend environment TTL
//	@Tags			run
//	@Security		BearerAuth
//	@Param			env	path	string	true	"Template name"
//	@Produce		json
//	@Success		200	{object}	models.ExtendResponse
//	@Router			/api/run/{env}/extend [post]
func (h *RunHandler) ExtendTTL(c *fiber.Ctx) error {
	username, ok := c.Locals(auth.UserContextKey).(string)
	if !ok {
		return unauthorized(c)
	}
	envName := c.Params("env")

	owner, ok := h.resolveOwner(c, envName, username)
	if !ok {
		return notFound(c, fmt.Sprintf("environment %q not found", envName))
	}
	inst, err := h.kubeClient.GetUserInstance(c.Context(), owner, envName)
	if err != nil {
		return internalError(c, err)
	}
	if inst == nil {
		return notFound(c, fmt.Sprintf("environment %q not found", envName))
	}

	extendSeconds := h.config.ExtendTTL
	maxExtends := 0
	if tmpl, terr := h.kubeClient.GetTemplate(c.Context(), envName); terr == nil && tmpl.Spec.TTL != nil {
		if tmpl.Spec.TTL.ExtendSeconds > 0 {
			extendSeconds = int(tmpl.Spec.TTL.ExtendSeconds)
		}
		maxExtends = tmpl.Spec.TTL.MaxExtends
	}

	newExpires, err := h.kubeClient.ExtendInstance(c.Context(), inst, extendSeconds, maxExtends)
	switch {
	case errors.Is(err, kube.ErrUnlimitedTTL):
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse{Error: err.Error()})
	case errors.Is(err, kube.ErrMaxExtends):
		return c.Status(fiber.StatusConflict).JSON(models.ErrorResponse{Error: err.Error()})
	case err != nil:
		return internalError(c, err)
	}

	logger.Info("Extended TTL", "user", username, "env", envName, "newExpires", newExpires)
	return c.JSON(models.ExtendResponse{ExpiresAt: newExpires.UTC().Format(time.RFC3339)})
}

// DeleteEnvironment deletes a user's environment. The operator cleans up the
// underlying workload via its finalizer.
//
//	@Summary		Delete environment
//	@Tags			run
//	@Security		BearerAuth
//	@Param			env	path	string	true	"Template name"
//	@Success		204	"No Content"
//	@Router			/api/run/{env} [delete]
func (h *RunHandler) DeleteEnvironment(c *fiber.Ctx) error {
	username, ok := c.Locals(auth.UserContextKey).(string)
	if !ok {
		return unauthorized(c)
	}
	envName := c.Params("env")

	owner, ok := h.resolveOwner(c, envName, username)
	if !ok {
		return notFound(c, fmt.Sprintf("environment %q not found", envName))
	}
	inst, err := h.kubeClient.GetUserInstance(c.Context(), owner, envName)
	if err != nil {
		return internalError(c, err)
	}
	if inst == nil {
		return notFound(c, fmt.Sprintf("environment %q not found", envName))
	}

	if err := h.kubeClient.DeleteInstance(c.Context(), inst); err != nil {
		return internalError(c, err)
	}

	logger.Info("Deleted environment", "user", username, "env", envName)
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *RunHandler) userLimit(tmpl *dployv1alpha1.DployTemplate) int {
	if tmpl.Spec.MaxInstancesPerUser != nil && *tmpl.Spec.MaxInstancesPerUser > 0 {
		return *tmpl.Spec.MaxInstancesPerUser
	}
	return h.config.MaxEnvironmentsPerUser
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

// claimsMap returns the requester's JWT claims from the request context.
func claimsMap(c *fiber.Ctx) map[string]any {
	if m, ok := c.Locals(auth.ClaimsContextKey).(map[string]any); ok && m != nil {
		return m
	}
	return map[string]any{}
}

// --- shared handler helpers ---

func respondInstance(c *fiber.Ctx, inst *dployv1alpha1.DployInstance) error {
	return c.JSON(models.RunEnvironmentResponse{
		UUID:      inst.Status.UUID,
		Status:    instanceStatus(inst),
		URL:       inst.Status.URL,
		ExpiresAt: instanceExpiresAt(inst),
		Owner:     inst.Spec.Owner,
		Shared:    isShared(c, inst.Spec.Owner),
	})
}

// isShared reports whether the instance is owned by an identity other than the
// requester's personal one (i.e. a team/group-owned, shared environment).
func isShared(c *fiber.Ctx, owner string) bool {
	if owner == "" {
		return false
	}
	username, _ := c.Locals(auth.UserContextKey).(string)
	self, _ := kube.ResolveOwner(claimsMap(c), "", username)
	return owner != self
}

// instanceStatus maps the instance phase/health onto the status strings the web
// UI already understands.
func instanceStatus(inst *dployv1alpha1.DployInstance) string {
	switch inst.Status.Phase {
	case dployv1alpha1.PhaseReady, dployv1alpha1.PhaseClaimed, dployv1alpha1.PhaseAvailable:
		if inst.Status.Health != "" {
			return inst.Status.Health
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

// instanceExpiresAt prefers the operator-observed expiry, falling back to the
// requested one before the first reconcile. Empty means unlimited.
func instanceExpiresAt(inst *dployv1alpha1.DployInstance) string {
	t := inst.Status.ExpiresAt
	if t == nil {
		t = inst.Spec.ExpiresAt
	}
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// claimsJSON marshals the requester's JWT claims for DployInstance.Spec.Claims.
func claimsJSON(c *fiber.Ctx) []byte {
	raw, ok := c.Locals(auth.ClaimsContextKey).(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	return b
}

// buildParams collects the template's declared parameters from the query string,
// applying defaults and enforcing required ones.
func buildParams(c *fiber.Ctx, tmpl *dployv1alpha1.DployTemplate) (map[string]string, error) {
	params := map[string]string{}
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
	return params, nil
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
