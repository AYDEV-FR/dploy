package handlers

import (
	"github.com/AYDEV-FR/dploy/internal/kube"
	"github.com/AYDEV-FR/dploy/internal/models"
	"github.com/gofiber/fiber/v2"
)

// AdminHandler exposes cluster-wide views for the Manager UI. Every route must
// be mounted behind both auth.Middleware and auth.AdminMiddleware.
type AdminHandler struct {
	kubeClient *kube.Client
}

func NewAdminHandler(kubeClient *kube.Client) *AdminHandler {
	return &AdminHandler{kubeClient: kubeClient}
}

// ListAllInstances answers `GET /api/admin/instances`: every DployInstance
// across all owners (including pool members). The response shape matches
// UserEnvironmentResponse so the Manager UI can reuse the env-list rendering;
// `shared` and `owner` flag the cross-owner context.
func (h *AdminHandler) ListAllInstances(c *fiber.Ctx) error {
	instances, err := h.kubeClient.ListAllInstances(c.Context())
	if err != nil {
		return internalError(c, err)
	}

	out := make([]models.UserEnvironmentResponse, 0, len(instances))
	for i := range instances {
		inst := &instances[i]
		out = append(out, models.UserEnvironmentResponse{
			Name:              inst.Spec.TemplateRef,
			UUID:              inst.Status.UUID,
			Status:            instanceStatus(inst),
			URL:               inst.Status.URL,
			ExpiresAt:         instanceExpiresAt(inst),
			ExtendCount:       kube.ExtendCount(inst),
			IsUnlimited:       inst.Spec.TTLSeconds == -1,
			Owner:             inst.Spec.Owner,
			Shared:            true, // admin view — everything is "someone else's" perspective
			ConnectionType:    string(inst.Status.ConnectionType),
			ConnectionMessage: inst.Status.ConnectionMessage,
		})
	}
	return c.JSON(models.UserEnvironmentsListResponse{
		Environments: out,
		Count:        len(out),
		Limit:        -1,
	})
}
