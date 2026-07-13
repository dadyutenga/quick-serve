package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Env            string
	Port           string
	DBPath         string
	SitesDir       string
	UploadsDir     string
	BaseDomain     string
	AnthropicKey   string
	AnthropicModel string
	MaxFileSize    int64
	MaxSiteQuota   int64
	MaxKVValue     int64
	MaxDeploySize  int64
	MaxDeployFiles int
	AIMaxTokens    int
	AIRateLimit    int
	DeployRateIP   int
	ServerURL      string
	// TrustProxy enables Fiber ProxyHeader (X-Forwarded-For). Only set behind nginx.
	TrustProxy bool
}

func Load() *Config {
	return &Config{
		Env:            getEnv("QUICK_ENV", "development"),
		Port:           getEnv("QUICK_PORT", "8080"),
		DBPath:         getEnv("QUICK_DB_PATH", "./data/quick.db"),
		SitesDir:       getEnv("QUICK_SITES_DIR", "./sites"),
		UploadsDir:     getEnv("QUICK_UPLOADS_DIR", "./uploads"),
		BaseDomain:     getEnv("QUICK_BASE_DOMAIN", "quick.dadyprojects.tech"),
		AnthropicKey:   getEnv("ANTHROPIC_API_KEY", ""),
		AnthropicModel: getEnv("ANTHROPIC_MODEL", "claude-sonnet-4-20250514"),
		MaxFileSize:    getEnvInt64("QUICK_MAX_FILE_SIZE", 10*1024*1024),   // 10MB
		MaxSiteQuota:   getEnvInt64("QUICK_MAX_SITE_QUOTA", 500*1024*1024), // 500MB
		MaxKVValue:     getEnvInt64("QUICK_MAX_KV_VALUE", 256*1024),        // 256KB
		MaxDeploySize:  getEnvInt64("QUICK_MAX_DEPLOY_SIZE", 50*1024*1024), // 50MB
		MaxDeployFiles: int(getEnvInt64("QUICK_MAX_DEPLOY_FILES", 500)),
		AIMaxTokens:    int(getEnvInt64("QUICK_AI_MAX_TOKENS", 1024)),
		AIRateLimit:    int(getEnvInt64("QUICK_AI_RATE_LIMIT", 10)), // per minute
		DeployRateIP:   int(getEnvInt64("QUICK_DEPLOY_RATE_IP", 5)), // per hour
		ServerURL:      getEnv("QUICK_SERVER_URL", "https://quick.dadyprojects.tech"),
		TrustProxy:     getEnvBool("QUICK_TRUST_PROXY", false),
	}
}

func getEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

func (c *Config) IsProduction() bool {
	return strings.EqualFold(c.Env, "production")
}

func (c *Config) SiteURL(name string) string {
	if c.IsProduction() || strings.Contains(c.BaseDomain, ".") {
		scheme := "https"
		if !c.IsProduction() {
			scheme = "http"
		}
		// In production use subdomain; in dev still show subdomain form
		if c.IsProduction() {
			return scheme + "://" + name + "." + c.BaseDomain
		}
	}
	// Development: path-based or host:port with subdomain simulation
	return "http://localhost:" + c.Port + "/s/" + name
}
