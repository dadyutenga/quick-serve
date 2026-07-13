package middleware

import (
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// CORS sets CORS headers. Auth is header-based (X-Quick-Token), not cookies.
// Allowed origins: *.BaseDomain, localhost, 127.0.0.1. Unknown origins get no ACAO
// on credentialed-style requests; simple GETs without Origin still work same-origin.
func CORS(baseDomain string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		origin := c.Get("Origin")
		if origin != "" {
			if IsAllowedOrigin(origin, baseDomain) {
				c.Set("Access-Control-Allow-Origin", origin)
				c.Set("Vary", "Origin")
			}
			// else: omit ACAO — browser will block cross-origin reads
		} else {
			// Non-browser / same-origin tools
			c.Set("Access-Control-Allow-Origin", "*")
		}
		c.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Set("Access-Control-Allow-Headers", "Content-Type, X-Quick-Token, Authorization")
		c.Set("Access-Control-Max-Age", "86400")

		if c.Method() == fiber.MethodOptions {
			if origin != "" && !IsAllowedOrigin(origin, baseDomain) {
				return c.SendStatus(fiber.StatusForbidden)
			}
			return c.SendStatus(fiber.StatusNoContent)
		}
		return c.Next()
	}
}

// IsAllowedOrigin checks if origin matches base domain or localhost.
func IsAllowedOrigin(origin, baseDomain string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	baseDomain = strings.ToLower(strings.TrimSpace(baseDomain))
	if baseDomain == "" {
		return false
	}
	if host == baseDomain || strings.HasSuffix(host, "."+baseDomain) {
		return true
	}
	return false
}
