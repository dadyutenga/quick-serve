CREATE TABLE IF NOT EXISTS sites (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT UNIQUE NOT NULL,
    owner_token_hash TEXT NOT NULL,
    site_token_hash  TEXT NOT NULL DEFAULT '',
    owner_ip         TEXT,
    created_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
    is_active        BOOLEAN DEFAULT 1
);
