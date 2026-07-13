CREATE TABLE IF NOT EXISTS files (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id       INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    filename      TEXT NOT NULL,
    original_name TEXT NOT NULL,
    mime_type     TEXT,
    size_bytes    INTEGER,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(site_id, filename)
);
