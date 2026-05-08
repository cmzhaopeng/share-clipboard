# Shared Clipboard

A lightweight browser-based shared clipboard for text, images, and file attachments.

## Features

- Browser-based access across devices
- Authentication with persistent browser session
- Text messages with multiple attachments
- Image preview and file download
- Multi-user management
- SQLite-backed metadata storage
- Delete removes server-side stored content
- Copy message content to system clipboard from the web UI

## Stack

- Backend: Go
- Frontend: React + Vite + TypeScript
- Metadata store: SQLite
- File storage: local disk

## Run locally

### Backend environment

Set these environment variables before starting the server:

```bash
APP_ADDR=:8080
APP_DATA_DIR=./data
APP_STATIC_DIR=./web/dist
APP_BOOTSTRAP_ADMIN_USERNAME=admin
APP_BOOTSTRAP_ADMIN_PASSWORD=change-me
APP_SESSION_SECRET=change-me-to-a-long-random-secret
APP_PUBLIC_BASE_URL=http://127.0.0.1:8080
APP_ALLOWED_ORIGINS=http://127.0.0.1:8080
APP_COOKIE_SECURE=false
APP_ALLOW_INSECURE_HTTP=true
```

### Frontend build

```bash
cd web
npm install
npm run build
```

### Start server

```bash
go run ./cmd/server
```

## Test

```bash
go test ./...
cd web && npm run build
./scripts/local-smoke.sh
```

## Deployment notes

- Example deployment templates are under `deploy/`
- Replace all placeholder values before production use
- Do not commit populated env files, runtime databases, temp files, or built assets