package middleware

import (
	"github.com/dadyprojects/quick/db"
	"github.com/gofiber/fiber/v2"
)

// AuthMiddleware validates X-Quick-Token against owner or site token hashes.
// scope "owner": owner token only
// scope "site": site token OR owner token
func AuthMiddleware(scope string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		siteVal := c.Locals("site")
		if siteVal == nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "site not found"})
		}
		site, ok := siteVal.(*db.Site)
		if !ok || site == nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "site not found"})
		}

		token := c.Get("X-Quick-Token")
		if token == "" {
			// Also allow query token for WebSocket (browsers can't set headers on WS upgrade easily)
			token = c.Query("token")
		}
		if token == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing token"})
		}

		switch scope {
		case "owner":
			if !VerifyToken(token, site.OwnerTokenHash) {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "invalid token"})
			}
		case "site":
			if VerifyToken(token, site.OwnerTokenHash) {
				return c.Next()
			}
			if !VerifyToken(token, site.SiteTokenHash) {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "invalid token"})
			}
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "invalid auth scope"})
		}
		return c.Next()
	}
}

// RequireSite ensures site was resolved (for public static / optional auth routes).
func RequireSite() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if c.Locals("site") == nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "site not found"})
		}
		return c.Next()
	}
}
