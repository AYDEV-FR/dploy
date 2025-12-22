package auth

import (
	"log"
	"strings"

	"github.com/gofiber/fiber/v2"
)

const UserContextKey = "username"

func Middleware(validator *JWTValidator) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Get token from Authorization header
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Missing Authorization header",
			})
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == authHeader {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Invalid Authorization header format",
			})
		}

		// Validate token
		username, err := validator.ValidateToken(tokenString)
		if err != nil {
			log.Printf("JWT validation failed: %v", err)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		log.Printf("JWT validation successful for user: %s", username)

		c.Locals(UserContextKey, username)
		return c.Next()
	}
}
