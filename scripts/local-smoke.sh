#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
RUNTIME_DIR="$ROOT_DIR/.tmp-runtime"
COOKIE_JAR="$RUNTIME_DIR/cookies.txt"
LOG_FILE="$RUNTIME_DIR/server.log"
PORT=$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)
BASE_URL="http://127.0.0.1:$PORT"

rm -rf "$RUNTIME_DIR"
mkdir -p "$RUNTIME_DIR"

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

pushd "$ROOT_DIR" >/dev/null
APP_ADDR=":$PORT" \
APP_DATA_DIR="$RUNTIME_DIR/data" \
APP_STATIC_DIR="$ROOT_DIR/web/dist" \
APP_BOOTSTRAP_ADMIN_USERNAME="tester" \
APP_BOOTSTRAP_ADMIN_PASSWORD="secret-pass" \
APP_SESSION_SECRET="local-session-secret" \
APP_PUBLIC_BASE_URL="$BASE_URL" \
APP_ALLOWED_ORIGINS="$BASE_URL" \
APP_COOKIE_SECURE="false" \
APP_ALLOW_INSECURE_HTTP="true" \
go run ./cmd/server >"$LOG_FILE" 2>&1 &
SERVER_PID=$!
popd >/dev/null

for _ in $(seq 1 30); do
  if curl -sf "$BASE_URL/" >/dev/null; then
    break
  fi
  sleep 1
done

curl -sf -c "$COOKIE_JAR" \
  -H 'Content-Type: application/json' \
  -H "Origin: $BASE_URL" \
  -d '{"username":"tester","password":"secret-pass"}' \
  "$BASE_URL/api/login" >/dev/null

curl -sf -b "$COOKIE_JAR" \
  -H 'Content-Type: application/json' \
  -H "Origin: $BASE_URL" \
  -d '{"username":"alice","password":"alice-pass-123","isAdmin":false}' \
  "$BASE_URL/api/users" >/dev/null

printf 'hello from smoke test' > "$RUNTIME_DIR/hello.txt"
CREATE_RESPONSE=$(curl -sf -b "$COOKIE_JAR" -H "Origin: $BASE_URL" -F 'message=smoke test message' -F "attachments=@$RUNTIME_DIR/hello.txt" "$BASE_URL/api/items")
CREATE_RESPONSE="$CREATE_RESPONSE" python3 - <<'PY'
import json, os
obj = json.loads(os.environ['CREATE_RESPONSE'])
assert obj['message'] == 'smoke test message'
assert len(obj['attachments']) == 1
assert obj['createdBy'] == 'tester'
print(obj['id'])
PY

LIST_RESPONSE=$(curl -sf -b "$COOKIE_JAR" "$BASE_URL/api/items")
LIST_RESPONSE="$LIST_RESPONSE" python3 - <<'PY'
import json, os
items = json.loads(os.environ['LIST_RESPONSE'])
assert len(items) >= 1
print('items_ok')
PY

USERS_RESPONSE=$(curl -sf -b "$COOKIE_JAR" "$BASE_URL/api/users")
USERS_RESPONSE="$USERS_RESPONSE" python3 - <<'PY'
import json, os
users = json.loads(os.environ['USERS_RESPONSE'])
assert any(u['username'] == 'alice' for u in users)
print('users_ok')
PY

test -f "$RUNTIME_DIR/data/clipboard.db"
echo 'smoke_ok'
