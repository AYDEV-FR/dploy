// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package handlers

import (
	"github.com/gofiber/fiber/v2"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
	"github.com/AYDEV-FR/dploy/internal/auth"
	"github.com/AYDEV-FR/dploy/internal/config"
	"github.com/AYDEV-FR/dploy/internal/kube"
	"github.com/AYDEV-FR/dploy/internal/models"
)

type EnvironmentsHandler struct {
	kubeClient *kube.Client
}

func NewEnvironmentsHandler(kubeClient *kube.Client) *EnvironmentsHandler {
	return &EnvironmentsHandler{kubeClient: kubeClient}
}

// ListAvailable returns the catalog: enabled, visible DployTemplates.
//
//	@Summary		List available environments
//	@Tags			environments
//	@Produce		json
//	@Success		200	{array}	models.AvailableEnvironmentResponse
//	@Router			/api/environments/available [get]
func (h *EnvironmentsHandler) ListAvailable(c *fiber.Ctx) error {
	tmpls, err := h.kubeClient.ListVisibleTemplates(c.Context())
	if err != nil {
		return internalError(c, err)
	}
	cfg := h.kubeClient.GetConfig()

	response := make([]models.AvailableEnvironmentResponse, 0, len(tmpls))
	for i := range tmpls {
		t := &tmpls[i]
		ttl, extend, maxExt, unlimited := templateTTL(t, cfg)
		response = append(response, models.AvailableEnvironmentResponse{
			Name:        t.Name,
			Description: t.Spec.Description,
			Icon:        t.Spec.Icon,
			Category:    t.Spec.Category,
			TTL:         ttl,
			ExtendTTL:   extend,
			MaxExtends:  maxExt,
			IsUnlimited: unlimited,
		})
	}

	return c.JSON(response)
}

// ListUserEnvironments returns all instances owned by the authenticated user.
//
//	@Summary		List user's environments
//	@Tags			environments
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}	models.UserEnvironmentResponse
//	@Router			/api/environments [get]
func (h *EnvironmentsHandler) ListUserEnvironments(c *fiber.Ctx) error {
	username, ok := c.Locals(auth.UserContextKey).(string)
	if !ok {
		return unauthorized(c)
	}

	cfg := h.kubeClient.GetConfig()

	// Index templates once (for icon/description/TTL) and collect the owner claims
	// they use, so we can resolve every identity the requester owns under.
	tmplByName := map[string]*dployv1alpha1.DployTemplate{}
	var ownerClaims []string
	if all, terr := h.kubeClient.ListTemplates(c.Context()); terr == nil {
		for i := range all {
			tmplByName[all[i].Name] = &all[i]
			if all[i].Spec.OwnerClaim != "" {
				ownerClaims = append(ownerClaims, all[i].Spec.OwnerClaim)
			}
		}
	}

	// List everything the requester owns: their username plus any group/claim
	// values used as owner keys (personal + team-shared environments).
	identities := kube.Identities(claimsMap(c), ownerClaims, username)
	insts, err := h.kubeClient.ListOwnedInstances(c.Context(), identities)
	if err != nil {
		return internalError(c, err)
	}

	// The requester's personal owner key, to flag team-shared instances.
	selfOwner, _ := kube.ResolveOwner(claimsMap(c), "", username)

	environments := make([]models.UserEnvironmentResponse, 0, len(insts))
	for i := range insts {
		inst := &insts[i]

		icon, description := "default", ""
		extendTTL, maxExtends := cfg.ExtendTTL, 0
		if t := tmplByName[inst.Spec.TemplateRef]; t != nil {
			icon = t.Spec.Icon
			description = t.Spec.Description
			_, extendTTL, maxExtends, _ = templateTTL(t, cfg)
		}

		environments = append(environments, models.UserEnvironmentResponse{
			Name:        inst.Spec.TemplateRef,
			Description: description,
			UUID:        inst.Status.UUID,
			Status:      instanceStatus(inst),
			URL:         inst.Status.URL,
			ExpiresAt:   instanceExpiresAt(inst),
			Icon:        icon,
			ExtendCount: kube.ExtendCount(inst),
			MaxExtends:  maxExtends,
			ExtendTTL:   extendTTL,
			IsUnlimited: inst.Spec.TTLSeconds == -1,
			Owner:       inst.Spec.Owner,
			Shared:      inst.Spec.Owner != "" && inst.Spec.Owner != selfOwner,

			ConnectionType:    string(inst.Status.ConnectionType),
			ConnectionMessage: inst.Status.ConnectionMessage,
		})
	}

	return c.JSON(models.UserEnvironmentsListResponse{
		Environments: environments,
		Count:        len(environments),
		Limit:        cfg.MaxEnvironmentsPerUser,
	})
}

// templateTTL resolves the effective TTL display values for a template, falling
// back to the API defaults. maxExtends 0 means unlimited.
func templateTTL(tmpl *dployv1alpha1.DployTemplate, cfg *config.Config) (ttl, extend, maxExt int, unlimited bool) {
	ttl = cfg.DefaultTTL
	extend = cfg.ExtendTTL
	if tmpl.Spec.TTL != nil {
		if tmpl.Spec.TTL.Seconds != 0 {
			ttl = int(tmpl.Spec.TTL.Seconds)
		}
		if tmpl.Spec.TTL.ExtendSeconds != 0 {
			extend = int(tmpl.Spec.TTL.ExtendSeconds)
		}
		maxExt = tmpl.Spec.TTL.MaxExtends
	}
	unlimited = ttl == -1
	return ttl, extend, maxExt, unlimited
}
