package handlers

import (
	"fmt"

	"github.com/AYDEV-FR/dploy/internal/auth"
	"github.com/AYDEV-FR/dploy/internal/config"
	"github.com/AYDEV-FR/dploy/internal/kube"
	"github.com/AYDEV-FR/dploy/internal/models"
	"github.com/gofiber/fiber/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const statusPending = "pending"

type RunHandler struct {
	kubeClient *kube.Client
	config     *config.Config
}

func NewRunHandler(kubeClient *kube.Client, cfg *config.Config) *RunHandler {
	return &RunHandler{
		kubeClient: kubeClient,
		config:     cfg,
	}
}

// CreateEnvironment creates a new environment or returns an existing one.
//
//	@Summary		Create or get environment
//	@Description	GET request that creates a new environment if it doesn't exist, or returns existing one
//	@Tags			run
//	@Security		BearerAuth
//	@Param			env	path	string	true	"Environment name"
//	@Produce		json
//	@Success		200	{object}	models.RunEnvironmentResponse
//	@Failure		401	{object}	models.ErrorResponse
//	@Failure		403	{object}	models.ErrorResponse
//	@Failure		404	{object}	models.ErrorResponse
//	@Router			/run/{env} [get]
func (h *RunHandler) CreateEnvironment(c *fiber.Ctx) error {
	username, ok := c.Locals(auth.UserContextKey).(string)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(models.ErrorResponse{
			Error: "unauthorized: missing user context",
		})
	}
	envName := c.Params("env")

	env, err := h.kubeClient.GetEnvironment(envName)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(models.ErrorResponse{
			Error: err.Error(),
		})
	}

	// Check if already exists
	existing, err := h.kubeClient.GetUserApplication(c.Context(), username, envName)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{
			Error: err.Error(),
		})
	}

	if existing != nil {
		return h.buildResponseFromApp(c, existing, username)
	}

	// Check global quota
	apps, err := h.kubeClient.ListUserApplications(c.Context(), username)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{
			Error: err.Error(),
		})
	}

	if len(apps.Items) >= h.config.MaxEnvironmentsPerUser {
		return c.Status(fiber.StatusForbidden).JSON(models.ErrorResponse{
			Error: fmt.Sprintf("Maximum %d environments allowed", h.config.MaxEnvironmentsPerUser),
		})
	}

	// Create new application
	app, err := h.kubeClient.CreateApplication(c.Context(), username, envName, env)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{
			Error: err.Error(),
		})
	}

	return h.buildResponseFromApp(c, app, username)
}

// GetStatus returns the status of a user's environment.
//
//	@Summary		Get environment status
//	@Description	Get status of a user's environment
//	@Tags			run
//	@Security		BearerAuth
//	@Param			env	path	string	true	"Environment name"
//	@Produce		json
//	@Success		200	{object}	models.StatusResponse
//	@Failure		401	{object}	models.ErrorResponse
//	@Failure		404	{object}	models.ErrorResponse
//	@Router			/api/run/{env}/status [get]
func (h *RunHandler) GetStatus(c *fiber.Ctx) error {
	username, ok := c.Locals(auth.UserContextKey).(string)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(models.ErrorResponse{
			Error: "unauthorized: missing user context",
		})
	}
	envName := c.Params("env")

	app, err := h.kubeClient.GetUserApplication(c.Context(), username, envName)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{
			Error: err.Error(),
		})
	}

	if app == nil {
		return c.Status(fiber.StatusNotFound).JSON(models.ErrorResponse{
			Error: fmt.Sprintf("Environment %s not found", envName),
		})
	}

	annotations := app.GetAnnotations()
	uuid := annotations["dploy.dev/uuid"]
	expiresAt := annotations["dploy.dev/expires-at"]

	status := statusPending
	if statusObj, found, err := unstructured.NestedMap(app.Object, "status", "health"); err == nil && found {
		if healthStatus, ok := statusObj["status"].(string); ok {
			status = healthStatus
		}
	}

	url := h.kubeClient.GenerateURL(username, uuid)

	return c.JSON(models.StatusResponse{
		UUID:      uuid,
		Status:    status,
		URL:       url,
		ExpiresAt: expiresAt,
	})
}

// ExtendTTL extends the TTL of a user's environment.
//
//	@Summary		Extend environment TTL
//	@Description	Extend the TTL of a user's environment by configured hours
//	@Tags			run
//	@Security		BearerAuth
//	@Param			env	path	string	true	"Environment name"
//	@Produce		json
//	@Success		200	{object}	models.ExtendResponse
//	@Failure		401	{object}	models.ErrorResponse
//	@Failure		404	{object}	models.ErrorResponse
//	@Router			/api/run/{env}/extend [post]
func (h *RunHandler) ExtendTTL(c *fiber.Ctx) error {
	username, ok := c.Locals(auth.UserContextKey).(string)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(models.ErrorResponse{
			Error: "unauthorized: missing user context",
		})
	}
	envName := c.Params("env")

	app, err := h.kubeClient.GetUserApplication(c.Context(), username, envName)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{
			Error: err.Error(),
		})
	}

	if app == nil {
		return c.Status(fiber.StatusNotFound).JSON(models.ErrorResponse{
			Error: fmt.Sprintf("Environment %s not found", envName),
		})
	}

	appName := app.GetName()
	newExpires, err := h.kubeClient.ExtendApplication(c.Context(), appName)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{
			Error: err.Error(),
		})
	}

	return c.JSON(models.ExtendResponse{
		ExpiresAt: newExpires.UTC().Format("2006-01-02T15:04:05Z07:00"), // Return ISO 8601 format
	})
}

// DeleteEnvironment deletes a user's environment.
//
//	@Summary		Delete environment
//	@Description	Delete a user's environment
//	@Tags			run
//	@Security		BearerAuth
//	@Param			env	path	string	true	"Environment name"
//	@Success		204	"No Content"
//	@Failure		401	{object}	models.ErrorResponse
//	@Failure		404	{object}	models.ErrorResponse
//	@Router			/api/run/{env} [delete]
func (h *RunHandler) DeleteEnvironment(c *fiber.Ctx) error {
	username, ok := c.Locals(auth.UserContextKey).(string)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(models.ErrorResponse{
			Error: "unauthorized: missing user context",
		})
	}
	envName := c.Params("env")

	app, err := h.kubeClient.GetUserApplication(c.Context(), username, envName)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{
			Error: err.Error(),
		})
	}

	if app == nil {
		return c.Status(fiber.StatusNotFound).JSON(models.ErrorResponse{
			Error: fmt.Sprintf("Environment %s not found", envName),
		})
	}

	appName := app.GetName()
	if err := h.kubeClient.DeleteApplication(c.Context(), appName); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{
			Error: err.Error(),
		})
	}

	return c.SendStatus(fiber.StatusNoContent)
}

func (h *RunHandler) buildResponseFromApp(c *fiber.Ctx, app *unstructured.Unstructured, username string) error {
	annotations := app.GetAnnotations()
	uuid := annotations["dploy.dev/uuid"]
	expiresAt := annotations["dploy.dev/expires-at"]

	status := statusPending
	if statusObj, found, err := unstructured.NestedMap(app.Object, "status", "health"); err == nil && found {
		if healthStatus, ok := statusObj["status"].(string); ok {
			status = healthStatus
		}
	}

	url := h.kubeClient.GenerateURL(username, uuid)

	return c.JSON(models.RunEnvironmentResponse{
		UUID:      uuid,
		Status:    status,
		URL:       url,
		ExpiresAt: expiresAt,
	})
}
