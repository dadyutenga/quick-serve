package routes

import (
	"archive/zip"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dadyprojects/quick/config"
	"github.com/dadyprojects/quick/db"
	"github.com/dadyprojects/quick/middleware"
	"github.com/dadyprojects/quick/static"
	"github.com/gofiber/fiber/v2"
)

var siteNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

type DeployHandler struct {
	DB     *sql.DB
	Cfg    *config.Config
	mu     sync.Mutex
	status map[string]string // name -> status
	// inFlight prevents concurrent deploys for the same name
	inFlight map[string]struct{}
}

func NewDeployHandler(database *sql.DB, cfg *config.Config) *DeployHandler {
	return &DeployHandler{
		DB:       database,
		Cfg:      cfg,
		status:   make(map[string]string),
		inFlight: make(map[string]struct{}),
	}
}

func (h *DeployHandler) setStatus(name, s string) {
	h.mu.Lock()
	h.status[name] = s
	h.mu.Unlock()
}

func (h *DeployHandler) getStatus(name string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.status[name]
	return s, ok
}

func (h *DeployHandler) delStatus(name string) {
	h.mu.Lock()
	delete(h.status, name)
	h.mu.Unlock()
}

func (h *DeployHandler) tryLockName(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.inFlight[name]; ok {
		return false
	}
	h.inFlight[name] = struct{}{}
	return true
}

func (h *DeployHandler) unlockName(name string) {
	h.mu.Lock()
	delete(h.inFlight, name)
	h.mu.Unlock()
}

