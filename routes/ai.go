package routes

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dadyprojects/quick/config"
	"github.com/dadyprojects/quick/db"
	"github.com/dadyprojects/quick/middleware"
	"github.com/gofiber/fiber/v2"
)

type AIHandler struct {
	DB      *sql.DB
	Cfg     *config.Config
	Limiter *middleware.RateLimiter
	Client  *http.Client
}

func NewAIHandler(database *sql.DB, cfg *config.Config) *AIHandler {
	return &AIHandler{
		DB:      database,
		Cfg:     cfg,
		Limiter: middleware.NewRateLimiter(cfg.AIRateLimit, time.Minute),
		Client:  &http.Client{Timeout: 60 * time.Second},
	}
}

type aiRequest struct {
	Prompt    string `json:"prompt"`
	System    string `json:"system"`
	MaxTokens int    `json:"max_tokens"`
	// Model intentionally ignored / rejected
	Model string `json:"model"`
}

// POST /api/ai
func (h *AIHandler) Proxy(c *fiber.Ctx) error {
	site := c.Locals("site").(*db.Site)

	// Validate config and body before consuming rate-limit budget
	if h.Cfg.AnthropicKey == "" {
		return c.Status(503).JSON(fiber.Map{"error": "AI not configured: ANTHROPIC_API_KEY is missing"})
	}

	const maxBody = 100 * 1024 // 100KB
	if cl := c.Request().Header.ContentLength(); cl > maxBody {
		return c.Status(413).JSON(fiber.Map{"error": "request body too large"})
	}
	body := c.Body()
	if len(body) > maxBody {
		return c.Status(413).JSON(fiber.Map{"error": "request body too large"})
	}

	var req aiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid JSON body"})
	}
	if req.Model != "" {
		return c.Status(400).JSON(fiber.Map{"error": "model override is not allowed"})
	}
	if stringsTrim(req.Prompt) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "prompt is required"})
	}
	if len(req.Prompt) > 50_000 {
		return c.Status(413).JSON(fiber.Map{"error": "prompt too long"})
	}

	// Rate limit only valid attempts that would hit the provider
	if !h.Limiter.Allow(fmt.Sprintf("ai:%d", site.ID)) {
		return c.Status(429).JSON(fiber.Map{"error": "rate limit exceeded (max 10/min per site)"})
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 512
	}
	if maxTokens > h.Cfg.AIMaxTokens {
		maxTokens = h.Cfg.AIMaxTokens
	}

	messages := []map[string]any{
		{"role": "user", "content": req.Prompt},
	}
	payload := map[string]any{
		"model":      h.Cfg.AnthropicModel,
		"max_tokens": maxTokens,
		"messages":   messages,
	}
	if req.System != "" {
		payload["system"] = req.System
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to build request"})
	}

	httpReq, err := http.NewRequestWithContext(c.Context(), http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(payloadBytes))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to create request"})
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", h.Cfg.AnthropicKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := h.Client.Do(httpReq)
	if err != nil {
		return c.Status(502).JSON(fiber.Map{"error": "AI provider request failed"})
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return c.Status(502).JSON(fiber.Map{"error": "failed to read AI response"})
	}

	if resp.StatusCode >= 400 {
		return c.Status(502).JSON(fiber.Map{
			"error":  "AI provider error",
			"status": resp.StatusCode,
		})
	}

	var anthropicResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &anthropicResp); err != nil {
		return c.Status(502).JSON(fiber.Map{"error": "invalid AI response"})
	}

	var text string
	for _, part := range anthropicResp.Content {
		if part.Type == "text" {
			text += part.Text
		}
	}

	_, _ = h.DB.Exec(`
		INSERT INTO ai_usage (site_id, tokens_in, tokens_out, created_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	`, site.ID, anthropicResp.Usage.InputTokens, anthropicResp.Usage.OutputTokens)

	return c.JSON(fiber.Map{"text": text})
}

// ClampMaxTokens is exported for tests.
func ClampMaxTokens(requested, hardCap int) int {
	if requested <= 0 {
		return 512
	}
	if requested > hardCap {
		return hardCap
	}
	return requested
}

func stringsTrim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 {
		last := s[len(s)-1]
		if last == ' ' || last == '\t' || last == '\n' || last == '\r' {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}
