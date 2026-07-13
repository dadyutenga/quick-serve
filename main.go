package main

import (
	_ "embed"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/dadyprojects/quick/config"
	"github.com/dadyprojects/quick/db"
	"github.com/dadyprojects/quick/middleware"
	"github.com/dadyprojects/quick/routes"
	"github.com/dadyprojects/quick/static"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
)

// Embedded for single-binary / scratch Docker (no loose files required).
//
//go:embed sdk.js
var sdkJS []byte

//go:embed web/index.html
var webConsole []byte

func main() {
	cfg := config.Load()

	if err := os.MkdirAll(cfg.SitesDir, 0o755); err != nil {
		log.Fatalf("sites dir: %v", err)
	}
	if err := os.MkdirAll(cfg.UploadsDir, 0o755); err != nil {
		log.Fatalf("uploads dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		log.Fatalf("db dir: %v", err)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer database.Close()

	fiberCfg := fiber.Config{
		BodyLimit:             int(cfg.MaxDeploySize + 2*1024*1024),
		DisableStartupMessage: false,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			msg := "internal server error"
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
				// Client errors may expose fiber message; keep generic for 5xx
				if code < 500 {
					msg = e.Message
				} else {
					log.Printf("request %s error: %v", c.Locals("requestid"), err)
				}
			} else {
				log.Printf("request %s error: %v", c.Locals("requestid"), err)
			}
			return c.Status(code).JSON(fiber.Map{"error": msg})
		},
	}
	// Only trust X-Forwarded-For when explicitly enabled (behind nginx).
	// Do not infer from QUICK_ENV=production alone — a public :8080 with
	// production env would allow client-spoofed XFF to bypass deploy rate limits.
	// systemd unit sets QUICK_TRUST_PROXY=1; nginx overwrites X-Forwarded-For.
	if cfg.TrustProxy {
		fiberCfg.ProxyHeader = fiber.HeaderXForwardedFor
	}

	app := fiber.New(fiberCfg)

	app.Use(recover.New())
	app.Use(requestid.New())
	app.Use(logger.New(logger.Config{
		// Do not log Authorization / X-Quick-Token headers or full query strings
		// (WS tokens may appear as ?token= — use path only)
		Format: "${time} ${status} ${method} ${path} ${latency} ${ip}\n",
	}))
	app.Use(middleware.CORS(cfg.BaseDomain))

	app.Use(middleware.SiteResolver(database, cfg.BaseDomain))

	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"ok": true})
	})

	app.Get("/sdk.js", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "application/javascript; charset=utf-8")
		c.Set("Cache-Control", "public, max-age=3600")
		return c.Send(sdkJS)
	})

	// Web console for browser deploy (upload zip / folder)
	serveConsole := func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/html; charset=utf-8")
		c.Set("Cache-Control", "no-cache")
		return c.Send(webConsole)
	}
	app.Get("/console", serveConsole)

	deployH := routes.NewDeployHandler(database, cfg)
	deployLimiter := middleware.NewRateLimiter(cfg.DeployRateIP, time.Hour)
	app.Post("/deploy",
		middleware.RateLimitMiddleware(deployLimiter, middleware.IPKey),
		deployH.Deploy,
	)
	app.Put("/deploy/:name", deployH.Redeploy)
	app.Delete("/sites/:name", deployH.Delete)
	app.Get("/deploy/status/:name", deployH.StatusHandler)

	dataH := routes.NewDataHandler(database, cfg)
	filesH := routes.NewFilesHandler(database, cfg)
	aiH := routes.NewAIHandler(database, cfg)
	hub := routes.NewHub()

	api := app.Group("/api", middleware.RequireSite())

	api.Post("/data/:key", middleware.AuthMiddleware("site"), dataH.Set)
	api.Get("/data/:key", middleware.AuthMiddleware("site"), dataH.Get)
	api.Delete("/data/:key", middleware.AuthMiddleware("site"), dataH.Delete)
	api.Get("/data", middleware.AuthMiddleware("site"), dataH.List)

	api.Post("/files", middleware.AuthMiddleware("site"), filesH.Upload)
	api.Get("/files", middleware.AuthMiddleware("site"), filesH.List)
	api.Delete("/files/:filename", middleware.AuthMiddleware("site"), filesH.Delete)
	api.Get("/files/:filename", filesH.Download)

	api.Post("/ai", middleware.AuthMiddleware("site"), aiH.Proxy)

	app.Use("/api/ws", middleware.RequireSite(), middleware.AuthMiddleware("site"), routes.WSUpgrade)
	app.Get("/api/ws", websocket.New(hub.HandleWS, websocket.Config{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}))

	sAPI := app.Group("/s/:name/api", func(c *fiber.Ctx) error {
		if c.Locals("site") == nil {
			return c.Status(404).JSON(fiber.Map{"error": "site not found"})
		}
		return c.Next()
	})
	sAPI.Post("/data/:key", middleware.AuthMiddleware("site"), dataH.Set)
	sAPI.Get("/data/:key", middleware.AuthMiddleware("site"), dataH.Get)
	sAPI.Delete("/data/:key", middleware.AuthMiddleware("site"), dataH.Delete)
	sAPI.Get("/data", middleware.AuthMiddleware("site"), dataH.List)
	sAPI.Post("/files", middleware.AuthMiddleware("site"), filesH.Upload)
	sAPI.Get("/files", middleware.AuthMiddleware("site"), filesH.List)
	sAPI.Delete("/files/:filename", middleware.AuthMiddleware("site"), filesH.Delete)
	sAPI.Get("/files/:filename", filesH.Download)
	sAPI.Post("/ai", middleware.AuthMiddleware("site"), aiH.Proxy)
	app.Use("/s/:name/api/ws", middleware.RequireSite(), middleware.AuthMiddleware("site"), routes.WSUpgrade)
	app.Get("/s/:name/api/ws", websocket.New(hub.HandleWS, websocket.Config{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}))

	serve := static.Serve(cfg.SitesDir)
	app.Get("/s/:name", serve)
	app.Get("/s/:name/*", serve)
	// Apex dashboard (no subdomain) — browser deploy UI
	app.Get("/", func(c *fiber.Ctx) error {
		if c.Locals("site") != nil {
			return serve(c)
		}
		return serveConsole(c)
	})
	app.Get("/*", func(c *fiber.Ctx) error {
		if c.Locals("site") == nil {
			return c.Status(404).JSON(fiber.Map{"error": "not found"})
		}
		return serve(c)
	})

	addr := ":" + cfg.Port
	if !cfg.TrustProxy {
		log.Printf("quick: ProxyHeader disabled (set QUICK_TRUST_PROXY=1 behind nginx; never expose :8080 publicly with trust on)")
	}
	log.Printf("quick server listening on %s (env=%s domain=%s)", addr, cfg.Env, cfg.BaseDomain)
	if err := app.Listen(addr); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
