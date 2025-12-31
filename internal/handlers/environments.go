package handlers

import (
	"strconv"

	"github.com/AYDEV-FR/dploy/internal/auth"
	"github.com/AYDEV-FR/dploy/internal/kube"
	"github.com/AYDEV-FR/dploy/internal/models"
	"github.com/gofiber/fiber/v2"
)

type EnvironmentsHandler struct {
	kubeClient *kube.Client
}

func NewEnvironmentsHandler(kubeClient *kube.Client) *EnvironmentsHandler {
	return &EnvironmentsHandler{kubeClient: kubeClient}
}

// ListAvailable returns a list of all available environments.
//
//	@Summary		List available environments
//	@Description	Get list of all enabled environments
//	@Tags			environments
//	@Produce		json
//	@Success		200	{array}	models.AvailableEnvironmentResponse
//	@Router			/api/environments/available [get]
func (h *EnvironmentsHandler) ListAvailable(c *fiber.Ctx) error {
	envs := h.kubeClient.ListAvailableEnvironments()
	defaultTTL := h.kubeClient.GetConfig().DefaultTTL

	response := make([]models.AvailableEnvironmentResponse, len(envs))
	for i, env := range envs {
		// Parse TTL configuration
		ttl := defaultTTL
		extendTTL := 0
		maxExtends := 0
		isUnlimited := false

		if ttlConfig := env.ParseTTL(); ttlConfig != nil {
			ttl = ttlConfig.TTL
			isUnlimited = ttlConfig.IsUnlimited()
			if ttlConfig.HasExtend {
				extendTTL = ttlConfig.ExtendTTL
			}
			if ttlConfig.HasMax {
				maxExtends = ttlConfig.MaxExtends
			}
		}

		response[i] = models.AvailableEnvironmentResponse{
			Name:        env.Name,
			Description: env.Description,
			Icon:        env.Icon,
			Category:    env.Category,
			TTL:         ttl,
			ExtendTTL:   extendTTL,
			MaxExtends:  maxExtends,
			IsUnlimited: isUnlimited,
		}
	}

	return c.JSON(response)
}

// ListUserEnvironments returns all environments for the authenticated user.
//
//	@Summary		List user's environments
//	@Description	Get list of all environments for authenticated user
//	@Tags			environments
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		models.UserEnvironmentResponse
//	@Failure		401	{object}	models.ErrorResponse
//	@Router			/api/environments [get]
func (h *EnvironmentsHandler) ListUserEnvironments(c *fiber.Ctx) error {
	username, ok := c.Locals(auth.UserContextKey).(string)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(models.ErrorResponse{
			Error: "unauthorized: missing user context",
		})
	}

	apps, err := h.kubeClient.ListUserApplications(c.Context(), username)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{
			Error: err.Error(),
		})
	}

	environments := make([]models.UserEnvironmentResponse, 0)
	for _, app := range apps.Items {
		labels := app.GetLabels()
		annotations := app.GetAnnotations()

		status := GetAppStatus(&app)

		envName := labels["dploy.dev/env"]
		uuid := annotations["dploy.dev/uuid"]
		expiresAt := annotations["dploy.dev/expires-at"]
		url := h.kubeClient.GenerateURL(username, uuid)

		// Get icon and description from environment template
		icon := "default"
		description := ""
		if env := h.kubeClient.GetEnvironmentByName(envName); env != nil {
			icon = env.Icon
			description = env.Description
		}

		// Parse extend count
		extendCount := 0
		if extendCountStr := annotations["dploy.dev/extend-count"]; extendCountStr != "" {
			if count, err := strconv.Atoi(extendCountStr); err == nil {
				extendCount = count
			}
		}

		// Parse max extends (-1 = unlimited, 0 = not set/use default)
		maxExtends := 0
		if maxExtendsStr := annotations["dploy.dev/max-extends"]; maxExtendsStr != "" {
			if max, err := strconv.Atoi(maxExtendsStr); err == nil {
				maxExtends = max
			}
		}

		// Parse extend TTL (0 = use default)
		extendTTL := 0
		if extendTTLStr := annotations["dploy.dev/extend-ttl"]; extendTTLStr != "" {
			if ttl, err := strconv.Atoi(extendTTLStr); err == nil {
				extendTTL = ttl
			}
		}

		// Check if unlimited (no expires-at annotation)
		isUnlimited := expiresAt == ""

		environments = append(environments, models.UserEnvironmentResponse{
			Name:        envName,
			Description: description,
			UUID:        uuid,
			Status:      status,
			URL:         url,
			ExpiresAt:   expiresAt,
			Icon:        icon,
			ExtendCount: extendCount,
			MaxExtends:  maxExtends,
			ExtendTTL:   extendTTL,
			IsUnlimited: isUnlimited,
		})
	}

	response := models.UserEnvironmentsListResponse{
		Environments: environments,
		Count:        len(environments),
		Limit:        h.kubeClient.GetConfig().MaxEnvironmentsPerUser,
	}

	return c.JSON(response)
}