// POST /deploy — create new site from zip
func (h *DeployHandler) Deploy(c *fiber.Ctx) error {
	if cl := int64(c.Request().Header.ContentLength()); cl > h.Cfg.MaxDeploySize+1024*1024 {
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": "upload too large"})
	}

	name := strings.ToLower(strings.TrimSpace(c.FormValue("name")))
	if name == "" {
		var genErr error
		name, genErr = randomSiteName()
		if genErr != nil {
			return c.Status(500).JSON(fiber.Map{"error": "name generation failed"})
		}
	}
	if !siteNameRe.MatchString(name) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid site name (use lowercase alphanumeric and hyphens)"})
	}
	switch name {
	case "www", "api", "quick", "admin", "static", "sdk":
		return c.Status(400).JSON(fiber.Map{"error": "reserved site name"})
	}

	if !h.tryLockName(name) {
		return c.Status(409).JSON(fiber.Map{"error": "deploy already in progress for this name"})
	}
	defer h.unlockName(name)

	// Existence check under lock
	existing, err := db.GetSiteByName(h.DB, name)
	if err == nil && existing != nil {
		return c.Status(409).JSON(fiber.Map{"error": "site already exists; use PUT /deploy/:name to redeploy"})
	}
	if err != nil && err != sql.ErrNoRows {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "missing zip file field 'file'"})
	}
	if fileHeader.Size > h.Cfg.MaxDeploySize {
		return c.Status(413).JSON(fiber.Map{"error": "zip exceeds size limit"})
	}

	ownerToken, err := middleware.GenerateToken()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "token generation failed"})
	}
	siteToken, err := middleware.GenerateToken()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "token generation failed"})
	}
	ownerHash, err := middleware.HashToken(ownerToken)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "token hash failed"})
	}
	siteHash, err := middleware.HashToken(siteToken)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "token hash failed"})
	}

	// Reserve the name in DB first so concurrent deploys fail on UNIQUE
	res, err := h.DB.Exec(`
		INSERT INTO sites (name, owner_token_hash, site_token_hash, owner_ip, created_at, updated_at, is_active)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 1)
	`, name, ownerHash, siteHash, c.IP())
	if err != nil {
		if isUniqueConstraint(err) {
			return c.Status(409).JSON(fiber.Map{"error": "site already exists; use PUT /deploy/:name to redeploy"})
		}
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}
	siteID, _ := res.LastInsertId()

	// Extract to temp dir then rename into place — never RemoveAll a path another request may own
	h.setStatus(name, "extracting")
	tmpDir := filepath.Join(h.Cfg.SitesDir, name+".tmp."+fmt.Sprintf("%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		_, _ = h.DB.Exec(`DELETE FROM sites WHERE id = ?`, siteID)
		h.setStatus(name, "error")
		return c.Status(500).JSON(fiber.Map{"error": "failed to create temp dir"})
	}
	defer os.RemoveAll(tmpDir)

	tmpZip := filepath.Join(os.TempDir(), fmt.Sprintf("quick-deploy-%s-%d.zip", name, time.Now().UnixNano()))
	if err := c.SaveFile(fileHeader, tmpZip); err != nil {
		_, _ = h.DB.Exec(`DELETE FROM sites WHERE id = ?`, siteID)
		h.setStatus(name, "error")
		return c.Status(500).JSON(fiber.Map{"error": "failed to save upload"})
	}
	defer os.Remove(tmpZip)

	if err := extractZipSafe(tmpZip, tmpDir, h.Cfg.MaxDeploySize, h.Cfg.MaxDeployFiles); err != nil {
		_, _ = h.DB.Exec(`DELETE FROM sites WHERE id = ?`, siteID)
		h.setStatus(name, "error")
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "index.html")); err != nil {
		_, _ = h.DB.Exec(`DELETE FROM sites WHERE id = ?`, siteID)
		h.setStatus(name, "error")
		return c.Status(400).JSON(fiber.Map{"error": "zip must contain index.html at root"})
	}

	if err := static.WriteSiteTokenFile(tmpDir, siteToken); err != nil {
		_, _ = h.DB.Exec(`DELETE FROM sites WHERE id = ?`, siteID)
		h.setStatus(name, "error")
		return c.Status(500).JSON(fiber.Map{"error": "failed to write site token"})
	}

	siteDir := filepath.Join(h.Cfg.SitesDir, name)
	// Remove only if empty leftover; under name lock + DB reserve this is ours
	_ = os.RemoveAll(siteDir)
	if err := os.Rename(tmpDir, siteDir); err != nil {
		if err := copyDir(tmpDir, siteDir); err != nil {
			_, _ = h.DB.Exec(`DELETE FROM sites WHERE id = ?`, siteID)
			_ = os.RemoveAll(siteDir)
			h.setStatus(name, "error")
			return c.Status(500).JSON(fiber.Map{"error": "failed to install site files"})
		}
	}

	_ = os.MkdirAll(filepath.Join(h.Cfg.UploadsDir, name), 0o755)

	h.setStatus(name, "ready")
	url := h.Cfg.SiteURL(name)
	return c.Status(201).JSON(fiber.Map{
		"name":        name,
		"url":         url,
		"owner_token": ownerToken,
		"site_token":  siteToken,
		"warning":     "Save these tokens — they will not be shown again",
	})
}

