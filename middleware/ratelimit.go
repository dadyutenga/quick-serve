package middleware

import (
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

type windowEntry struct {
	times []time.Time
}

// RateLimiter is a simple in-memory sliding window limiter.
type RateLimiter struct {
	mu        sync.Mutex
	windows   map[string]*windowEntry
	limit     int
	window    time.Duration
	lastClean time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		windows: make(map[string]*windowEntry),
		limit:   limit,
		window:  window,
	}
}

func (r *RateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	if now.Sub(r.lastClean) > r.window {
		r.cleanup(now)
		r.lastClean = now
	}

	e, ok := r.windows[key]
	if !ok {
		r.windows[key] = &windowEntry{times: []time.Time{now}}
		return true
	}

	cutoff := now.Add(-r.window)
	valid := e.times[:0]
	for _, t := range e.times {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	e.times = valid

	if len(e.times) >= r.limit {
		return false
	}
	e.times = append(e.times, now)
	return true
}

func (r *RateLimiter) cleanup(now time.Time) {
	cutoff := now.Add(-r.window)
	for k, e := range r.windows {
		valid := e.times[:0]
		for _, t := range e.times {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}
		if len(valid) == 0 {
			delete(r.windows, k)
		} else {
			e.times = valid
		}
	}
}

// RateLimitMiddleware limits by a key function (e.g. site id or IP).
func RateLimitMiddleware(limiter *RateLimiter, keyFn func(*fiber.Ctx) string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		key := keyFn(c)
		if key == "" {
			key = c.IP()
		}
		if !limiter.Allow(key) {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "rate limit exceeded",
			})
		}
		return c.Next()
	}
}

func SiteKey(c *fiber.Ctx) string {
	if s, ok := c.Locals("site_name").(string); ok && s != "" {
		return "site:" + s
	}
	return "ip:" + c.IP()
}

func IPKey(c *fiber.Ctx) string {
	return "ip:" + c.IP()
}
