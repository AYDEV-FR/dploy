package handlers

import (
	"sort"
	"time"

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
// across all owners (including pool members), shaped like `kubectl get
// dployinstance` for the Manager UI's table. Sorted by template then name so
// the order is stable across reloads.
func (h *AdminHandler) ListAllInstances(c *fiber.Ctx) error {
	instances, err := h.kubeClient.ListAllInstances(c.Context())
	if err != nil {
		return internalError(c, err)
	}
	sort.Slice(instances, func(i, j int) bool {
		if instances[i].Spec.TemplateRef != instances[j].Spec.TemplateRef {
			return instances[i].Spec.TemplateRef < instances[j].Spec.TemplateRef
		}
		return instances[i].Name < instances[j].Name
	})

	out := make([]models.AdminInstanceRow, 0, len(instances))
	for i := range instances {
		inst := &instances[i]
		phase := string(inst.Status.Phase)
		if phase == "" {
			phase = "Pending"
		}
		out = append(out, models.AdminInstanceRow{
			Name:        inst.Name,
			Template:    inst.Spec.TemplateRef,
			Owner:       inst.Spec.Owner,
			Phase:       phase,
			URL:         inst.Status.URL,
			ExpiresAt:   instanceExpiresAt(inst),
			CreatedAt:   inst.CreationTimestamp.UTC().Format(time.RFC3339),
			Namespace:   inst.Status.Namespace,
			UUID:        inst.Status.UUID,
			IsUnlimited: inst.Spec.TTLSeconds == -1,
		})
	}
	return c.JSON(models.AdminInstancesListResponse{
		Instances: out,
		Count:     len(out),
	})
}

// ListAllTemplates answers `GET /api/admin/templates`: every DployTemplate
// (visible and hidden, enabled and disabled) shaped like
// `kubectl get dploytemplate -o wide` for the Manager UI.
func (h *AdminHandler) ListAllTemplates(c *fiber.Ctx) error {
	templates, err := h.kubeClient.ListTemplates(c.Context())
	if err != nil {
		return internalError(c, err)
	}
	sort.Slice(templates, func(i, j int) bool {
		return templates[i].Name < templates[j].Name
	})

	out := make([]models.AdminTemplateRow, 0, len(templates))
	for i := range templates {
		t := &templates[i]
		method := string(t.Spec.Method)
		if method == "" {
			method = "on-demand"
		}
		// Chart ref differs by source type: helm uses .Chart, git uses .Path.
		ref := t.Spec.Chart.Chart
		if t.Spec.Chart.Type == "git" || ref == "" {
			ref = t.Spec.Chart.Path
		}
		row := models.AdminTemplateRow{
			Name:        t.Name,
			DisplayName: t.Spec.DisplayName,
			Method:      method,
			Enabled:     t.Spec.Enabled,
			Visible:     t.IsVisible(),
			Available:   t.Status.PoolAvailable,
			Claimed:     t.Status.PoolClaimed,
			ChartType:   string(t.Spec.Chart.Type),
			ChartRepo:   t.Spec.Chart.RepoURL,
			ChartRef:    ref,
			Revision:    t.Spec.Chart.TargetRevision,
			CreatedAt:   t.CreationTimestamp.UTC().Format(time.RFC3339),
		}
		if t.Spec.Pool != nil {
			row.PoolSize = t.Spec.Pool.Size
		}
		out = append(out, row)
	}
	return c.JSON(models.AdminTemplatesListResponse{
		Templates: out,
		Count:     len(out),
	})
}