// PUT /deploy/:name — redeploy with owner token
func (h *DeployHandler) Redeploy(c *fiber.Ctx) error {
	name := strings.ToLower(c.Params("name"))
	site, err := db.GetSiteByName(h.DB, name)
	if err == sql.ErrNoRows {
		return c.Status(404).JSON(fiber.Map{"error": "site not found"})
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}

	token := c.Get("X-Quick-Token")
	if token == "" || !middleware.VerifyToken(token, site.OwnerTokenHash) {
		return c.Status(403).JSON(fiber.Map{"error": "invalid owner token"})
	}

	if cl := int64(c.Request().Header.ContentLength()); cl > h.Cfg.MaxDeploySize+1024*1024 {
		return c.Status(413).JSON(fiber.Map{"error": "upload too large"})
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "missing zip file field 'file'"})
	}
	if fileHeader.Size > h.Cfg.MaxDeploySize {
		return c.Status(413).JSON(fiber.Map{"error": "zip exceeds size limit"})
	}

	if !h.tryLockName(name) {
		return c.Status(409).JSON(fiber.Map{"error": "deploy already in progress for this name"})
	}
	defer h.unlockName(name)

	h.setStatus(name, "extracting")
	siteDir := filepath.Join(h.Cfg.SitesDir, name)

	// Preserve or re-issue site token in memory only until install succeeds.
	// Never UPDATE site_token_hash before extract/swap — a failed redeploy
	// must not invalidate the previous hash without returning the new token.
	oldTokenBytes, tokenErr := os.ReadFile(filepath.Join(siteDir, ".quick_site_token"))
	injectToken := strings.TrimSpace(string(oldTokenBytes))
	var newSiteToken, newSiteHash string
	if tokenErr != nil || injectToken == "" {
		tok, err := middleware.GenerateToken()
		if err != nil {
			h.setStatus(name, "error")
			return c.Status(500).JSON(fiber.Map{"error": "token generation failed"})
		}
		hash, err := middleware.HashToken(tok)
		if err != nil {
			h.setStatus(name, "error")
			return c.Status(500).JSON(fiber.Map{"error": "token hash failed"})
		}
		newSiteToken = tok
		newSiteHash = hash
		injectToken = tok
	}

	tmpDir := siteDir + ".tmp." + fmt.Sprintf("%d", time.Now().UnixNano())
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		h.setStatus(name, "error")
		return c.Status(500).JSON(fiber.Map{"error": "failed to create temp dir"})
	}
	defer os.RemoveAll(tmpDir)

	tmpZip := filepath.Join(os.TempDir(), fmt.Sprintf("quick-redeploy-%s-%d.zip", name, time.Now().UnixNano()))
	if err := c.SaveFile(fileHeader, tmpZip); err != nil {
		h.setStatus(name, "error")
		return c.Status(500).JSON(fiber.Map{"error": "failed to save upload"})
	}
	defer os.Remove(tmpZip)

	if err := extractZipSafe(tmpZip, tmpDir, h.Cfg.MaxDeploySize, h.Cfg.MaxDeployFiles); err != nil {
		h.setStatus(name, "error")
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "index.html")); err != nil {
		h.setStatus(name, "error")
		return c.Status(400).JSON(fiber.Map{"error": "zip must contain index.html at root"})
	}

	if err := static.WriteSiteTokenFile(tmpDir, injectToken); err != nil {
		h.setStatus(name, "error")
		return c.Status(500).JSON(fiber.Map{"error": "failed to write site token"})
	}

	// Swap: install new tree without leaving site without token
	backup := siteDir + ".old." + fmt.Sprintf("%d", time.Now().UnixNano())
	_ = os.Rename(siteDir, backup)
	if err := os.Rename(tmpDir, siteDir); err != nil {
		if err := copyDir(tmpDir, siteDir); err != nil {
			// try restore
			_ = os.Rename(backup, siteDir)
			h.setStatus(name, "error")
			return c.Status(500).JSON(fiber.Map{"error": "failed to install site files"})
		}
	}
	_ = os.RemoveAll(backup)

	// Commit hash only after files are live. On DB failure after swap, return the
	// new token so the operator is not locked out of a half-migrated site.
	if newSiteHash != "" {
		if _, err := h.DB.Exec(`UPDATE sites SET site_token_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, newSiteHash, site.ID); err != nil {
			h.setStatus(name, "error")
			return c.Status(500).JSON(fiber.Map{
				"error":      "files installed but failed to update site token hash",
				"site_token": newSiteToken,
				"warning":    "Save site_token — disk has the new token but DB update failed",
			})
		}
	} else {
		_, _ = h.DB.Exec(`UPDATE sites SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, site.ID)
	}
	h.setStatus(name, "ready")

	resp := fiber.Map{
		"name": name,
		"url":  h.Cfg.SiteURL(name),
	}
	if newSiteToken != "" {
		resp["site_token"] = newSiteToken
		resp["warning"] = "Site token was missing and has been re-issued — save the new site_token"
	}
	return c.JSON(resp)
}

