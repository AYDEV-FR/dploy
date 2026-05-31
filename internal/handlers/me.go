package handlers

import (
	"github.com/AYDEV-FR/dploy/internal/auth"
	"github.com/AYDEV-FR/dploy/internal/config"
	"github.com/AYDEV-FR/dploy/internal/kube"
	"github.com/AYDEV-FR/dploy/internal/models"
	"github.com/gofiber/fiber/v2"
)

// MeHandler answers `GET /api/me`: a single-shot endpoint the web UI calls
// after login to discover its own username, owner key and admin status —
// enough to decide whether to show admin-only affordances (the Manager link).
type MeHandler struct {
	cfg *config.Config
}

func NewMeHandler(cfg *config.Config) *MeHandler {
	return &MeHandler{cfg: cfg}
}

// Get returns the requester's resolved username, owner key and admin flag.
// Mount behind the auth Middleware (claims must already be on c.Locals).
func (h *MeHandler) Get(c *fiber.Ctx) error {
	username, _ := c.Locals(auth.UserContextKey).(string)
	claims, _ := c.Locals(auth.ClaimsContextKey).(map[string]any)
	owner, _ := kube.ResolveOwner(claims, "", username)
	return c.JSON(models.MeResponse{
		Username: username,
		Owner:    owner,
		Admin:    auth.IsAdmin(claims, h.cfg.AdminClaim, h.cfg.AdminValue),
	})
}
