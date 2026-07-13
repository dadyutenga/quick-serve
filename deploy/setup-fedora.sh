#!/usr/bin/env bash
# Setup Quick on Fedora (or RHEL-like) VPS
set -euo pipefail

QUICK_ROOT="${QUICK_ROOT:-/opt/quick}"
DOMAIN="${DOMAIN:-quick.dadyprojects.tech}"

echo "==> Creating quick user and directories"
if ! id quick &>/dev/null; then
  sudo useradd -r -s /sbin/nologin quick
fi
sudo mkdir -p "$QUICK_ROOT"/{data,sites,uploads}
sudo chown -R quick:quick "$QUICK_ROOT"

echo "==> Installing binary (expects ./quick-server in CWD)"
if [[ ! -f ./quick-server ]]; then
  echo "Build first: GOOS=linux GOARCH=amd64 go build -o quick-server ."
  exit 1
fi
sudo cp ./quick-server "$QUICK_ROOT/quick-server"
sudo cp ./sdk.js "$QUICK_ROOT/sdk.js"
sudo chown quick:quick "$QUICK_ROOT/quick-server" "$QUICK_ROOT/sdk.js"
sudo chmod 755 "$QUICK_ROOT/quick-server"

if [[ ! -f "$QUICK_ROOT/.env" ]]; then
  echo "==> Creating $QUICK_ROOT/.env (edit ANTHROPIC_API_KEY)"
  sudo tee "$QUICK_ROOT/.env" >/dev/null <<EOF
ANTHROPIC_API_KEY=
QUICK_BASE_DOMAIN=$DOMAIN
EOF
  sudo chmod 600 "$QUICK_ROOT/.env"
  sudo chown quick:quick "$QUICK_ROOT/.env"
fi

echo "==> Installing systemd unit"
sudo cp ./deploy/quick.service /etc/systemd/system/quick.service
sudo systemctl daemon-reload
sudo systemctl enable --now quick.service

echo "==> Nginx config"
if command -v nginx &>/dev/null; then
  sudo cp ./deploy/nginx.conf /etc/nginx/conf.d/quick.conf
  # SELinux: allow nginx to proxy
  if command -v setsebool &>/dev/null; then
    sudo setsebool -P httpd_can_network_connect 1 || true
  fi
  if command -v firewall-cmd &>/dev/null; then
    sudo firewall-cmd --add-service=http --add-service=https --permanent || true
    sudo firewall-cmd --reload || true
  fi
  sudo nginx -t && sudo systemctl reload nginx
else
  echo "nginx not installed; skip. Install and copy deploy/nginx.conf to conf.d/"
fi

echo "==> Done. Check: sudo journalctl -u quick -f"
echo "Wildcard TLS: use certbot with DNS-01 for *.$DOMAIN"
