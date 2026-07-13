package db

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Site struct {
	ID             int64
	Name           string
	OwnerTokenHash string
	SiteTokenHash  string
	OwnerIP        string
	IsActive       bool
}

func Open(dbPath string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	// modernc.org/sqlite DSN
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1) // SQLite + WAL: single writer is fine; serialize writes
	db.SetMaxIdleConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	// Ensure WAL and busy_timeout even if DSN pragmas are ignored
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA foreign_keys=ON;",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("pragma: %w", err)
		}
	}

	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name TEXT PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return err
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var exists string
		err := db.QueryRow(`SELECT name FROM schema_migrations WHERE name = ?`, name).Scan(&exists)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}

		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}

		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func GetSiteByName(db *sql.DB, name string) (*Site, error) {
	s := &Site{}
	var active int
	err := db.QueryRow(`
		SELECT id, name, owner_token_hash, site_token_hash, COALESCE(owner_ip,''), is_active
		FROM sites WHERE name = ? AND is_active = 1
	`, name).Scan(&s.ID, &s.Name, &s.OwnerTokenHash, &s.SiteTokenHash, &s.OwnerIP, &active)
	if err != nil {
		return nil, err
	}
	s.IsActive = active == 1
	return s, nil
}

func GetSiteByID(db *sql.DB, id int64) (*Site, error) {
	s := &Site{}
	var active int
	err := db.QueryRow(`
		SELECT id, name, owner_token_hash, site_token_hash, COALESCE(owner_ip,''), is_active
		FROM sites WHERE id = ? AND is_active = 1
	`, id).Scan(&s.ID, &s.Name, &s.OwnerTokenHash, &s.SiteTokenHash, &s.OwnerIP, &active)
	if err != nil {
		return nil, err
	}
	s.IsActive = active == 1
	return s, nil
}