// DELETE /sites/:name
func (h *DeployHandler) Delete(c *fiber.Ctx) error {
	name := strings.ToLower(c.Params("name"))
	site, err := db.GetSiteByName(h.DB, name)
	if err == sql.ErrNoRows {
		return c.Status(404).JSON(fiber.Map{"error": "site not found"})
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}

	token := c.Get("X-Quick-Token")
	if token == "" || !middleware.VerifyToken(token, site.OwnerTokenHash) {
		return c.Status(403).JSON(fiber.Map{"error": "invalid owner token"})
	}

	// Same per-name lock as deploy/redeploy to avoid DB/disk desync
	if !h.tryLockName(name) {
		return c.Status(409).JSON(fiber.Map{"error": "deploy or delete already in progress for this name"})
	}
	defer h.unlockName(name)

	if _, err := h.DB.Exec(`DELETE FROM sites WHERE id = ?`, site.ID); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to delete site"})
	}

	_ = os.RemoveAll(filepath.Join(h.Cfg.SitesDir, name))
	_ = os.RemoveAll(filepath.Join(h.Cfg.UploadsDir, name))
	h.delStatus(name)

	return c.JSON(fiber.Map{"deleted": name})
}

// GET /deploy/status/:name
func (h *DeployHandler) StatusHandler(c *fiber.Ctx) error {
	name := strings.ToLower(c.Params("name"))
	status, ok := h.getStatus(name)
	if !ok {
		if _, err := db.GetSiteByName(h.DB, name); err == nil {
			return c.JSON(fiber.Map{"name": name, "status": "ready"})
		}
		return c.Status(404).JSON(fiber.Map{"error": "unknown site"})
	}
	return c.JSON(fiber.Map{"name": name, "status": status})
}

func extractZipSafe(zipPath, dest string, maxSize int64, maxFiles int) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("invalid zip: %w", err)
	}
	defer r.Close()

	if len(r.File) > maxFiles {
		return fmt.Errorf("too many files in zip (max %d)", maxFiles)
	}

	var total int64
	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}

	for _, f := range r.File {
		name := f.Name
		name = strings.ReplaceAll(name, "\\", "/")
		if strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
			return fmt.Errorf("illegal path in zip: %s", f.Name)
		}
		clean := filepath.Clean(name)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("zip-slip rejected: %s", f.Name)
		}
		if strings.HasSuffix(name, "/") || f.FileInfo().IsDir() {
			continue
		}
		if filepath.Base(clean) == ".quick_site_token" {
			continue
		}

		target := filepath.Join(destAbs, clean)
		targetAbs, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		if targetAbs != destAbs && !strings.HasPrefix(targetAbs, destAbs+string(os.PathSeparator)) {
			return fmt.Errorf("zip-slip rejected: %s", f.Name)
		}

		// Fast-path reject absurd declared sizes
		if f.UncompressedSize64 > uint64(maxSize) {
			return fmt.Errorf("file too large in zip: %s", f.Name)
		}

		remaining := maxSize - total
		if remaining <= 0 {
			return fmt.Errorf("extracted size exceeds limit")
		}

		if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(targetAbs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return err
		}
		// Count actual bytes written, not declared UncompressedSize64 (zip-bomb defense)
		written, err := io.Copy(out, io.LimitReader(rc, remaining+1))
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
		if written > remaining {
			_ = os.Remove(targetAbs)
			return fmt.Errorf("extracted size exceeds limit")
		}
		total += written
	}
	return nil
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique") ||
		strings.Contains(s, "constraint failed") ||
		strings.Contains(s, "already exists")
}

func randomSiteName() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	tok, err := middleware.GenerateToken()
	if err != nil {
		return "", err
	}
	for i := 0; i < 8; i++ {
		b[i] = alphabet[int(tok[i%len(tok)])%len(alphabet)]
	}
	if b[0] >= '0' && b[0] <= '9' {
		b[0] = 'a' + (b[0] - '0')
	}
	return string(b), nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
