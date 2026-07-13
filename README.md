# Quick

Zero-config backend for AI-generated HTML sites.

Stack: **Go (Fiber) + SQLite + local disk + WebSockets**

Anyone deploys a plain `index.html` with one `<script src="/sdk.js">` tag and immediately gets:

- Per-site key-value storage
- File uploads with public URLs
- AI proxy (server-held Anthropic key, rate limited)
- Realtime pub/sub over WebSockets (`site_id:room` scoped)
- A public subdomain on deploy

## Quick start (local)

```bash
# Requirements: Go 1.22+
cd quick
go mod tidy
go build -o quick-server .
go build -o quick ./cli

# Run server
./quick-server
# Windows: .\quick-server.exe

# Deploy a folder (in another terminal)
set QUICK_SERVER=http://localhost:8080   # PowerShell: $env:QUICK_SERVER=...
./quick deploy ./examples/hello --name hello

# Open (path-based local routing)
# http://localhost:8080/s/hello/
```

### Environment

| Variable | Default | Description |
|---|---|---|
| `QUICK_PORT` | `8080` | Listen port |
| `QUICK_DB_PATH` | `./data/quick.db` | SQLite path |
| `QUICK_SITES_DIR` | `./sites` | Deployed site files |
| `QUICK_UPLOADS_DIR` | `./uploads` | User uploads |
| `QUICK_BASE_DOMAIN` | `quick.dadyprojects.tech` | Subdomain base |
| `ANTHROPIC_API_KEY` | _(empty)_ | Required for `/api/ai` |
| `QUICK_ENV` | `development` | `production` enables https site URLs |

See `.env.example`.

## Docker (skinniest image)

Multi-stage build → **static binary + CA certs on `scratch`** (no OS, ~13MB).  
`sdk.js` + deploy console are **embedded** in the binary.

```bash
# Install Docker Desktop first, then from quick/:
docker build -t quick:slim .
docker run --rm -p 8080:8080 -v quick-data:/data quick:slim

# or
docker compose up -d --build
```

Open http://localhost:8080/ — data/sites persist in the `quick-data` volume.

## HTML site example

```html
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8" />
  <title>Hello Quick</title>
  <script src="/sdk.js"></script>
</head>
<body>
  <h1>Hello</h1>
  <script>
    // window.__QUICK_TOKEN__ is injected by the server (site token)
    quick.data.set("visits", { n: 1 }).then(console.log);
    quick.data.get("visits").then(console.log);
  </script>
</body>
</html>
```

## CLI

```
quick deploy <folder> [--name <name>]
quick redeploy <name> [folder]
quick list
quick delete <name>
```

Config + tokens: `~/.quick/config.json`  
Server URL: env `QUICK_SERVER` (default `http://localhost:8080`)

## Auth model

| Token | Powers | Where it lives |
|---|---|---|
| **owner_token** | deploy, redeploy, delete | CLI config only |
| **site_token** | data / files / ai / ws | Embedded in public HTML |

Header: `X-Quick-Token`. Never trust `Origin`/`Referer` for auth.

## API (per site host)

- `POST/GET/DELETE /api/data/:key` · `GET /api/data`
- `POST/GET/DELETE /api/files` · `GET /api/files/:filename`
- `POST /api/ai` body `{ "prompt": "..." }`
- `GET /api/ws?room=default&token=...` (WebSocket)

Deploy (apex):

- `POST /deploy` multipart `file` (+ optional `name`)
- `PUT /deploy/:name` (owner token)
- `DELETE /sites/:name` (owner token)
- `GET /deploy/status/:name`

## Production (VPS)

```bash
GOOS=linux GOARCH=amd64 go build -o quick-server .
GOOS=linux GOARCH=amd64 go build -o quick ./cli
# On VPS:
sudo bash deploy/setup-fedora.sh   # or setup-ubuntu.sh
```

- Nginx: `deploy/nginx.conf` → `/etc/nginx/conf.d/quick.conf`
- systemd: `deploy/quick.service`
- Wildcard TLS via certbot DNS-01 for `*.quick.dadyprojects.tech`

## Security notes

- Path traversal guards on static serve and file downloads (blocks any `.quick*` path segment)
- Zip-slip + **actual** extracted size/file-count limits on deploy
- Per-site combined quota (files + KV, 500MB) and 10MB per file
- KV values must be valid JSON; 256KB cap
- AI max_tokens clamped; model override rejected; rate limit counts only valid provider-bound attempts
- Deploy rate limit 5/hour/IP; AI 10/min/site
- Tokens hashed with bcrypt; never logged (app logger uses path only, not query)
- Secrets only via env / `.env` (chmod 600)
- **Do not expose `:8080` publicly.** Behind nginx set `QUICK_TRUST_PROXY=1` (required for real client IPs; not implied by `QUICK_ENV=production`). Nginx overwrites `X-Forwarded-For` with `$remote_addr`.
- Site tokens are public-tier (embedded in HTML); WS may pass them as `?token=` — nginx `access_log off` on `/api/ws`
- CORS allows `*.BaseDomain` and localhost only (not arbitrary origins)

## Makefile

```
make build   # server + cli
make test
make tidy
make linux   # cross-compile for Linux VPS
```
