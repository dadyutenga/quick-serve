package routes

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dadyprojects/quick/config"
	"github.com/dadyprojects/quick/db"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type FilesHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

func NewFilesHandler(database *sql.DB, cfg *config.Config) *FilesHandler {
	return &FilesHandler{DB: database, Cfg: cfg}
}

// siteAPIPrefix returns /s/<name> when request is path-based, else "".
func siteAPIPrefix(c *fiber.Ctx) string {
	path := c.Path()
	if strings.HasPrefix(path, "/s/") {
		rest := strings.TrimPrefix(path, "/s/")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) > 0 && parts[0] != "" {
			return "/s/" + parts[0]
		}
	}
	return ""
}

// POST /api/files
func (h *FilesHandler) Upload(c *fiber.Ctx) error {
	site := c.Locals("site").(*db.Site)

	if cl := int64(c.Request().Header.ContentLength()); cl > h.Cfg.MaxFileSize+1024*1024 {
		return c.Status(413).JSON(fiber.Map{"error": "file too large"})
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "missing form field 'file'"})
	}
	if fileHeader.Size > h.Cfg.MaxFileSize {
		return c.Status(413).JSON(fiber.Map{"error": fmt.Sprintf("file exceeds %d byte limit", h.Cfg.MaxFileSize)})
	}

	// Shared with KV Set via siteQuotaLock
	mu := siteQuotaLock(site.ID)
	mu.Lock()
	defer mu.Unlock()

	// Combined files + kv quota
	used, err := h.siteStorageBytes(site.ID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "quota check failed"})
	}
	if used+fileHeader.Size > h.Cfg.MaxSiteQuota {
		return c.Status(413).JSON(fiber.Map{"error": "site storage quota exceeded"})
	}

	origName := filepath.Base(fileHeader.Filename)
	ext := filepath.Ext(origName)
	ext = strings.ToLower(ext)
	if len(ext) > 16 {
		ext = ""
	}
	filename := uuid.New().String() + ext

	siteUploadDir := filepath.Join(h.Cfg.UploadsDir, site.Name)
	if err := os.MkdirAll(siteUploadDir, 0o755); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to create upload dir"})
	}

	destPath := filepath.Join(siteUploadDir, filename)
	destAbs, _ := filepath.Abs(destPath)
	dirAbs, _ := filepath.Abs(siteUploadDir)
	if !strings.HasPrefix(destAbs, dirAbs+string(os.PathSeparator)) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid path"})
	}

	if err := c.SaveFile(fileHeader, destPath); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to save file"})
	}

	mime := fileHeader.Header.Get("Content-Type")
	if mime == "" {
		mime = "application/octet-stream"
	}

	_, err = h.DB.Exec(`
		INSERT INTO files (site_id, filename, original_name, mime_type, size_bytes, created_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, site.ID, filename, origName, mime, fileHeader.Size)
	if err != nil {
		_ = os.Remove(destPath)
		return c.Status(500).JSON(fiber.Map{"error": "failed to record file"})
	}

	url := siteAPIPrefix(c) + "/api/files/" + filename
	return c.Status(201).JSON(fiber.Map{
		"url":      url,
		"filename": filename,
		"size":     fileHeader.Size,
		"mime":     mime,
	})
}

// GET /api/files/:filename
func (h *FilesHandler) Download(c *fiber.Ctx) error {
	site := c.Locals("site").(*db.Site)
	filename := filepath.Base(c.Params("filename"))
	if filename == "" || filename == "." || strings.Contains(filename, "..") {
		return c.Status(400).JSON(fiber.Map{"error": "invalid filename"})
	}

	var mime string
	var original string
	err := h.DB.QueryRow(`
		SELECT mime_type, original_name FROM files WHERE site_id = ? AND filename = ?
	`, site.ID, filename).Scan(&mime, &original)
	if err == sql.ErrNoRows {
		return c.Status(404).JSON(fiber.Map{"error": "file not found"})
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}

	path := filepath.Join(h.Cfg.UploadsDir, site.Name, filename)
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid path"})
	}
	rootAbs, _ := filepath.Abs(filepath.Join(h.Cfg.UploadsDir, site.Name))
	if pathAbs != rootAbs && !strings.HasPrefix(pathAbs, rootAbs+string(os.PathSeparator)) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid path"})
	}

	if mime != "" {
		c.Set("Content-Type", mime)
	}
	if original != "" {
		c.Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, sanitizeHeader(original)))
	}
	return c.SendFile(pathAbs, false)
}

// DELETE /api/files/:filename
func (h *FilesHandler) Delete(c *fiber.Ctx) error {
	site := c.Locals("site").(*db.Site)
	filename := filepath.Base(c.Params("filename"))

	res, err := h.DB.Exec(`DELETE FROM files WHERE site_id = ? AND filename = ?`, site.ID, filename)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return c.Status(404).JSON(fiber.Map{"error": "file not found"})
	}

	_ = os.Remove(filepath.Join(h.Cfg.UploadsDir, site.Name, filename))
	return c.JSON(fiber.Map{"deleted": filename})
}

// GET /api/files
func (h *FilesHandler) List(c *fiber.Ctx) error {
	site := c.Locals("site").(*db.Site)
	prefix := siteAPIPrefix(c)
	rows, err := h.DB.Query(`
		SELECT filename, original_name, mime_type, size_bytes, created_at
		FROM files WHERE site_id = ? ORDER BY created_at DESC
	`, site.ID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}
	defer rows.Close()

	files := make([]fiber.Map, 0)
	for rows.Next() {
		var fn, orig, mime, created string
		var size int64
		if err := rows.Scan(&fn, &orig, &mime, &size, &created); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "scan error"})
		}
		files = append(files, fiber.Map{
			"filename":      fn,
			"original_name": orig,
			"mime_type":     mime,
			"size":          size,
			"url":           prefix + "/api/files/" + fn,
			"created_at":    created,
		})
	}
	return c.JSON(fiber.Map{"files": files})
}

func (h *FilesHandler) siteStorageBytes(siteID int64) (int64, error) {
	var filesTotal, kvTotal sql.NullInt64
	err := h.DB.QueryRow(`SELECT COALESCE(SUM(size_bytes), 0) FROM files WHERE site_id = ?`, siteID).Scan(&filesTotal)
	if err != nil {
		return 0, err
	}
	err = h.DB.QueryRow(`SELECT COALESCE(SUM(LENGTH(value)), 0) FROM kv WHERE site_id = ?`, siteID).Scan(&kvTotal)
	if err != nil {
		return 0, err
	}
	return filesTotal.Int64 + kvTotal.Int64, nil
}

func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, `"`, "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}
