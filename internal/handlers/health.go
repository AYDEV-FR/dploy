package handlers

import (
	"github.com/AYDEV-FR/dploy/internal/kube"
	"github.com/AYDEV-FR/dploy/internal/models"
	"github.com/gofiber/fiber/v2"
)

type HealthHandler struct {
	kubeClient *kube.Client
}

func NewHealthHandler(kubeClient *kube.Client) *HealthHandler {
	return &HealthHandler{kubeClient: kubeClient}
}

// Health returns the liveness status of the API.
//
//	@Summary		Health check
//	@Description	Liveness probe
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	models.HealthResponse
//	@Router			/health [get]
func (h *HealthHandler) Health(c *fiber.Ctx) error {
	return c.JSON(models.HealthResponse{Status: "ok"})
}

// Ready returns the readiness status of the API.
//
//	@Summary		Readiness check
//	@Description	Readiness probe - checks Kubernetes connectivity
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	models.HealthResponse
//	@Failure		503	{object}	models.ErrorResponse
//	@Router			/ready [get]
func (h *HealthHandler) Ready(c *fiber.Ctx) error {
	if err := h.kubeClient.Ready(c.Context()); err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(models.ErrorResponse{
			Error: "Service not ready",
		})
	}

	return c.JSON(models.HealthResponse{Status: "ready"})
}
