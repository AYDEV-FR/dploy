package auth

import (
	"github.com/AYDEV-FR/dploy/internal/config"
	"github.com/AYDEV-FR/dploy/internal/models"
	"github.com/gofiber/fiber/v2"
)

// IsAdmin reports whether the requester's JWT claims mark them as admin under
// the configured claim/value pair. It handles three claim shapes:
//
//   - boolean (e.g. `is_admin: true` with AdminClaim="is_admin", AdminValue="true")
//   - string (e.g. `role: "admin"` with AdminClaim="role", AdminValue="admin")
//   - list of strings (e.g. `groups: ["admin","dev"]` with AdminClaim="groups",
//     AdminValue="admin"; matches if any element equals AdminValue)
//
// Missing claim or unsupported shape → false (deny by default).
func IsAdmin(claims map[string]any, claimName, value string) bool {
	if claimName == "" || claims == nil {
		return false
	}
	raw, ok := claims[claimName]
	if !ok {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return v == (value == "true")
	case string:
		return v == value
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == value {
				return true
			}
		}
	case []string:
		for _, s := range v {
			if s == value {
				return true
			}
		}
	}
	return false
}

// AdminMiddleware enforces that the requester is admin. Mount BEHIND the auth
// Middleware so claims are already attached to c.Locals.
func AdminMiddleware(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		claims, _ := c.Locals(ClaimsContextKey).(map[string]any)
		if !IsAdmin(claims, cfg.AdminClaim, cfg.AdminValue) {
			return c.Status(fiber.StatusForbidden).JSON(models.ErrorResponse{Error: "admin required"})
		}
		return c.Next()
	}
}
