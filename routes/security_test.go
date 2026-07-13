package routes

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dadyprojects/quick/config"
	"github.com/dadyprojects/quick/db"
	"github.com/dadyprojects/quick/middleware"
	"github.com/dadyprojects/quick/static"
	"github.com/gofiber/fiber/v2"
)

func testCfg(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{
		Env:            "development",
		Port:           "0",
		DBPath:         filepath.Join(root, "test.db"),
		SitesDir:       filepath.Join(root, "sites"),
		UploadsDir:     filepath.Join(root, "uploads"),
		BaseDomain:     "quick.dadyprojects.tech",
		MaxFileSize:    10 * 1024 * 1024,
		MaxSiteQuota:   500 * 1024 * 1024,
		MaxKVValue:     256 * 1024,
		MaxDeploySize:  50 * 1024 * 1024,
		MaxDeployFiles: 500,
		AIMaxTokens:    1024,
		AIRateLimit:    10,
		DeployRateIP:   100,
	}
	_ = os.MkdirAll(cfg.SitesDir, 0o755)
	_ = os.MkdirAll(cfg.UploadsDir, 0o755)
	return cfg
}

func zipIndex(t *testing.T, html string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("index.html")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(html))
	_ = zw.Close()
	return buf.Bytes()
}

func multipartZip(t *testing.T, name string, zipData []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if name != "" {
		_ = w.WriteField("name", name)
	}
	part, err := w.CreateFormFile("file", "site.zip")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(zipData)
	_ = w.Close()
	return &body, w.FormDataContentType()
}

func setupApp(t *testing.T) (*fiber.App, *config.Config) {
	t.Helper()
	cfg := testCfg(t)
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	app := fiber.New()
	app.Use(middleware.SiteResolver(database, cfg.BaseDomain))
	deployH := NewDeployHandler(database, cfg)
	dataH := NewDataHandler(database, cfg)
	aiH := NewAIHandler(database, cfg)

	app.Post("/deploy", deployH.Deploy)
	app.Put("/deploy/:name", deployH.Redeploy)
	app.Delete("/sites/:name", deployH.Delete)

	app.Post("/s/:name/api/data/:key", middleware.RequireSite(), middleware.AuthMiddleware("site"), dataH.Set)
	app.Get("/s/:name/api/data/:key", middleware.RequireSite(), middleware.AuthMiddleware("site"), dataH.Get)
	app.Post("/s/:name/api/ai", middleware.RequireSite(), middleware.AuthMiddleware("site"), aiH.Proxy)
	app.Get("/s/:name/*", middleware.RequireSite(), static.Serve(cfg.SitesDir))
	app.Get("/s/:name", middleware.RequireSite(), static.Serve(cfg.SitesDir))

	return app, cfg
}

func TestOwnerVsSiteTokenDelete(t *testing.T) {
	app, _ := setupApp(t)
	zipData := zipIndex(t, "<html><body>hi</body></html>")
	body, ct := multipartZip(t, "tokentest", zipData)
	req := httptest.NewRequest("POST", "/deploy", body)
	req.Header.Set("Content-Type", ct)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("deploy %d: %s", resp.StatusCode, b)
	}
	var out struct {
		OwnerToken string `json:"owner_token"`
		SiteToken  string `json:"site_token"`
		Name       string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest("DELETE", "/sites/"+out.Name, nil)
	req.Header.Set("X-Quick-Token", out.SiteToken)
	resp, err = app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("site token delete: want 403 got %d", resp.StatusCode)
	}

	req = httptest.NewRequest("DELETE", "/sites/"+out.Name, nil)
	req.Header.Set("X-Quick-Token", out.OwnerToken)
	resp, err = app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("owner delete: want 200 got %d %s", resp.StatusCode, b)
	}
}

