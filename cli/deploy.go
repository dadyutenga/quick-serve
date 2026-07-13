package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func cmdDeploy(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: quick deploy <folder> [--name <name>]")
	}
	folder := args[0]
	name := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--name" && i+1 < len(args) {
			name = args[i+1]
			i++
		}
	}

	info, err := os.Stat(folder)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("folder not found: %s", folder)
	}
	indexPath := filepath.Join(folder, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		return fmt.Errorf("index.html not found in %s", folder)
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	zipData, err := zipFolder(folder)
	if err != nil {
		return fmt.Errorf("zip: %w", err)
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if name != "" {
		_ = w.WriteField("name", name)
	}
	part, err := w.CreateFormFile("file", "site.zip")
	if err != nil {
		return err
	}
	if _, err := part.Write(zipData); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	url := strings.TrimRight(cfg.Server, "/") + "/deploy"
	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("deploy failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Name       string `json:"name"`
		URL        string `json:"url"`
		OwnerToken string `json:"owner_token"`
		SiteToken  string `json:"site_token"`
		Warning    string `json:"warning"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	cfg.Sites[result.Name] = SiteConfig{
		OwnerToken: result.OwnerToken,
		SiteToken:  result.SiteToken,
		URL:        result.URL,
	}
	if err := saveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to save config: %v\n", err)
	}

	fmt.Printf("Deployed: %s\n", result.URL)
	fmt.Printf("Site name: %s\n", result.Name)
	fmt.Println()
	fmt.Println("=== SAVE THESE TOKENS — they will not be shown again ===")
	fmt.Printf("Owner token: %s\n", result.OwnerToken)
	fmt.Printf("Site token:  %s\n", result.SiteToken)
	if result.Warning != "" {
		fmt.Println(result.Warning)
	}
	fmt.Printf("\nSaved to ~/.quick/config.json\n")
	return nil
}

func cmdRedeploy(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: quick redeploy <name> [folder]")
	}
	name := args[0]
	folder := name
	if len(args) >= 2 {
		folder = args[1]
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	site, ok := cfg.Sites[name]
	if !ok || site.OwnerToken == "" {
		return fmt.Errorf("unknown site %q — deploy first or check ~/.quick/config.json", name)
	}

	if _, err := os.Stat(folder); err != nil {
		folder = "./" + name
	}
	if _, err := os.Stat(filepath.Join(folder, "index.html")); err != nil {
		return fmt.Errorf("index.html not found in %s", folder)
	}

	zipData, err := zipFolder(folder)
	if err != nil {
		return err
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "site.zip")
	if err != nil {
		return err
	}
	if _, err := part.Write(zipData); err != nil {
		return err
	}
	_ = w.Close()

	url := strings.TrimRight(cfg.Server, "/") + "/deploy/" + name
	req, err := http.NewRequest(http.MethodPut, url, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Quick-Token", site.OwnerToken)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("redeploy failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Name      string `json:"name"`
		URL       string `json:"url"`
		SiteToken string `json:"site_token"`
		Warning   string `json:"warning"`
	}
	_ = json.Unmarshal(respBody, &result)
	if result.SiteToken != "" {
		sc := cfg.Sites[name]
		sc.SiteToken = result.SiteToken
		if result.URL != "" {
			sc.URL = result.URL
		}
		cfg.Sites[name] = sc
		_ = saveConfig(cfg)
		fmt.Println("=== NEW SITE TOKEN (re-issued) — save this ===")
		fmt.Println(result.SiteToken)
	}
	fmt.Printf("Redeployed: %s\n", result.URL)
	return nil
}

func cmdList() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(cfg.Sites) == 0 {
		fmt.Println("No sites. Deploy with: quick deploy ./folder")
		return nil
	}
	fmt.Printf("Server: %s\n\n", cfg.Server)
	for name, s := range cfg.Sites {
		fmt.Printf("  %s\n    %s\n", name, s.URL)
	}
	return nil
}

func cmdDelete(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: quick delete <name>")
	}
	name := args[0]
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	site, ok := cfg.Sites[name]
	if !ok || site.OwnerToken == "" {
		return fmt.Errorf("unknown site %q", name)
	}

	url := strings.TrimRight(cfg.Server, "/") + "/sites/" + name
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Quick-Token", site.OwnerToken)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("delete failed (%d): %s", resp.StatusCode, string(respBody))
	}

	delete(cfg.Sites, name)
	_ = saveConfig(cfg)
	fmt.Printf("Deleted site %s\n", name)
	return nil
}

func zipFolder(root string) ([]byte, error) {
	ignorer, err := loadQuickIgnore(root)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Never package ignore file itself or internal token files
		base := filepath.Base(path)
		if base == ".quickignore" || base == ".quick_site_token" {
			return nil
		}

		relSlash := filepath.ToSlash(rel)
		if shouldIgnore(relSlash, info.IsDir(), ignorer) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		w, err := zw.Create(relSlash)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, f)
		f.Close()
		return err
	})
	if err != nil {
		zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// quickIgnore holds builtin + .quickignore patterns (gitignore-ish subset).
type quickIgnore struct {
	patterns []ignorePattern
}

type ignorePattern struct {
	raw      string
	negated  bool
	dirOnly  bool
	segments []string // split by /
}

func loadQuickIgnore(root string) (*quickIgnore, error) {
	qi := &quickIgnore{}
	// Builtins
	for _, p := range []string{
		"node_modules/",
		".git/",
		"dist/",
		".DS_Store",
		"*.pyc",
		"__pycache__/",
	} {
		qi.patterns = append(qi.patterns, parseIgnoreLine(p))
	}
	f, err := os.Open(filepath.Join(root, ".quickignore"))
	if err != nil {
		if os.IsNotExist(err) {
			return qi, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		qi.patterns = append(qi.patterns, parseIgnoreLine(line))
	}
	return qi, sc.Err()
}

func parseIgnoreLine(line string) ignorePattern {
	p := ignorePattern{raw: line}
	if strings.HasPrefix(line, "!") {
		p.negated = true
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		p.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	line = strings.TrimPrefix(line, "/")
	p.segments = strings.Split(line, "/")
	return p
}

func shouldIgnore(relSlash string, isDir bool, qi *quickIgnore) bool {
	// Builtin always-skip segments
	parts := strings.Split(relSlash, "/")
	for _, part := range parts {
		if part == "node_modules" || part == ".git" {
			return true
		}
		if strings.HasPrefix(part, ".") && part != ".well-known" {
			// skip hidden files/dirs except .well-known
			return true
		}
	}

	ignored := false
	for _, p := range qi.patterns {
		if p.dirOnly && !isDir {
			// still match if any path segment is a dir match — for files under ignored dir, Walk skips via SkipDir
			if matchIgnore(relSlash, p) {
				if p.negated {
					ignored = false
				} else {
					ignored = true
				}
			}
			continue
		}
		if matchIgnore(relSlash, p) {
			if p.negated {
				ignored = false
			} else {
				ignored = true
			}
		}
	}
	return ignored
}

func matchIgnore(rel string, p ignorePattern) bool {
	pat := strings.Join(p.segments, "/")
	if pat == "" {
		return false
	}
	// Exact or suffix segment match
	if rel == pat {
		return true
	}
	if strings.HasSuffix(rel, "/"+pat) {
		return true
	}
	// Basename glob *
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}
	if strings.Contains(pat, "*") && len(p.segments) == 1 {
		return matchStar(pat, base)
	}
	// Prefix dir: "dist" matches "dist/foo"
	if strings.HasPrefix(rel, pat+"/") {
		return true
	}
	return false
}

func matchStar(pattern, name string) bool {
	// simple single-segment * glob
	if pattern == "*" {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		return strings.HasSuffix(name, pattern[1:])
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 2 {
		return strings.HasPrefix(name, parts[0]) && strings.HasSuffix(name, parts[1])
	}
	return name == pattern
}
