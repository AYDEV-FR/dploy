package handlers

import (
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

	response := make([]models.AvailableEnvironmentResponse, len(envs))
	for i, env := range envs {
		response[i] = models.AvailableEnvironmentResponse{
			Name:        env.Name,
			Description: env.Description,
			Icon:        env.Icon,
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

		// Get icon from environment template
		icon := "default"
		if env := h.kubeClient.GetEnvironmentByName(envName); env != nil {
			icon = env.Icon
		}

		environments = append(environments, models.UserEnvironmentResponse{
			Name:      envName,
			UUID:      uuid,
			Status:    status,
			URL:       url,
			ExpiresAt: expiresAt,
			Icon:      icon,
		})
	}

	response := models.UserEnvironmentsListResponse{
		Environments: environments,
		Count:        len(environments),
		Limit:        h.kubeClient.GetConfig().MaxEnvironmentsPerUser,
	}

	return c.JSON(response)
}
