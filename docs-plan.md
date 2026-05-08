# Shared Clipboard Follow-up Plan

> **For Hermes:** Use subagent-driven-development skill to execute verification and deployment in order.

**Goal:** Finalize the requested follow-up features: multi-user login management, SQLite persistence, and copy-to-system-clipboard UI, then deploy them to the target server.

**Architecture:** Keep the single Go server + React frontend architecture. Persist users/items/attachments metadata in SQLite, retain file blobs on disk, manage multiple users through authenticated API endpoints, and expose a clipboard-copy action in the item list UI.

**Tech Stack:** Go 1.24, modernc SQLite driver, React 18 + Vite, systemd, HTTPS on port 2053.

---

### Task 1: Inspect current code and deployment drift
- Confirm local code implements `/api/users`, SQLite tables, and frontend copy button.
- Confirm remote service/env are still on the older deployment and need refresh.

### Task 2: Verify locally
- Run `go test ./...`
- Run `npm run build`
- Run `./scripts/local-smoke.sh`

### Task 3: Independent review
- Review auth/session logic, SQLite migration, delete semantics, and multi-user behavior.

### Task 4: Deploy updated build
- Build Linux binary
- Copy binary, env file, and frontend assets to the target server
- Restart `shared-clipboard.service`

### Task 5: Verify remote state
- Confirm service is active on `:2053`
- Confirm HTTPS responds
- Confirm SQLite file exists and legacy JSON migrated if present
- Return access details to the user