func TestCrossSiteKVIsolation(t *testing.T) {
	app, _ := setupApp(t)
	zipData := zipIndex(t, "<html>a</html>")

	deploy := func(name string) string {
		body, ct := multipartZip(t, name, zipData)
		req := httptest.NewRequest("POST", "/deploy", body)
		req.Header.Set("Content-Type", ct)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 201 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("deploy %s: %d %s", name, resp.StatusCode, b)
		}
		var out struct {
			SiteToken string `json:"site_token"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out.SiteToken
	}

	tokA := deploy("sitea")
	tokB := deploy("siteb")

	req := httptest.NewRequest("POST", "/s/sitea/api/data/secret", strings.NewReader(`{"v":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Quick-Token", tokA)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("set: %d %s", resp.StatusCode, b)
	}

	req = httptest.NewRequest("GET", "/s/sitea/api/data/secret", nil)
	req.Header.Set("X-Quick-Token", tokB)
	resp, err = app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("cross-site read: want 403 got %d", resp.StatusCode)
	}

	req = httptest.NewRequest("GET", "/s/sitea/api/data/secret", nil)
	req.Header.Set("X-Quick-Token", tokA)
	resp, err = app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("own read: want 200 got %d", resp.StatusCode)
	}
}

func TestStaticBlocksQuickTokenAndInjects(t *testing.T) {
	app, cfg := setupApp(t)
	zipData := zipIndex(t, "<html><head></head><body>ok</body></html>")
	body, ct := multipartZip(t, "statictest", zipData)
	req := httptest.NewRequest("POST", "/deploy", body)
	req.Header.Set("Content-Type", ct)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("deploy: %d %s", resp.StatusCode, b)
	}

	nested := filepath.Join(cfg.SitesDir, "statictest", "assets", ".quick_site_token")
	_ = os.MkdirAll(filepath.Dir(nested), 0o755)
	_ = os.WriteFile(nested, []byte("secret"), 0o600)

	req = httptest.NewRequest("GET", "/s/statictest/assets/.quick_site_token", nil)
	resp, err = app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("nested token: want 404 got %d", resp.StatusCode)
	}

	req = httptest.NewRequest("GET", "/s/statictest/", nil)
	resp, err = app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(b, []byte("__QUICK_TOKEN__")) {
		t.Fatalf("expected token inject: %s", b)
	}
}

func TestAIRejectsModelAndMissingKey(t *testing.T) {
	app, _ := setupApp(t)
	zipData := zipIndex(t, "<html>x</html>")
	body, ct := multipartZip(t, "aitest", zipData)
	req := httptest.NewRequest("POST", "/deploy", body)
	req.Header.Set("Content-Type", ct)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		SiteToken string `json:"site_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)

	req = httptest.NewRequest("POST", "/s/aitest/api/ai", strings.NewReader(`{"prompt":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Quick-Token", out.SiteToken)
	resp, err = app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 503 {
		t.Fatalf("want 503 got %d", resp.StatusCode)
	}

	cfg := testCfg(t)
	cfg.AnthropicKey = "sk-test"
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	app2 := fiber.New()
	app2.Use(middleware.SiteResolver(database, cfg.BaseDomain))
	dh := NewDeployHandler(database, cfg)
	aiH := NewAIHandler(database, cfg)
	app2.Post("/deploy", dh.Deploy)
	app2.Post("/s/:name/api/ai", middleware.RequireSite(), middleware.AuthMiddleware("site"), aiH.Proxy)

	body, ct = multipartZip(t, "aitest2", zipData)
	req = httptest.NewRequest("POST", "/deploy", body)
	req.Header.Set("Content-Type", ct)
	resp, err = app2.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)

	req = httptest.NewRequest("POST", "/s/aitest2/api/ai", strings.NewReader(`{"prompt":"hi","model":"evil"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Quick-Token", out.SiteToken)
	resp, err = app2.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("model override want 400 got %d %s", resp.StatusCode, b)
	}
}

func TestKVRejectsNonJSON(t *testing.T) {
	app, _ := setupApp(t)
	zipData := zipIndex(t, "<html>x</html>")
	body, ct := multipartZip(t, "kvjson", zipData)
	req := httptest.NewRequest("POST", "/deploy", body)
	req.Header.Set("Content-Type", ct)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		SiteToken string `json:"site_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)

	req = httptest.NewRequest("POST", "/s/kvjson/api/data/k", strings.NewReader(`not-json`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Quick-Token", out.SiteToken)
	resp, err = app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("want 400 got %d", resp.StatusCode)
	}
}
