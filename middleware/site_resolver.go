package middleware

import (
	"database/sql"
	"strings"

	"github.com/dadyprojects/quick/db"
	"github.com/gofiber/fiber/v2"
)

// SiteResolver extracts site name from Host (subdomain) or path /s/:name and attaches *db.Site to Locals.
func SiteResolver(database *sql.DB, baseDomain string) fiber.Handler {
	baseDomain = strings.ToLower(strings.TrimSpace(baseDomain))

	return func(c *fiber.Ctx) error {
		path := c.Path()
		// Apex-only routes: no site resolution required
		if path == "/sdk.js" || path == "/health" ||
			strings.HasPrefix(path, "/deploy") ||
			strings.HasPrefix(path, "/sites/") {
			return c.Next()
		}

		name := extractSiteName(c, baseDomain)
		if name == "" {
			return c.Next()
		}

		site, err := db.GetSiteByName(database, name)
		if err == sql.ErrNoRows {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "site not found"})
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "site lookup failed"})
		}

		c.Locals("site", site)
		c.Locals("site_name", site.Name)
		return c.Next()
	}
}

func extractSiteName(c *fiber.Ctx, baseDomain string) string {
	// Dev path-based routing: /s/:name/...
	path := c.Path()
	if strings.HasPrefix(path, "/s/") {
		rest := strings.TrimPrefix(path, "/s/")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) > 0 && isValidSiteName(parts[0]) {
			return strings.ToLower(parts[0])
		}
	}

	host := strings.ToLower(c.Hostname())
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}

	if baseDomain != "" && (host == baseDomain || host == "www."+baseDomain) {
		return ""
	}

	// subdomain.baseDomain
	if baseDomain != "" && strings.HasSuffix(host, "."+baseDomain) {
		sub := strings.TrimSuffix(host, "."+baseDomain)
		if i := strings.Index(sub, "."); i >= 0 {
			sub = sub[:i]
		}
		if isValidSiteName(sub) {
			return sub
		}
		return ""
	}

	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return ""
	}

	// Fallback: first label as site name (e.g. mysite.localhost)
	parts := strings.Split(host, ".")
	if len(parts) >= 2 && isValidSiteName(parts[0]) {
		return parts[0]
	}
	return ""
}

func isValidSiteName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		if r == '-' && i > 0 && i < len(name)-1 {
			continue
		}
		return false
	}
	switch name {
	case "www", "api", "quick", "admin", "static", "sdk":
		return false
	}
	return true
}
