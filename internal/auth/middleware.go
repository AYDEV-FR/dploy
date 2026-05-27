package auth

import (
	"strings"

	"github.com/AYDEV-FR/dploy/internal/logger"
	"github.com/gofiber/fiber/v2"
)

const (
	// UserContextKey holds the sanitized username in the request context.
	UserContextKey = "username"
	// ClaimsContextKey holds the requester's JWT claims (map[string]any).
	ClaimsContextKey = "claims"
)

func Middleware(validator *JWTValidator) fiber.Handler {
	return func(c *fiber.Ctx) error {
		logger.Debug("Auth middleware processing request",
			"method", c.Method(),
			"path", c.Path(),
		)

		// Get token from Authorization header
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			logger.Debug("Missing Authorization header")
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Missing Authorization header",
			})
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == authHeader {
			logger.Debug("Invalid Authorization header format")
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Invalid Authorization header format",
			})
		}

		// Validate token
		username, claims, err := validator.Validate(tokenString)
		if err != nil {
			logger.Warn("JWT validation failed", "error", err)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		logger.Debug("Authenticated user", "user", username)
		c.Locals(UserContextKey, username)
		c.Locals(ClaimsContextKey, claims)
		return c.Next()
	}
}
