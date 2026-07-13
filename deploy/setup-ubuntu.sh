#!/usr/bin/env bash
# Setup Quick on Ubuntu/Debian VPS
set -euo pipefail

QUICK_ROOT="${QUICK_ROOT:-/opt/quick}"
DOMAIN="${DOMAIN:-quick.dadyprojects.tech}"

echo "==> Creating quick user and directories"
if ! id quick &>/dev/null; then
  sudo useradd -r -s /usr/sbin/nologin quick
fi
sudo mkdir -p "$QUICK_ROOT"/{data,sites,uploads}
sudo chown -R quick:quick "$QUICK_ROOT"

if [[ ! -f ./quick-server ]]; then
  echo "Build first: GOOS=linux GOARCH=amd64 go build -o quick-server ."
  exit 1
fi
sudo cp ./quick-server "$QUICK_ROOT/quick-server"
sudo cp ./sdk.js "$QUICK_ROOT/sdk.js"
sudo chown quick:quick "$QUICK_ROOT/quick-server" "$QUICK_ROOT/sdk.js"
sudo chmod 755 "$QUICK_ROOT/quick-server"

if [[ ! -f "$QUICK_ROOT/.env" ]]; then
  sudo tee "$QUICK_ROOT/.env" >/dev/null <<EOF
ANTHROPIC_API_KEY=
QUICK_BASE_DOMAIN=$DOMAIN
EOF
  sudo chmod 600 "$QUICK_ROOT/.env"
  sudo chown quick:quick "$QUICK_ROOT/.env"
fi

sudo cp ./deploy/quick.service /etc/systemd/system/quick.service
sudo systemctl daemon-reload
sudo systemctl enable --now quick.service

if command -v nginx &>/dev/null; then
  sudo cp ./deploy/nginx.conf /etc/nginx/conf.d/quick.conf
  sudo nginx -t && sudo systemctl reload nginx
fi

echo "==> Done. journalctl -u quick -f"
