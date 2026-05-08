# Shared Clipboard

A lightweight self-hosted browser clipboard for text snippets, images, and file attachments.

Shared Clipboard is designed for the simple case of moving content between your own devices or a small trusted team, without turning the tool into a full chat app or a heavy knowledge platform.

## Highlights

- Browser-based access across phones, tablets, and desktops
- Login-protected workspace with persistent session cookies
- Text items with optional file attachments
- Image preview and file download in the web UI
- Shared and private item visibility modes
- Multi-user management for admin accounts
- User password change flow
- SQLite-backed metadata storage
- Attachment files stored on local disk
- Delete operations remove server-side stored content
- One-click copy of message text to the system clipboard
- Reverse-proxy-friendly deployment model for HTTPS

## Stack

- **Backend:** Go
- **Frontend:** React + Vite + TypeScript
- **Metadata store:** SQLite
- **Attachment storage:** local filesystem
- **Deployment style:** Linux + systemd + reverse proxy

## Repository layout

```text
.
├── cmd/server/                  # application entrypoint
├── internal/app/                # HTTP handlers, auth, storage, business logic
├── web/                         # React frontend
├── deploy/                      # example deployment assets
├── scripts/local-smoke.sh       # end-to-end local smoke test
└── README.md
```

## Features

### Core clipboard flow
- Create text entries from the browser
- Upload one or more attachments with an entry
- Preview supported image attachments inline
- Download non-image files from the item list
- Delete items together with their stored files

### Authentication and access
- Username/password login
- Persistent cookie-based browser session
- Admin-managed user creation and deletion
- Password change for the current user
- Revocable server-side session handling

### Storage model
- SQLite stores users, items, attachment metadata, and sessions
- Uploaded files are written to disk under the data directory
- Deleting an item removes its associated server-side files

## Quick start for local development

### 1) Build the frontend

```bash
cd web
npm ci
npm run build
cd ..
```

### 2) Start the backend

```bash
APP_ADDR=:8080 \
APP_DATA_DIR=./data \
APP_STATIC_DIR=./web/dist \
APP_BOOTSTRAP_ADMIN_USERNAME=admin \
APP_BOOTSTRAP_ADMIN_PASSWORD=change-me \
APP_SESSION_SECRET=replace-with-a-long-random-secret \
APP_PUBLIC_BASE_URL=http://127.0.0.1:8080 \
APP_ALLOWED_ORIGINS=http://127.0.0.1:8080 \
APP_COOKIE_SECURE=false \
APP_ALLOW_INSECURE_HTTP=true \
go run ./cmd/server
```

Then open:

- `http://127.0.0.1:8080`

### Local environment variables

| Variable | Required | Purpose |
|---|---:|---|
| `APP_ADDR` | no | Listen address, default `:2053` |
| `APP_DATA_DIR` | yes | Directory for SQLite data and uploaded files |
| `APP_STATIC_DIR` | no | Built frontend directory, default `./web/dist` |
| `APP_BOOTSTRAP_ADMIN_USERNAME` | yes | Initial admin username |
| `APP_BOOTSTRAP_ADMIN_PASSWORD` | yes | Initial admin password |
| `APP_SESSION_SECRET` | yes | Secret used for session security |
| `APP_PUBLIC_BASE_URL` | no | Public base URL used for origin validation |
| `APP_ALLOWED_ORIGINS` | no | Comma-separated allowed origins |
| `APP_COOKIE_SECURE` | no | Set `false` for local HTTP development |
| `APP_ALLOW_INSECURE_HTTP` | no | Set `true` only when intentionally serving plain HTTP |
| `APP_TLS_CERT` | conditional | TLS certificate path when the app serves HTTPS directly |
| `APP_TLS_KEY` | conditional | TLS private key path when the app serves HTTPS directly |

## Testing

### Backend tests

```bash
go test ./...
```

### Frontend build verification

```bash
cd web
npm ci
npm run build
```

### Smoke test

```bash
./scripts/local-smoke.sh
```

The smoke test boots a temporary local server, logs in, creates an item with an attachment, checks user management, and confirms the SQLite database is created.

## Deployment overview

Deployment examples are provided in `deploy/` as sanitized templates.

Typical production layout:

1. Build frontend assets in `web/dist`
2. Build the Go binary
3. Deploy assets and binary to the target server
4. Configure environment variables in a local `.env` file on the server
5. Run the app behind a reverse proxy such as Caddy or Nginx
6. Expose HTTPS at the proxy layer

### Included deployment assets

- `deploy/shared-clipboard.env.example` — example environment file with placeholders
- `deploy/shared-clipboard.service` — systemd service template
- `deploy/Caddyfile` — reverse proxy example
- `deploy/deploy.sh` — example build/upload helper script

### Reverse proxy note

If TLS is terminated by Caddy or another proxy and the Go app listens only on localhost, `APP_ALLOW_INSECURE_HTTP=true` can be acceptable for the internal hop between proxy and app. For direct public serving without a reverse proxy, provide `APP_TLS_CERT` and `APP_TLS_KEY` instead.

## Privacy and security notes

- This public repository intentionally uses placeholder hostnames, usernames, secrets, and deployment values
- Do **not** commit populated environment files, runtime databases, uploaded files, or build artifacts
- Use a long random value for `APP_SESSION_SECRET`
- Use HTTPS in real deployments
- Restrict `APP_ALLOWED_ORIGINS` to the exact origins that should access the service
- Treat uploaded files as user content and store them on trusted infrastructure only

## Open-source repository policy

The repo is prepared for public sharing:

- real deployment IPs/domains are removed
- local secret files are ignored
- generated assets and local runtime directories are ignored
- example configuration keeps only safe placeholders

## Roadmap ideas

- drag-and-drop upload polish
- better mobile layout tuning
- optional per-user quotas
- richer admin audit visibility
- deployment automation improvements

## License

MIT — see [LICENSE](LICENSE).
