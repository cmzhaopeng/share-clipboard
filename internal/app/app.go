package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const (
	cookieName         = "shared_clipboard_session"
	maxMessageLength   = 20000
	maxAttachmentBytes = 50 << 20
	sessionLifetime    = 365 * 24 * time.Hour
	visibilityShared   = "shared"
	visibilityPrivate  = "private"
)

type App struct {
	cfg Config
	mux *http.ServeMux
	mu  sync.RWMutex
	db  *sql.DB
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type sessionResponse struct {
	Authenticated          bool   `json:"authenticated"`
	Username               string `json:"username,omitempty"`
	IsAdmin                bool   `json:"isAdmin"`
	BootstrapAdminUsername string `json:"bootstrapAdminUsername,omitempty"`
}

type userUpsertRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"isAdmin"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

type itemAccessScope struct {
	Visibility string `json:"visibility"`
}

type authUser struct {
	Username string
	IsAdmin  bool
	Version  string
}

type authSession struct {
	Username  string
	Version   string
	ExpiresAt time.Time
}

type requestError struct {
	msg string
}

func (e *requestError) Error() string {
	return e.msg
}

func (e *requestError) Is(target error) bool {
	return target == errBadItemRequest
}

func newBadItemRequest(msg string) error {
	return &requestError{msg: msg}
}

func New(cfg Config) (*App, error) {
	if strings.TrimSpace(cfg.DataDir) == "" {
		return nil, errors.New("data dir is required")
	}
	if strings.TrimSpace(cfg.BootstrapAdminUsername) == "" || strings.TrimSpace(cfg.BootstrapAdminPassword) == "" || strings.TrimSpace(cfg.SessionSecret) == "" {
		return nil, errors.New("bootstrap admin username, bootstrap admin password, and session secret are required")
	}
	if strings.TrimSpace(cfg.PublicBaseURL) == "" && !cfg.AllowInsecureHTTP {
		return nil, errors.New("public base URL is required when insecure HTTP is disabled")
	}
	if strings.TrimSpace(cfg.PublicBaseURL) != "" {
		parsed, err := url.Parse(cfg.PublicBaseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, errors.New("public base URL must be a valid absolute URL")
		}
		if parsed.Scheme != "https" && !cfg.AllowInsecureHTTP {
			return nil, errors.New("public base URL must use https unless insecure HTTP is explicitly allowed")
		}
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "files"), 0o700); err != nil {
		return nil, fmt.Errorf("create files dir: %w", err)
	}
	dbPath := filepath.Join(cfg.DataDir, "clipboard.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	application := &App{cfg: cfg, mux: http.NewServeMux(), db: db}
	if err := application.initDatabase(); err != nil {
		_ = db.Close()
		return nil, err
	}
	application.routes()
	return application, nil
}

func (a *App) Handler() http.Handler {
	return a.mux
}

func (a *App) routes() {
	a.mux.HandleFunc("/api/login", a.handleLogin)
	a.mux.HandleFunc("/api/logout", a.handleLogout)
	a.mux.HandleFunc("/api/session", a.handleSession)
	a.mux.HandleFunc("/api/users/change-password", a.requireAuth(a.handleChangePassword))
	a.mux.HandleFunc("/api/users", a.requireAuth(a.handleUsers))
	a.mux.HandleFunc("/api/users/", a.requireAuth(a.handleUserByUsername))
	a.mux.HandleFunc("/api/items", a.requireAuth(a.handleItems))
	a.mux.HandleFunc("/api/items/", a.requireAuth(a.handleItemByID))
	a.mux.HandleFunc("/api/files/", a.requireAuth(a.handleFileDownload))
	a.mux.HandleFunc("/api/previews/", a.requireAuth(a.handleImagePreview))
	a.mux.Handle("/", a.frontendHandler())
}

func (a *App) frontendHandler() http.Handler {
	if strings.TrimSpace(a.cfg.StaticDir) != "" {
		if _, err := os.Stat(filepath.Join(a.cfg.StaticDir, "index.html")); err == nil {
			fs := http.FileServer(http.Dir(a.cfg.StaticDir))
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/api/") {
					http.NotFound(w, r)
					return
				}
				path := filepath.Join(a.cfg.StaticDir, filepath.Clean(r.URL.Path))
				if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
					fs.ServeHTTP(w, r)
					return
				}
				http.ServeFile(w, r, filepath.Join(a.cfg.StaticDir, "index.html"))
			})
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><title>Shared Clipboard</title></head><body><div style="font-family:sans-serif;padding:24px">Frontend not built. Run <code>npm run build</code> in <code>web/</code>.</div></body></html>`)
	})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := a.enforceOrigin(r); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	username := strings.TrimSpace(req.Username)
	ok, err := a.checkPassword(username, req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	user, err := a.lookupAuthUser(username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
		return
	}
	sessionValue, expiresAt, err := a.createSession(user.Username, user.Version)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
		return
	}
	cookie := &http.Cookie{
		Name:     cookieName,
		Value:    sessionValue,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.cfg.CookieSecure,
	}
	http.SetCookie(w, cookie)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := a.enforceOrigin(r); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if cookie, err := r.Cookie(cookieName); err == nil && cookie.Value != "" {
		_ = a.deleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.cfg.CookieSecure,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	user, ok := a.authenticatedUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, sessionResponse{Authenticated: false, BootstrapAdminUsername: strings.TrimSpace(a.cfg.BootstrapAdminUsername)})
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{Authenticated: true, Username: user.Username, IsAdmin: user.IsAdmin, BootstrapAdminUsername: strings.TrimSpace(a.cfg.BootstrapAdminUsername)})
}

func (a *App) handleUsers(w http.ResponseWriter, r *http.Request) {
	if err := a.enforceOrigin(r); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	user, ok := a.authenticatedUser(r)
	if !ok || !user.IsAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin permission required"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		users, err := a.listUsers()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list users failed"})
			return
		}
		writeJSON(w, http.StatusOK, users)
	case http.MethodPost:
		var req userUpsertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username and password are required"})
			return
		}
		if err := a.upsertUser(req.Username, req.Password, req.IsAdmin); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save user failed"})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"username": req.Username})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (a *App) handleUserByUsername(w http.ResponseWriter, r *http.Request) {
	if err := a.enforceOrigin(r); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	user, ok := a.authenticatedUser(r)
	if !ok || !user.IsAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin permission required"})
		return
	}
	if r.Method != http.MethodDelete {
		methodNotAllowed(w, http.MethodDelete)
		return
	}
	username := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/users/"))
	if username == "" || strings.Contains(username, "/") {
		http.NotFound(w, r)
		return
	}
	if err := a.deleteUser(username); err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			http.NotFound(w, r)
		case errors.Is(err, errBadUserRequest):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete user failed"})
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if err := a.enforceOrigin(r); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	user, ok := a.authenticatedUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(req.CurrentPassword) == "" || strings.TrimSpace(req.NewPassword) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "current password and new password are required"})
		return
	}
	ok, err := a.checkPassword(user.Username, req.CurrentPassword)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "change password failed"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "current password is incorrect"})
		return
	}
	if err := a.changeOwnPassword(user.Username, req.NewPassword); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errBadUserRequest) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	if cookie, err := r.Cookie(cookieName); err == nil && cookie.Value != "" {
		_ = a.deleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.cfg.CookieSecure,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleItems(w http.ResponseWriter, r *http.Request) {
	if err := a.enforceOrigin(r); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	switch r.Method {
	case http.MethodGet:
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
		writeJSON(w, http.StatusOK, items)
	case http.MethodPost:
		user, _ := a.authenticatedUser(r)
		item, err := a.createItem(r.Context(), user, r)
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, errPayloadTooLarge):
				status = http.StatusRequestEntityTooLarge
			case errors.Is(err, errBadItemRequest):
				status = http.StatusBadRequest
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (a *App) handleItemByID(w http.ResponseWriter, r *http.Request) {
	if err := a.enforceOrigin(r); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if r.Method != http.MethodDelete {
		methodNotAllowed(w, http.MethodDelete)
		return
	}
	itemID := strings.TrimPrefix(r.URL.Path, "/api/items/")
	if itemID == "" || strings.Contains(itemID, "/") {
		http.NotFound(w, r)
		return
	}
	user, ok := a.authenticatedUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	if err := a.deleteItem(itemID, user); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, errPermissionDenied) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission denied"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	user, ok := a.authenticatedUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	attachment, filePath, found := a.lookupAttachment(strings.TrimPrefix(r.URL.Path, "/api/files/"), user)
	if !found {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", attachment.Name))
	http.ServeFile(w, r, filePath)
}

func (a *App) handleImagePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	user, ok := a.authenticatedUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	attachment, filePath, found := a.lookupAttachment(strings.TrimPrefix(r.URL.Path, "/api/previews/"), user)
	if !found || !isSafePreviewMimeType(attachment.MimeType) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", attachment.MimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", attachment.Name))
	http.ServeFile(w, r, filePath)
}

var (
	errPayloadTooLarge = errors.New("attachment exceeds 50MB limit")
	errBadItemRequest  = errors.New("bad item request")
	errBadUserRequest  = errors.New("bad user request")
	errPermissionDenied = errors.New("permission denied")
)

func (a *App) createItem(ctx context.Context, user authUser, r *http.Request) (_ Item, err error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxAttachmentBytes*20)
	if err := r.ParseMultipartForm(maxAttachmentBytes); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "request body too large") || strings.Contains(strings.ToLower(err.Error()), "http: request body too large") {
			return Item{}, errPayloadTooLarge
		}
		return Item{}, newBadItemRequest(fmt.Sprintf("parse form: %v", err))
	}
	message := strings.TrimSpace(r.FormValue("message"))
	if message == "" && len(r.MultipartForm.File["attachments"]) == 0 {
		return Item{}, newBadItemRequest("message or attachment is required")
	}
	if len(message) > maxMessageLength {
		return Item{}, newBadItemRequest(fmt.Sprintf("message exceeds %d characters", maxMessageLength))
	}
	visibility, err := normalizeVisibility(r.FormValue("visibility"))
	if err != nil {
		return Item{}, err
	}
	item := Item{ID: randomID(), Message: message, CreatedAt: time.Now().UTC(), CreatedBy: user.Username, Visibility: visibility, Attachments: []Attachment{}}
	itemDir := filepath.Join(a.cfg.DataDir, "files", item.ID)
	cleanupPaths := []string{itemDir}
	committed := false
	defer func() {
		if err != nil && !committed {
			cleanupFiles(cleanupPaths)
		}
	}()
	if err := os.MkdirAll(itemDir, 0o700); err != nil {
		return Item{}, fmt.Errorf("mkdir item dir: %w", err)
	}
	files := r.MultipartForm.File["attachments"]
	for _, header := range files {
		if header.Size > maxAttachmentBytes {
			return Item{}, errPayloadTooLarge
		}
		file, err := header.Open()
		if err != nil {
			return Item{}, fmt.Errorf("open upload: %w", err)
		}
		defer file.Close()
		storedName := randomID() + filepath.Ext(header.Filename)
		targetPath := filepath.Join(itemDir, storedName)
		target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
		if err != nil {
			return Item{}, fmt.Errorf("create target: %w", err)
		}
		written, copyErr := io.Copy(target, io.LimitReader(file, maxAttachmentBytes+1))
		closeErr := target.Close()
		if copyErr != nil {
			return Item{}, fmt.Errorf("save attachment: %w", copyErr)
		}
		if closeErr != nil {
			return Item{}, fmt.Errorf("close attachment: %w", closeErr)
		}
		if written > maxAttachmentBytes {
			_ = os.Remove(targetPath)
			return Item{}, errPayloadTooLarge
		}
		mimeType := header.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(header.Filename)))
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		attachment := Attachment{
			Name:       header.Filename,
			StoredName: storedName,
			Size:       written,
			MimeType:   mimeType,
			URL:        "/api/files/" + item.ID + "/" + storedName,
			PreviewURL: previewURL(item.ID, storedName, mimeType),
		}
		item.Attachments = append(item.Attachments, attachment)
		cleanupPaths = append(cleanupPaths, targetPath)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO items (id, message, created_at, created_by, visibility) VALUES (?, ?, ?, ?, ?)`, item.ID, item.Message, item.CreatedAt.Format(time.RFC3339Nano), item.CreatedBy, item.Visibility); err != nil {
		_ = tx.Rollback()
		return Item{}, err
	}
	for _, attachment := range item.Attachments {
		if _, err := tx.ExecContext(ctx, `INSERT INTO attachments (item_id, stored_name, name, size, mime_type) VALUES (?, ?, ?, ?, ?)`, item.ID, attachment.StoredName, attachment.Name, attachment.Size, attachment.MimeType); err != nil {
			_ = tx.Rollback()
			return Item{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Item{}, err
	}
	committed = true
	return item, nil
}

func (a *App) deleteItem(id string, requester authUser) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	item, err := a.getItemByIDLocked(id)
	if err != nil {
		return err
	}
	if !requester.IsAdmin && item.CreatedBy != requester.Username {
		return errPermissionDenied
	}
	filesPath := filepath.Join(a.cfg.DataDir, "files", id)
	trashPath := filepath.Join(a.cfg.DataDir, ".trash", id+"-"+randomID())
	if err := os.MkdirAll(filepath.Dir(trashPath), 0o700); err != nil {
		return err
	}
	movedToTrash := false
	if _, err := os.Stat(filesPath); err == nil {
		if err := os.Rename(filesPath, trashPath); err != nil {
			return err
		}
		movedToTrash = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tx, err := a.db.BeginTx(context.Background(), nil)
	if err != nil {
		if movedToTrash {
			_ = os.Rename(trashPath, filesPath)
		}
		return err
	}
	if _, err := tx.Exec(`DELETE FROM attachments WHERE item_id = ?`, id); err != nil {
		_ = tx.Rollback()
		if movedToTrash {
			_ = os.Rename(trashPath, filesPath)
		}
		return err
	}
	if _, err := tx.Exec(`DELETE FROM items WHERE id = ?`, id); err != nil {
		_ = tx.Rollback()
		if movedToTrash {
			_ = os.Rename(trashPath, filesPath)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		if movedToTrash {
			_ = os.Rename(trashPath, filesPath)
		}
		return err
	}
	if movedToTrash {
		if err := os.RemoveAll(trashPath); err != nil {
			_, _ = a.db.Exec(`INSERT OR IGNORE INTO items (id, message, created_at, created_by, visibility) VALUES (?, ?, ?, ?, ?)`, item.ID, item.Message, item.CreatedAt.Format(time.RFC3339Nano), item.CreatedBy, item.Visibility)
			for _, attachment := range item.Attachments {
				_, _ = a.db.Exec(`INSERT OR IGNORE INTO attachments (item_id, stored_name, name, size, mime_type) VALUES (?, ?, ?, ?, ?)`, item.ID, attachment.StoredName, attachment.Name, attachment.Size, attachment.MimeType)
			}
			_ = os.Rename(trashPath, filesPath)
			return err
		}
	}
	return nil
}

func (a *App) listItemsForUser(user authUser) ([]Item, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	query := `SELECT id, message, created_at, created_by, visibility FROM items ORDER BY created_at DESC`
	var rows *sql.Rows
	var err error
	if user.IsAdmin {
		rows, err = a.db.Query(query)
	} else {
		rows, err = a.db.Query(`SELECT id, message, created_at, created_by, visibility FROM items WHERE visibility = ? OR created_by = ? ORDER BY created_at DESC`, visibilityShared, user.Username)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		var item Item
		var createdAt string
		if err := rows.Scan(&item.ID, &item.Message, &createdAt, &item.CreatedBy, &item.Visibility); err != nil {
			return nil, err
		}
		if item.Visibility == "" {
			item.Visibility = visibilityShared
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		attachments, err := a.listAttachments(item.ID)
		if err != nil {
			return nil, err
		}
		item.Attachments = attachments
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *App) listAttachments(itemID string) ([]Attachment, error) {
	rows, err := a.db.Query(`SELECT stored_name, name, size, mime_type FROM attachments WHERE item_id = ? ORDER BY rowid`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	attachments := []Attachment{}
	for rows.Next() {
		var attachment Attachment
		if err := rows.Scan(&attachment.StoredName, &attachment.Name, &attachment.Size, &attachment.MimeType); err != nil {
			return nil, err
		}
		attachment.URL = "/api/files/" + itemID + "/" + attachment.StoredName
		attachment.PreviewURL = previewURL(itemID, attachment.StoredName, attachment.MimeType)
		attachments = append(attachments, attachment)
	}
	return attachments, rows.Err()
}

func (a *App) lookupAttachment(trimmed string, user authUser) (Attachment, string, bool) {
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Attachment{}, "", false
	}
	itemID, storedName := parts[0], parts[1]
	a.mu.RLock()
	defer a.mu.RUnlock()
	item, err := a.getItemByIDLocked(itemID)
	if err != nil || !canAccessItem(user, item) {
		return Attachment{}, "", false
	}
	attachments, err := a.listAttachments(itemID)
	if err != nil {
		return Attachment{}, "", false
	}
	for _, attachment := range attachments {
		if attachment.StoredName == storedName {
			return attachment, filepath.Join(a.cfg.DataDir, "files", itemID, storedName), true
		}
	}
	return Attachment{}, "", false
}

func (a *App) listUsers() ([]UserRecord, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	rows, err := a.db.Query(`SELECT username, is_admin, created_at, updated_at FROM users ORDER BY username ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []UserRecord{}
	for rows.Next() {
		var user UserRecord
		var createdAt, updatedAt string
		var isAdmin int
		if err := rows.Scan(&user.Username, &isAdmin, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		user.IsAdmin = isAdmin == 1
		user.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		user.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		users = append(users, user)
	}
	return users, rows.Err()
}

func (a *App) upsertUser(username, password string, isAdmin bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	trimmedUsername := strings.TrimSpace(username)
	trimmedPassword := strings.TrimSpace(password)
	if trimmedUsername == "" || trimmedPassword == "" {
		return errBadUserRequest
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	hash, err := hashPassword(trimmedPassword)
	if err != nil {
		return err
	}
	adminValue := 0
	if isAdmin {
		adminValue = 1
	}
	_, err = a.db.Exec(`INSERT INTO users (username, password_hash, is_admin, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash, is_admin = excluded.is_admin, updated_at = excluded.updated_at`, trimmedUsername, hash, adminValue, now, now)
	return err
}

func (a *App) changeOwnPassword(username, newPassword string) error {
	trimmedUsername := strings.TrimSpace(username)
	trimmedPassword := strings.TrimSpace(newPassword)
	if trimmedUsername == "" || trimmedPassword == "" {
		return errBadUserRequest
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	hash, err := hashPassword(trimmedPassword)
	if err != nil {
		return err
	}
	result, err := a.db.Exec(`UPDATE users SET password_hash = ?, updated_at = ? WHERE username = ?`, hash, time.Now().UTC().Format(time.RFC3339Nano), trimmedUsername)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return os.ErrNotExist
	}
	_, err = a.db.Exec(`DELETE FROM sessions WHERE username = ?`, trimmedUsername)
	return err
}

func (a *App) deleteUser(username string) error {
	trimmedUsername := strings.TrimSpace(username)
	if trimmedUsername == "" {
		return errBadUserRequest
	}
	if trimmedUsername == strings.TrimSpace(a.cfg.BootstrapAdminUsername) {
		return fmt.Errorf("%w: bootstrap admin cannot be deleted", errBadUserRequest)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	result, err := a.db.Exec(`DELETE FROM users WHERE username = ?`, trimmedUsername)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return os.ErrNotExist
	}
	_, err = a.db.Exec(`DELETE FROM sessions WHERE username = ?`, trimmedUsername)
	return err
}

func (a *App) checkPassword(username, password string) (bool, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var hash string
	if err := a.db.QueryRow(`SELECT password_hash FROM users WHERE username = ?`, strings.TrimSpace(username)).Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if strings.HasPrefix(hash, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil, nil
	}
	expected := legacyHashPassword(username, password, a.cfg.SessionSecret)
	if hmac.Equal([]byte(hash), []byte(expected)) {
		_ = a.upgradePasswordHash(username, password)
		return true, nil
	}
	return false, nil
}

func (a *App) initDatabase() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS users (
			username TEXT PRIMARY KEY,
			password_hash TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS items (
			id TEXT PRIMARY KEY,
			message TEXT NOT NULL,
			created_at TEXT NOT NULL,
			created_by TEXT NOT NULL,
			visibility TEXT NOT NULL DEFAULT 'shared'
		);`,
		`CREATE TABLE IF NOT EXISTS attachments (
			item_id TEXT NOT NULL,
			stored_name TEXT NOT NULL,
			name TEXT NOT NULL,
			size INTEGER NOT NULL,
			mime_type TEXT NOT NULL,
			PRIMARY KEY (item_id, stored_name),
			FOREIGN KEY(item_id) REFERENCES items(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			user_version TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(username) REFERENCES users(username) ON DELETE CASCADE
		);`,
	}
	for _, stmt := range stmts {
		if _, err := a.db.Exec(stmt); err != nil {
			return err
		}
	}
	if _, err := a.db.Exec(`ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
	}
	if _, err := a.db.Exec(`ALTER TABLE users ADD COLUMN updated_at TEXT`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
	}
	if _, err := a.db.Exec(`ALTER TABLE items ADD COLUMN visibility TEXT NOT NULL DEFAULT 'shared'`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
	}
	if _, err := a.db.Exec(`UPDATE users SET updated_at = COALESCE(NULLIF(updated_at, ''), created_at, ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if _, err := a.db.Exec(`UPDATE items SET visibility = COALESCE(NULLIF(visibility, ''), 'shared')`); err != nil {
		return err
	}
	if _, err := a.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err := a.bootstrapAdmin(); err != nil {
		return err
	}
	if err := a.migrateLegacyItemsJSON(); err != nil {
		return err
	}
	return nil
}

func (a *App) bootstrapAdmin() error {
	username := strings.TrimSpace(a.cfg.BootstrapAdminUsername)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var existingHash string
	var isAdmin int
	err := a.db.QueryRow(`SELECT password_hash, is_admin FROM users WHERE username = ?`, username).Scan(&existingHash, &isAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		hash, hashErr := hashPassword(a.cfg.BootstrapAdminPassword)
		if hashErr != nil {
			return hashErr
		}
		_, err = a.db.Exec(`INSERT INTO users (username, password_hash, is_admin, created_at, updated_at) VALUES (?, ?, 1, ?, ?)`, username, hash, now, now)
		return err
	}
	if err != nil {
		return err
	}

	passwordMatches := false
	if strings.HasPrefix(existingHash, "$2") {
		passwordMatches = bcrypt.CompareHashAndPassword([]byte(existingHash), []byte(a.cfg.BootstrapAdminPassword)) == nil
	} else {
		passwordMatches = hmac.Equal([]byte(existingHash), []byte(legacyHashPassword(username, a.cfg.BootstrapAdminPassword, a.cfg.SessionSecret)))
	}
	if passwordMatches && isAdmin == 1 {
		return nil
	}

	hash, err := hashPassword(a.cfg.BootstrapAdminPassword)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(`UPDATE users SET password_hash = ?, is_admin = 1, updated_at = ? WHERE username = ?`, hash, now, username)
	return err
}

func (a *App) createSession(username, version string) (string, time.Time, error) {
	expiresAt := time.Now().UTC().Add(sessionLifetime)
	token := randomID() + randomID()
	tokenHash := sha256Hex(token)
	_, err := a.db.Exec(
		`INSERT INTO sessions (token_hash, username, user_version, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`,
		tokenHash,
		username,
		version,
		expiresAt.Format(time.RFC3339Nano),
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func (a *App) deleteSession(token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	_, err := a.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, sha256Hex(token))
	return err
}

func (a *App) lookupSession(token string) (authSession, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var session authSession
	var expiresAt string
	err := a.db.QueryRow(`SELECT username, user_version, expires_at FROM sessions WHERE token_hash = ?`, sha256Hex(token)).Scan(&session.Username, &session.Version, &expiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authSession{}, os.ErrNotExist
		}
		return authSession{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return authSession{}, err
	}
	session.ExpiresAt = parsed
	return session, nil
}

func (a *App) migrateLegacyItemsJSON() error {
	legacyPath := filepath.Join(a.cfg.DataDir, "items.json")
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	var items []Item
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.CreatedBy == "" {
			item.CreatedBy = a.cfg.BootstrapAdminUsername
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO items (id, message, created_at, created_by) VALUES (?, ?, ?, ?)`, item.ID, item.Message, item.CreatedAt.Format(time.RFC3339Nano), item.CreatedBy); err != nil {
			_ = tx.Rollback()
			return err
		}
		for _, attachment := range item.Attachments {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO attachments (item_id, stored_name, name, size, mime_type) VALUES (?, ?, ?, ?, ?)`, item.ID, attachment.StoredName, attachment.Name, attachment.Size, attachment.MimeType); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return os.Rename(legacyPath, legacyPath+".migrated")
}

func (a *App) getItemByIDLocked(id string) (Item, error) {
	var item Item
	var createdAt string
	if err := a.db.QueryRow(`SELECT id, message, created_at, created_by, visibility FROM items WHERE id = ?`, id).Scan(&item.ID, &item.Message, &createdAt, &item.CreatedBy, &item.Visibility); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Item{}, os.ErrNotExist
		}
		return Item{}, err
	}
	if item.Visibility == "" {
		item.Visibility = visibilityShared
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	attachments, err := a.listAttachments(id)
	if err != nil {
		return Item{}, err
	}
	item.Attachments = attachments
	return item, nil
}

func normalizeVisibility(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", visibilityShared:
		return visibilityShared, nil
	case visibilityPrivate:
		return visibilityPrivate, nil
	default:
		return "", newBadItemRequest("visibility must be shared or private")
	}
}

func canAccessItem(user authUser, item Item) bool {
	if user.IsAdmin {
		return true
	}
	return item.Visibility == visibilityShared || item.CreatedBy == user.Username
}

func (a *App) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.authenticatedUser(r); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		next(w, r)
	}
}

func (a *App) authenticatedUser(r *http.Request) (authUser, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil || cookie.Value == "" {
		return authUser{}, false
	}
	session, err := a.lookupSession(cookie.Value)
	if err != nil {
		return authUser{}, false
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		_ = a.deleteSession(cookie.Value)
		return authUser{}, false
	}
	user, err := a.lookupAuthUser(session.Username)
	if err != nil {
		return authUser{}, false
	}
	if user.Version != session.Version {
		_ = a.deleteSession(cookie.Value)
		return authUser{}, false
	}
	return user, true
}

func (a *App) lookupAuthUser(username string) (authUser, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var user authUser
	var isAdmin int
	if err := a.db.QueryRow(`SELECT username, is_admin, updated_at FROM users WHERE username = ?`, username).Scan(&user.Username, &isAdmin, &user.Version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authUser{}, os.ErrNotExist
		}
		return authUser{}, err
	}
	user.IsAdmin = isAdmin == 1
	return user, nil
}

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func legacyHashPassword(username, password, sessionSecret string) string {
	mac := hmac.New(sha256.New, []byte(sessionSecret))
	_, _ = mac.Write([]byte(username))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(password))
	return hex.EncodeToString(mac.Sum(nil))
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (a *App) upgradePasswordHash(username, password string) error {
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(`UPDATE users SET password_hash = ?, updated_at = ? WHERE username = ?`, hash, time.Now().UTC().Format(time.RFC3339Nano), strings.TrimSpace(username))
	return err
}

func randomID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
}

func previewURL(itemID, storedName, mimeType string) string {
	if isSafePreviewMimeType(mimeType) {
		return "/api/previews/" + itemID + "/" + storedName
	}
	return ""
}

func isSafePreviewMimeType(mimeType string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0])) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func cleanupFiles(paths []string) {
	for _, path := range paths {
		_ = os.RemoveAll(path)
	}
}

func (a *App) enforceOrigin(r *http.Request) error {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return nil
	}
	if len(a.cfg.AllowedOrigins) == 0 {
		return nil
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return errors.New("missing origin")
	}
	for _, allowed := range a.cfg.AllowedOrigins {
		if origin == allowed {
			return nil
		}
	}
	return errors.New("origin not allowed")
}
