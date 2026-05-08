#!/usr/bin/env bash
set -euo pipefail

REMOTE=${1:-${REMOTE:-user@example-host}}
SSH_KEY=${SSH_KEY:-$HOME/.ssh/id_ed25519}
ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

if [[ ! -f "$SSH_KEY" ]]; then
  echo "SSH key not found: $SSH_KEY" >&2
  exit 1
fi

pushd "$ROOT_DIR/web" >/dev/null
npm install
npm run build
popd >/dev/null

pushd "$ROOT_DIR" >/dev/null
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o shared-clipboard ./cmd/server
popd >/dev/null

ssh -i "$SSH_KEY" "$REMOTE" '
  set -euo pipefail
  sudo mkdir -p /opt/shared-clipboard/web/dist /opt/shared-clipboard/data
  sudo rm -rf /tmp/shared-clipboard-dist
  mkdir -p /tmp/shared-clipboard-dist
  if ! id -u shared-clipboard >/dev/null 2>&1; then
    sudo useradd --system --home /opt/shared-clipboard --shell /usr/sbin/nologin shared-clipboard
  fi
  sudo chown -R shared-clipboard:shared-clipboard /opt/shared-clipboard/data /opt/shared-clipboard/web/dist
  sudo find /opt/shared-clipboard/data -type d -exec chmod 700 {} +
  sudo find /opt/shared-clipboard/data -type f -exec chmod 600 {} +
'
scp -i "$SSH_KEY" "$ROOT_DIR/shared-clipboard" "$REMOTE":/tmp/shared-clipboard
scp -i "$SSH_KEY" "$ROOT_DIR/deploy/shared-clipboard.service" "$REMOTE":/tmp/shared-clipboard.service
scp -i "$SSH_KEY" -r "$ROOT_DIR/web/dist/." "$REMOTE":/tmp/shared-clipboard-dist/

ssh -i "$SSH_KEY" "$REMOTE" '
  set -euo pipefail
  sudo install -o root -g root -m 755 /tmp/shared-clipboard /opt/shared-clipboard/shared-clipboard
  sudo mkdir -p /opt/shared-clipboard/web/dist
  sudo rsync -a --delete /tmp/shared-clipboard-dist/ /opt/shared-clipboard/web/dist/
  sudo chown -R shared-clipboard:shared-clipboard /opt/shared-clipboard/web/dist /opt/shared-clipboard/data
  sudo find /opt/shared-clipboard/data -type d -exec chmod 700 {} +
  sudo find /opt/shared-clipboard/data -type f -exec chmod 600 {} +
  sudo install -o root -g root -m 644 /tmp/shared-clipboard.service /etc/systemd/system/shared-clipboard.service
  sudo rm -rf /tmp/shared-clipboard /tmp/shared-clipboard.service /tmp/shared-clipboard-dist
  sudo systemctl daemon-reload
'

echo 'Upload complete. Configure /opt/shared-clipboard/.env and Caddy before starting service.'
