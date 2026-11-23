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
- In-app viewer for images, PDF, text, audio and video; everything else via share/download
- Copy images and text files to the clipboard; share any file through the system share sheet (iOS: Copy / Save to Files / other apps)
- Live updates: other devices see new and deleted items within a second (Server-Sent Events)
- Content type detection on upload (extension, then file signature) when the client sends a generic type
- Mobile-first UI that works as an iPhone home-screen web app
- Delete items together with their stored files

### Authentication and access
- Username/password login
- Persistent cookie-based browser session
- Admin-managed user creation and deletion
- Password change for the current user
- Revocable server-side session handling
- Personal API tokens for iOS Shortcuts, scripts and CLIs (shown once, stored hashed, revocable)

## HTTP API for Shortcuts and scripts

Create a token in the web UI under **Settings → API tokens**, then send it as `Authorization: Bearer <token>`.
Bearer requests skip the browser Origin check. Tokens only reach the clipboard endpoints (items, files, previews, events, latest): account endpoints (users, passwords, tokens) return 403 for token requests, even for an admin's token.

```bash
TOKEN=scb_...
# send text (multipart, urlencoded or JSON bodies are accepted)
curl -H "Authorization: Bearer $TOKEN" -F message="hello" https://clipboard.example.com/api/items
# send a file; add -F visibility=private to keep it to yourself
curl -H "Authorization: Bearer $TOKEN" -F attachments=@report.pdf https://clipboard.example.com/api/items
# newest item: its text, or the first attachment's bytes when it has no text (?format=json for the full item)
curl -H "Authorization: Bearer $TOKEN" https://clipboard.example.com/api/latest
```

`GET /api/events` is a Server-Sent Events stream of `item` events (`{"type":"item.created","id":"..."}`), filtered to the items the caller may see.

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
- `deploy/backup.sh` + `deploy/shared-clipboard-backup.{service,timer}` — daily snapshot (SQLite online backup + hard-linked copy of `files/`, 14 days kept by default)

### Backups

Install `deploy/backup.sh` as `/opt/shared-clipboard/backup.sh`, create `/var/backups/shared-clipboard` owned by the service user (mode 700), install the two units into `/etc/systemd/system`, then `systemctl enable --now shared-clipboard-backup.timer`. Each run writes `/var/backups/shared-clipboard/<timestamp>/{clipboard.db,files/}` and points `latest` at it.

To restore: stop the service, copy `clipboard.db` and `files/` from a snapshot into the data directory (removing any `clipboard.db-wal`/`-shm`), fix ownership, and start the service again.

### HEIC photos

iPhones copy and save photos as HEIC, which most browsers cannot show. If `heif-convert` (or `heif-dec`) is on the server's `PATH`, HEIC uploads are converted to JPEG on arrival and existing HEIC attachments are converted at startup. On Debian/Ubuntu: `apt install libheif-examples libheif-plugin-libde265` (the plugin provides the HEVC decoder). Without it, HEIC files are stored and downloadable as-is.

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

- upload progress
- per-item expiry / burn-after-reading
- optional per-user quotas
- richer admin audit visibility
- deployment automation improvements

## License

MIT — see [LICENSE](LICENSE).
