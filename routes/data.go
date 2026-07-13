package routes

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"time"

	"github.com/dadyprojects/quick/config"
	"github.com/dadyprojects/quick/db"
	"github.com/gofiber/fiber/v2"
)

type DataHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

func NewDataHandler(database *sql.DB, cfg *config.Config) *DataHandler {
	return &DataHandler{DB: database, Cfg: cfg}
}

// POST /api/data/:key
func (h *DataHandler) Set(c *fiber.Ctx) error {
	site := c.Locals("site").(*db.Site)
	key := c.Params("key")
	if key == "" || len(key) > 256 {
		return c.Status(400).JSON(fiber.Map{"error": "invalid key"})
	}

	if cl := int64(c.Request().Header.ContentLength()); cl > h.Cfg.MaxKVValue {
		return c.Status(413).JSON(fiber.Map{"error": "value exceeds 256KB limit"})
	}

	body := c.Body()
	if int64(len(body)) > h.Cfg.MaxKVValue {
		return c.Status(413).JSON(fiber.Map{"error": "value exceeds 256KB limit"})
	}
	if len(body) == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "empty body"})
	}
	if !json.Valid(body) {
		return c.Status(400).JSON(fiber.Map{"error": "value must be valid JSON"})
	}

	// Serialize with file uploads for combined soft quota
	mu := siteQuotaLock(site.ID)
	mu.Lock()
	defer mu.Unlock()

	var filesTotal, kvTotal sql.NullInt64
	if err := h.DB.QueryRow(`SELECT COALESCE(SUM(size_bytes), 0) FROM files WHERE site_id = ?`, site.ID).Scan(&filesTotal); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "quota check failed"})
	}
	if err := h.DB.QueryRow(`SELECT COALESCE(SUM(LENGTH(value)), 0) FROM kv WHERE site_id = ? AND key != ?`, site.ID, key).Scan(&kvTotal); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "quota check failed"})
	}
	if filesTotal.Int64+kvTotal.Int64+int64(len(body)) > h.Cfg.MaxSiteQuota {
		return c.Status(413).JSON(fiber.Map{"error": "site storage quota exceeded"})
	}

	value := string(body)
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := h.DB.Exec(`
		INSERT INTO kv (site_id, key, value, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(site_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, site.ID, key, value, now, now)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to save"})
	}

	return c.JSON(fiber.Map{"key": key, "ok": true})
}

// GET /api/data/:key
func (h *DataHandler) Get(c *fiber.Ctx) error {
	site := c.Locals("site").(*db.Site)
	key := c.Params("key")

	var value string
	err := h.DB.QueryRow(`SELECT value FROM kv WHERE site_id = ? AND key = ?`, site.ID, key).Scan(&value)
	if err == sql.ErrNoRows {
		return c.Status(404).JSON(fiber.Map{"error": "key not found"})
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}

	// Marshal safely: value was validated as JSON on write
	type resp struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	raw := json.RawMessage(value)
	if !json.Valid(raw) {
		// Legacy/corrupt: return as JSON string
		b, _ := json.Marshal(map[string]any{"key": key, "value": value})
		c.Set("Content-Type", "application/json")
		return c.Send(b)
	}
	b, err := json.Marshal(resp{Key: key, Value: raw})
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "encode error"})
	}
	c.Set("Content-Type", "application/json")
	return c.Send(b)
}

// DELETE /api/data/:key
func (h *DataHandler) Delete(c *fiber.Ctx) error {
	site := c.Locals("site").(*db.Site)
	key := c.Params("key")

	res, err := h.DB.Exec(`DELETE FROM kv WHERE site_id = ? AND key = ?`, site.ID, key)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return c.Status(404).JSON(fiber.Map{"error": "key not found"})
	}
	return c.JSON(fiber.Map{"deleted": key})
}

// GET /api/data — list keys with pagination
func (h *DataHandler) List(c *fiber.Ctx) error {
	site := c.Locals("site").(*db.Site)
	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	cursor := c.Query("cursor")

	var rows *sql.Rows
	var err error
	if cursor != "" {
		rows, err = h.DB.Query(`
			SELECT key, value FROM kv
			WHERE site_id = ? AND key > ?
			ORDER BY key ASC LIMIT ?
		`, site.ID, cursor, limit)
	} else {
		rows, err = h.DB.Query(`
			SELECT key, value FROM kv
			WHERE site_id = ?
			ORDER BY key ASC LIMIT ?
		`, site.ID, limit)
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}
	defer rows.Close()

	items := make([]fiber.Map, 0)
	var lastKey string
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "scan error"})
		}
		if json.Valid([]byte(v)) {
			items = append(items, fiber.Map{"key": k, "value": json.RawMessage(v)})
		} else {
			items = append(items, fiber.Map{"key": k, "value": v})
		}
		lastKey = k
	}

	resp := fiber.Map{"keys": items, "count": len(items)}
	if len(items) == limit && lastKey != "" {
		resp["next_cursor"] = lastKey
	}
	return c.JSON(resp)
}
