package static

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dadyprojects/quick/db"
	"github.com/gofiber/fiber/v2"
)

// Serve returns a handler that serves files from sites/<name>/ with path traversal protection
// and injects site_token into HTML responses.
//
// Token source: sites/<name>/.quick_site_token (mode 0600, never served) written at deploy time.
//
// Path-based hosting (/s/<name>/...): relative CSS/JS breaks when the browser URL lacks a
// trailing slash (href="css/x" resolves to /s/css/x). We 301 to a trailing slash on the
// site root and inject <base href="/s/<name>/"> plus rewrite root-absolute asset URLs.
func Serve(sitesDir string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		siteVal := c.Locals("site")
		if siteVal == nil {
			return c.Status(fiber.StatusNotFound).SendString("site not found")
		}
		site := siteVal.(*db.Site)

		fullReq := c.Path()
		pathBase := "" // e.g. /s/my-site when using path routing
		reqPath := fullReq

		if strings.HasPrefix(fullReq, "/s/") {
			rest := strings.TrimPrefix(fullReq, "/s/")
			parts := strings.SplitN(rest, "/", 2)
			if len(parts) >= 1 && parts[0] != "" {
				pathBase = "/s/" + parts[0]
			}
			// Exact site root /s/name (no extra path). Fiber Path() usually has no trailing
			// slash — force browser URL to /s/name/ so relative css/js/img resolve correctly.
			// Without this, href="css/style.css" becomes /s/css/style.css → 404, unstyled page.
			if pathBase != "" && (len(parts) == 1 || (len(parts) == 2 && parts[1] == "")) {
				rawPath := string(c.Request().URI().Path())
				if !strings.HasSuffix(rawPath, "/") {
					loc := pathBase + "/"
					if q := string(c.Request().URI().QueryString()); q != "" {
						loc += "?" + q
					}
					return c.Redirect(loc, fiber.StatusMovedPermanently)
				}
				reqPath = "/"
			} else if len(parts) == 2 {
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
			html := string(body)
			if pathBase != "" {
				html = injectBase(html, pathBase+"/")
				html = rewriteRootAbsoluteAssets(html, pathBase)
			}
			html = injectToken(html, token)
			c.Type("html")
			c.Set("Cache-Control", "no-cache")
			return c.SendString(html)
		}

		// Ensure correct MIME for CSS/JS (some deploys look "unstyled" if type is wrong)
		switch strings.ToLower(filepath.Ext(fullAbs)) {
		case ".css":
			c.Type("css")
		case ".js", ".mjs":
			c.Type("js")
		case ".svg":
			c.Type("svg")
		case ".woff":
			c.Set("Content-Type", "font/woff")
		case ".woff2":
			c.Set("Content-Type", "font/woff2")
		}

		return c.SendFile(fullAbs, false)
	}
}

// injectBase adds <base href="..."> so relative css/js/img resolve under the site path prefix.
func injectBase(html, baseHref string) string {
	if baseHref == "" {
		return html
	}
	lower := strings.ToLower(html)
	if strings.Contains(lower, "<base") {
		return html
	}
	tag := `<base href="` + baseHref + `">`
	if i := strings.Index(lower, "<head>"); i >= 0 {
		end := i + len("<head>")
		return html[:end] + "\n" + tag + html[end:]
	}
	if i := strings.Index(lower, "<head "); i >= 0 {
		if j := strings.Index(html[i:], ">"); j >= 0 {
			end := i + j + 1
			return html[:end] + "\n" + tag + html[end:]
		}
	}
	return tag + html
}

// Root-absolute assets like href="/css/app.css" ignore <base>; rewrite them under /s/<name>.
// Leave platform routes alone: /sdk.js, /api, /deploy, /s/, /health, /console, protocol-relative //.
// (Go RE2 has no lookahead — filter reserved prefixes in the replace func.)
var rootAbsAssetRe = regexp.MustCompile(`(?i)(\b(?:href|src|action)=)(["'])/([^"']*)`)

func rewriteRootAbsoluteAssets(html, sitePrefix string) string {
	if sitePrefix == "" {
		return html
	}
	return rootAbsAssetRe.ReplaceAllStringFunc(html, func(m string) string {
		sub := rootAbsAssetRe.FindStringSubmatch(m)
		if len(sub) != 4 {
			return m
		}
		attr, quote, path := sub[1], sub[2], sub[3]
		lower := strings.ToLower(path)
		switch {
		case path == "" || strings.HasPrefix(path, "/"): // protocol-relative was // — path wouldn't include both
			return m
		case lower == "sdk.js" || strings.HasPrefix(lower, "sdk.js?"):
			return m
		case lower == "api" || strings.HasPrefix(lower, "api/"):
			return m
		case lower == "deploy" || strings.HasPrefix(lower, "deploy/"):
			return m
		case strings.HasPrefix(lower, "s/"):
			return m
		case lower == "health" || strings.HasPrefix(lower, "health/"):
			return m
		case lower == "console" || strings.HasPrefix(lower, "console/"):
			return m
		case lower == "sites" || strings.HasPrefix(lower, "sites/"):
			return m
		}
		return attr + quote + sitePrefix + "/" + path + quote
	})
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
