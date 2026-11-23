package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Personal API tokens let iOS Shortcuts, scripts and CLIs use the API without a browser session.
// Only the SHA-256 of a token is stored; the plaintext is shown once, at creation.

const (
	apiTokenPrefix    = "scb_"
	maxTokenNameRunes = 60
	maxTokensPerUser  = 20
)

type APIToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	Token      string     `json:"token,omitempty"` // only in the creation response
}

func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}
	scheme, value, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", true // present but malformed: never fall back to the cookie
	}
	return strings.TrimSpace(value), true
}

func (a *App) userFromAPIToken(token string) (authUser, bool) {
	if !strings.HasPrefix(token, apiTokenPrefix) {
		return authUser{}, false
	}
	hash := sha256Hex(token)
	var username, id string
	a.mu.RLock()
	err := a.db.QueryRow(`SELECT id, username FROM api_tokens WHERE token_hash = ?`, hash).Scan(&id, &username)
	a.mu.RUnlock()
	if err != nil {
		return authUser{}, false
	}
	user, err := a.lookupAuthUser(username)
	if err != nil {
		return authUser{}, false
	}
	_, _ = a.db.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339Nano), id)
	return user, true
}

func (a *App) handleTokens(w http.ResponseWriter, r *http.Request) {
	if err := a.enforceOrigin(r); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	// Token management needs a browser session: a leaked token must not be able to mint more.
	if _, hasBearer := bearerToken(r); hasBearer {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "token management requires a browser session"})
		return
	}
	user, ok := a.authenticatedUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	switch {
	case r.URL.Path == "/api/tokens" && r.Method == http.MethodGet:
		tokens, err := a.listTokens(user.Username)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load tokens failed"})
			return
		}
		writeJSON(w, http.StatusOK, tokens)
	case r.URL.Path == "/api/tokens" && r.Method == http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" || utf8.RuneCountInString(name) > maxTokenNameRunes {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token name must be 1-60 characters"})
			return
		}
		token, err := a.createToken(user.Username, name)
		if err != nil {
			if errors.Is(err, errBadItemRequest) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create token failed"})
			return
		}
		writeJSON(w, http.StatusCreated, token)
	case strings.HasPrefix(r.URL.Path, "/api/tokens/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(r.URL.Path, "/api/tokens/")
		res, err := a.db.Exec(`DELETE FROM api_tokens WHERE id = ? AND username = ?`, id, user.Username)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete token failed"})
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost, http.MethodDelete)
	}
}

func (a *App) listTokens(username string) ([]APIToken, error) {
	rows, err := a.db.Query(`SELECT id, name, created_at, last_used_at FROM api_tokens WHERE username = ? ORDER BY created_at DESC`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := []APIToken{}
	for rows.Next() {
		var t APIToken
		var createdAt string
		var lastUsed sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &createdAt, &lastUsed); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		if lastUsed.Valid {
			if parsed, err := time.Parse(time.RFC3339Nano, lastUsed.String); err == nil {
				t.LastUsedAt = &parsed
			}
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

func (a *App) createToken(username, name string) (APIToken, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM api_tokens WHERE username = ?`, username).Scan(&count); err != nil {
		return APIToken{}, err
	}
	if count >= maxTokensPerUser {
		return APIToken{}, newBadItemRequest("too many tokens; revoke an unused one first")
	}
	now := time.Now().UTC()
	t := APIToken{ID: randomID(), Name: name, CreatedAt: now, Token: apiTokenPrefix + randomID() + randomID()}
	_, err := a.db.Exec(`INSERT INTO api_tokens (id, username, name, token_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		t.ID, username, name, sha256Hex(t.Token), now.Format(time.RFC3339Nano))
	return t, err
}

// handleLatest serves the newest item the caller can see, in a form Shortcuts can use directly:
// the message as plain text, or — for attachment-only items — the first file itself.
func (a *App) handleLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	user, ok := a.authenticatedUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	items, err := a.listItemsForUser(user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load items failed"})
		return
	}
	if len(items) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "clipboard is empty"})
		return
	}
	item := items[0]
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("format") == "json" {
		writeJSON(w, http.StatusOK, item)
		return
	}
	if item.Message != "" || len(item.Attachments) == 0 {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(item.Message))
		return
	}
	attachment := item.Attachments[0]
	contentType := attachment.MimeType
	if t, ok := inlineContentType(attachment.MimeType); ok {
		contentType = t
	} else {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", contentDisposition("attachment", attachment.Name))
	http.ServeFile(w, r, a.attachmentPath(item.ID, attachment.StoredName))
}

func (a *App) attachmentPath(itemID, storedName string) string {
	return filepath.Join(a.cfg.DataDir, "files", itemID, storedName)
}
