package auth

import (
	"strings"

	"github.com/AYDEV-FR/dploy/internal/logger"
	"github.com/gofiber/fiber/v2"
)

const UserContextKey = "username"

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
		username, err := validator.ValidateToken(tokenString)
		if err != nil {
			logger.Warn("JWT validation failed", "error", err)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		logger.Debug("Authenticated user", "user", username)
		c.Locals(UserContextKey, username)
		return c.Next()
	}
}
