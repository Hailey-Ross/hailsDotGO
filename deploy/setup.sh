#!/bin/bash
# Run this ONCE on your VPS as root to install Caddy, create the app user,
# and register the systemd service. Run from the directory containing these files.
set -e

echo "==> Installing Caddy..."
apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
    | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
    | tee /etc/apt/sources.list.d/caddy-stable.list
apt-get update
apt-get install -y caddy

echo "==> Creating app user and directories..."
useradd -r -s /bin/false hailsdotgo 2>/dev/null || echo "User already exists, skipping."
mkdir -p /opt/hailsdotgo/static/js /opt/hailsdotgo/static/css /opt/hailsdotgo/templates
chown -R hailsdotgo:hailsdotgo /opt/hailsdotgo

echo "==> Installing systemd service..."
cp hailsdotgo.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable hailsdotgo
echo "    Service registered (not started yet — run after first deploy)"

echo "==> Installing Caddyfile..."
cp Caddyfile /etc/caddy/Caddyfile
echo "    Caddyfile installed (Caddy not restarted yet)"

echo ""
echo "==> Setup complete. Next steps in order:"
echo "    1. Apply docker-compose.override.yml to AzuraCast (see walkthrough)"
echo "    2. Run deploy.ps1 from your Windows machine to upload the app"
echo "    3. systemctl restart caddy"
echo "    4. systemctl start hailsdotgo"
