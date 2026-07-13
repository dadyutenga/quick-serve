package static

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dadyprojects/quick/db"
	"github.com/gofiber/fiber/v2"
)

// Serve returns a handler that serves files from sites/<name>/ with path traversal protection
// and injects site_token into HTML responses.
//
// Token source: sites/<name>/.quick_site_token (mode 0600, never served) written at deploy time.
func Serve(sitesDir string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		siteVal := c.Locals("site")
		if siteVal == nil {
			return c.Status(fiber.StatusNotFound).SendString("site not found")
		}
		site := siteVal.(*db.Site)

		reqPath := c.Path()
		if strings.HasPrefix(reqPath, "/s/") {
			parts := strings.SplitN(strings.TrimPrefix(reqPath, "/s/"), "/", 2)
			if len(parts) == 2 {
				reqPath = "/" + parts[1]
			} else {
				reqPath = "/"
			}
		}
		if strings.HasPrefix(reqPath, "/api/") {
			return c.Next()
		}

		if reqPath == "/" || reqPath == "" {
			reqPath = "/index.html"
		}

		clean := filepath.Clean("/" + reqPath)
		clean = strings.TrimPrefix(clean, "/")
		if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "\\..") {
			return c.Status(fiber.StatusBadRequest).SendString("invalid path")
		}
		// Never serve internal token / control files (any path segment)
		if isQuickControlPath(clean) {
			return c.Status(fiber.StatusNotFound).SendString("not found")
		}

		siteRoot, err := filepath.Abs(filepath.Join(sitesDir, site.Name))
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString("path error")
		}
		fullPath := filepath.Join(siteRoot, clean)
		fullAbs, err := filepath.Abs(fullPath)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("invalid path")
		}
		if fullAbs != siteRoot && !strings.HasPrefix(fullAbs, siteRoot+string(os.PathSeparator)) {
			return c.Status(fiber.StatusBadRequest).SendString("invalid path")
		}

		info, err := os.Stat(fullAbs)
		if err != nil {
			if os.IsNotExist(err) {
				return c.Status(fiber.StatusNotFound).SendString("not found")
			}
			return c.Status(fiber.StatusInternalServerError).SendString("stat error")
		}
		if info.IsDir() {
			fullAbs = filepath.Join(fullAbs, "index.html")
			if _, err := os.Stat(fullAbs); err != nil {
				return c.Status(fiber.StatusNotFound).SendString("not found")
			}
		}

		if strings.HasSuffix(strings.ToLower(fullAbs), ".html") {
			body, err := os.ReadFile(fullAbs)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString("read error")
			}
			token := readSiteTokenFile(siteRoot)
			html := injectToken(string(body), token)
			c.Type("html")
			return c.SendString(html)
		}

		return c.SendFile(fullAbs, false)
	}
}

// isQuickControlPath rejects .quick* basenames anywhere in the path.
func isQuickControlPath(clean string) bool {
	// Normalize to slash for segment walk
	p := filepath.ToSlash(clean)
	for _, seg := range strings.Split(p, "/") {
		if seg == ".quick_site_token" || strings.HasPrefix(seg, ".quick") {
			return true
		}
	}
	return false
}

func readSiteTokenFile(siteRoot string) string {
	b, err := os.ReadFile(filepath.Join(siteRoot, ".quick_site_token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func injectToken(html, token string) string {
	if token == "" {
		return html
	}
	snippet := `<script>window.__QUICK_TOKEN__=` + jsString(token) + `;</script>`
	lower := strings.ToLower(html)
	if i := strings.Index(lower, "</head>"); i >= 0 {
		return html[:i] + snippet + html[i:]
	}
	if i := strings.Index(lower, "</body>"); i >= 0 {
		return html[:i] + snippet + html[i:]
	}
	return snippet + html
}

func jsString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				const hex = "0123456789abcdef"
				b.WriteByte(hex[r>>4])
				b.WriteByte(hex[r&0xf])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// WriteSiteTokenFile stores the plaintext site token for HTML injection (mode 0600).
func WriteSiteTokenFile(siteRoot, siteToken string) error {
	return os.WriteFile(filepath.Join(siteRoot, ".quick_site_token"), []byte(siteToken), 0o600)
}
