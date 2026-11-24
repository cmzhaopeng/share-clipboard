package app

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) (*App, *httptest.Server, string) {
	t.Helper()
	tempDir := t.TempDir()
	application, err := New(Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "tester",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowedOrigins:         []string{"http://127.0.0.1:9999"},
		AllowInsecureHTTP:      true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	return application, server, tempDir
}

func loggedInClient(t *testing.T, baseURL, username, password string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginJSON(t, client, baseURL, username, password)
	return client
}

func createAPIToken(t *testing.T, client *http.Client, baseURL, name string) APIToken {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/tokens", strings.NewReader(`{"name":"`+name+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/tokens: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/tokens status=%d body=%s", resp.StatusCode, b)
	}
	var token APIToken
	_ = json.NewDecoder(resp.Body).Decode(&token)
	if !strings.HasPrefix(token.Token, apiTokenPrefix) {
		t.Fatalf("token = %q, want %s prefix", token.Token, apiTokenPrefix)
	}
	return token
}

func bearerDo(t *testing.T, method, rawURL, token, contentType string, body io.Reader) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, rawURL, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestAPITokenLifecycleAndShortcutEndpoints(t *testing.T) {
	_, server, _ := newTestServer(t)
	client := loggedInClient(t, server.URL, "tester", "secret-pass")
	token := createAPIToken(t, client, server.URL, "iPhone 快捷指令")

	// Listing never returns the secret again.
	resp, err := client.Get(server.URL + "/api/tokens")
	if err != nil {
		t.Fatal(err)
	}
	var listed []APIToken
	_ = json.NewDecoder(resp.Body).Decode(&listed)
	resp.Body.Close()
	if len(listed) != 1 || listed[0].Token != "" || listed[0].Name != "iPhone 快捷指令" {
		t.Fatalf("listed tokens = %+v", listed)
	}

	// JSON create without an Origin header (as iOS Shortcuts sends it).
	resp2, body := bearerDo(t, http.MethodPost, server.URL+"/api/items", token.Token, "application/json", strings.NewReader(`{"message":"from shortcut"}`))
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("bearer JSON create status=%d body=%s", resp2.StatusCode, body)
	}
	resp2, body = bearerDo(t, http.MethodGet, server.URL+"/api/latest", token.Token, "", nil)
	if resp2.StatusCode != http.StatusOK || body != "from shortcut" || !strings.HasPrefix(resp2.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("latest status=%d type=%q body=%q", resp2.StatusCode, resp2.Header.Get("Content-Type"), body)
	}

	// urlencoded form create.
	form := url.Values{"message": {"form text"}, "visibility": {"private"}}
	resp2, body = bearerDo(t, http.MethodPost, server.URL+"/api/items", token.Token, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if resp2.StatusCode != http.StatusCreated || !strings.Contains(body, `"visibility":"private"`) {
		t.Fatalf("bearer form create status=%d body=%s", resp2.StatusCode, body)
	}

	// Attachment-only item: /api/latest returns the file itself.
	var buf bytes.Buffer
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32))
	w := newMultipartWithFile(t, &buf, "attachments", "截图", "application/octet-stream", png)
	resp2, body = bearerDo(t, http.MethodPost, server.URL+"/api/items", token.Token, w, &buf)
	if resp2.StatusCode != http.StatusCreated || !strings.Contains(body, `"mimeType":"image/png"`) {
		t.Fatalf("bearer file create status=%d body=%s", resp2.StatusCode, body)
	}
	resp2, body = bearerDo(t, http.MethodGet, server.URL+"/api/latest", token.Token, "", nil)
	if resp2.Header.Get("Content-Type") != "image/png" || body != string(png) {
		t.Fatalf("latest file type=%q len=%d", resp2.Header.Get("Content-Type"), len(body))
	}

	// A token cannot manage tokens.
	resp2, _ = bearerDo(t, http.MethodPost, server.URL+"/api/tokens", token.Token, "application/json", strings.NewReader(`{"name":"x"}`))
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("token minting token status=%d, want 403", resp2.StatusCode)
	}

	// A bad bearer never falls back to the cookie session.
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/items", nil)
	req.Header.Set("Authorization", "Bearer scb_wrong")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad bearer with cookie status=%d, want 401", resp.StatusCode)
	}

	// Revoke.
	req, _ = http.NewRequest(http.MethodDelete, server.URL+"/api/tokens/"+token.ID, nil)
	req.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke status=%d", resp.StatusCode)
	}
	resp2, _ = bearerDo(t, http.MethodGet, server.URL+"/api/latest", token.Token, "", nil)
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token status=%d, want 401", resp2.StatusCode)
	}
}

func TestBrowserPostStillRequiresOrigin(t *testing.T) {
	_, server, _ := newTestServer(t)
	client := loggedInClient(t, server.URL, "tester", "secret-pass")
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/items", strings.NewReader(`{"message":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cookie POST without Origin status=%d, want 403", resp.StatusCode)
	}
}

func newMultipartWithFile(t *testing.T, buf *bytes.Buffer, field, fileName, contentType string, content []byte) string {
	t.Helper()
	boundary := "testboundary123"
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString(`Content-Disposition: form-data; name="` + field + `"; filename="` + fileName + `"` + "\r\n")
	buf.WriteString("Content-Type: " + contentType + "\r\n\r\n")
	buf.Write(content)
	buf.WriteString("\r\n--" + boundary + "--\r\n")
	return "multipart/form-data; boundary=" + boundary
}

func TestDetectMimeType(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "noext")
	_ = os.WriteFile(png, []byte("\x89PNG\r\n\x1a\n0000000000"), 0o600)
	cases := []struct{ declared, name, path, want string }{
		{"application/pdf", "x.bin", "", "application/pdf"},
		{"application/octet-stream", "报告.PDF", "", "application/pdf"},
		{"", "photo.heic", "", "image/heic"},
		{"application/octet-stream", "notes.md", "", "text/markdown"},
		{"application/octet-stream", "noext", png, "image/png"},
		{"application/octet-stream", "blob", "", "application/octet-stream"},
	}
	for _, c := range cases {
		if got := detectMimeType(c.declared, c.name, c.path); got != c.want {
			t.Errorf("detectMimeType(%q, %q) = %q, want %q", c.declared, c.name, got, c.want)
		}
	}
}

func TestStartupFixesGenericMimeTypes(t *testing.T) {
	application, server, dataDir := newTestServer(t)
	client := loggedInClient(t, server.URL, "tester", "secret-pass")
	item, _ := createItemWithContentType(t, client, server.URL, dataDir, "old", "doc.pdf", "application/pdf", []byte("%PDF-1.4"))
	// Simulate a row written by an older version.
	if _, err := application.db.Exec(`UPDATE attachments SET mime_type = 'application/octet-stream'`); err != nil {
		t.Fatal(err)
	}
	if err := application.fixGenericMimeTypes(); err != nil {
		t.Fatal(err)
	}
	attachments, _ := application.listAttachments(item.ID)
	if attachments[0].MimeType != "application/pdf" || attachments[0].InlineURL == "" {
		t.Fatalf("after fix: %+v", attachments[0])
	}
}

func TestEventsStreamNotifiesOnlyAuthorizedUsers(t *testing.T) {
	_, server, _ := newTestServer(t)
	admin := loggedInClient(t, server.URL, "tester", "secret-pass")
	upsertUser(t, admin, server.URL, "alice", "alice-pass-123", false)
	upsertUser(t, admin, server.URL, "bob", "bob-pass-123", false)
	alice := loggedInClient(t, server.URL, "alice", "alice-pass-123")
	bob := loggedInClient(t, server.URL, "bob", "bob-pass-123")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	openStream := func(c *http.Client) *bufio.Reader {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/events", nil)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("GET /api/events: %v", err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatalf("events status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		return bufio.NewReader(resp.Body)
	}
	nextEvent := func(r *bufio.Reader) itemEvent {
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Fatalf("read event: %v", err)
			}
			if strings.HasPrefix(line, "data: ") {
				var ev itemEvent
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev)
				return ev
			}
		}
	}
	aliceStream := openStream(alice)
	bobStream := openStream(bob)
	time.Sleep(100 * time.Millisecond) // let both subscriptions register

	private := createTextOnlyItemWithVisibility(t, alice, server.URL, "alice only", "private")
	shared := createTextOnlyItemWithVisibility(t, alice, server.URL, "for everyone", "shared")

	if ev := nextEvent(aliceStream); ev.Type != "item.created" || ev.ID != private.ID {
		t.Fatalf("alice first event = %+v, want private item", ev)
	}
	// Bob must not hear about Alice's private item: his first event is the shared one.
	if ev := nextEvent(bobStream); ev.ID != shared.ID {
		t.Fatalf("bob first event = %+v, want shared item %s", ev, shared.ID)
	}
}

func TestHEICHelpers(t *testing.T) {
	if !isHEIC("image/heic", "x") || !isHEIC("application/octet-stream", "IMG_1.HEIC") || isHEIC("image/jpeg", "a.jpg") {
		t.Fatal("isHEIC misclassified")
	}
	if got := jpegName("Clipboard 2026.heic"); got != "Clipboard 2026.jpg" {
		t.Fatalf("jpegName = %q", got)
	}
	if heifConverter() == "" {
		t.Skip("heif-convert not installed; conversion covered on the server")
	}
}

func TestDeletingUserRevokesTheirTokens(t *testing.T) {
	_, server, _ := newTestServer(t)
	admin := loggedInClient(t, server.URL, "tester", "secret-pass")
	upsertUser(t, admin, server.URL, "carol", "carol-pass-123", false)
	carol := loggedInClient(t, server.URL, "carol", "carol-pass-123")
	token := createAPIToken(t, carol, server.URL, "phone")

	deleteUser(t, admin, server.URL, "carol", http.StatusNoContent)
	upsertUser(t, admin, server.URL, "carol", "new-carol-pass-1", false)

	if resp, _ := bearerDo(t, http.MethodGet, server.URL+"/api/items", token.Token, "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token after user re-creation status=%d, want 401", resp.StatusCode)
	}
}

func TestAdminTokenCannotManageAccounts(t *testing.T) {
	_, server, _ := newTestServer(t)
	admin := loggedInClient(t, server.URL, "tester", "secret-pass")
	upsertUser(t, admin, server.URL, "dave", "dave-pass-123", false)
	token := createAPIToken(t, admin, server.URL, "admin phone").Token

	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/api/users", ""},
		{http.MethodPost, "/api/users", `{"username":"mallory","password":"x-pass-123","isAdmin":true}`},
		{http.MethodDelete, "/api/users/dave", ""},
		{http.MethodPost, "/api/users/change-password", `{"currentPassword":"secret-pass","newPassword":"pwned-pass-1"}`},
		{http.MethodGet, "/api/tokens", ""},
	}
	for _, c := range cases {
		var body io.Reader
		if c.body != "" {
			body = strings.NewReader(c.body)
		}
		if resp, out := bearerDo(t, c.method, server.URL+c.path, token, "application/json", body); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s with admin token: status=%d body=%s, want 403", c.method, c.path, resp.StatusCode, out)
		}
	}
	// Clipboard endpoints still work with the token.
	if resp, out := bearerDo(t, http.MethodPost, server.URL+"/api/items", token, "application/json", strings.NewReader(`{"message":"ok"}`)); resp.StatusCode != http.StatusCreated {
		t.Fatalf("token create item status=%d body=%s", resp.StatusCode, out)
	}
	// The browser session can still manage accounts, and nothing above took effect.
	assertLoginStatus(t, admin, server.URL, "dave", "dave-pass-123", http.StatusNoContent)
	assertLoginStatus(t, admin, server.URL, "tester", "secret-pass", http.StatusNoContent)
}

func TestTokenUploadsDefaultPrivateAndAuthorCanToggle(t *testing.T) {
	_, server, _ := newTestServer(t)
	admin := loggedInClient(t, server.URL, "tester", "secret-pass")
	upsertUser(t, admin, server.URL, "erin", "erin-pass-123", false)
	upsertUser(t, admin, server.URL, "frank", "frank-pass-123", false)
	erin := loggedInClient(t, server.URL, "erin", "erin-pass-123")
	frank := loggedInClient(t, server.URL, "frank", "frank-pass-123")
	token := createAPIToken(t, erin, server.URL, "phone").Token

	// No visibility given: a token upload stays private to its author.
	resp, body := bearerDo(t, http.MethodPost, server.URL+"/api/items", token, "application/json", strings.NewReader(`{"message":"from phone"}`))
	if resp.StatusCode != http.StatusCreated || !strings.Contains(body, `"visibility":"private"`) {
		t.Fatalf("token default create status=%d body=%s", resp.StatusCode, body)
	}
	var item Item
	_ = json.Unmarshal([]byte(body), &item)
	if item.Source != sourceAPI || item.SourceName != "phone" {
		t.Fatalf("token item source=%q name=%q, want api/phone", item.Source, item.SourceName)
	}
	// An explicit visibility is still honored.
	resp, body = bearerDo(t, http.MethodPost, server.URL+"/api/items", token, "application/json", strings.NewReader(`{"message":"x","visibility":"shared"}`))
	if resp.StatusCode != http.StatusCreated || !strings.Contains(body, `"visibility":"shared"`) {
		t.Fatalf("token explicit shared status=%d body=%s", resp.StatusCode, body)
	}
	// Browser uploads keep the shared default.
	if browser := createTextOnlyItemWithVisibility(t, erin, server.URL, "browser", ""); browser.Visibility != visibilityShared || browser.Source != sourceWeb || browser.SourceName != "" {
		t.Fatalf("browser item visibility=%q source=%q name=%q, want shared/web", browser.Visibility, browser.Source, browser.SourceName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/events", nil)
	stream, err := frank.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	time.Sleep(100 * time.Millisecond)

	patch := func(c *http.Client, visibility string) int {
		req, _ := http.NewRequest(http.MethodPatch, server.URL+"/api/items/"+item.ID, strings.NewReader(`{"visibility":"`+visibility+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://127.0.0.1:9999")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	// Other users cannot see it, the admin can see it but cannot reshare it.
	if got := patch(frank, "shared"); got != http.StatusNotFound {
		t.Fatalf("frank patch status=%d, want 404", got)
	}
	if got := patch(admin, "shared"); got != http.StatusForbidden {
		t.Fatalf("admin patch status=%d, want 403", got)
	}
	if got := patch(erin, "bogus"); got != http.StatusBadRequest {
		t.Fatalf("bad visibility status=%d, want 400", got)
	}
	if got := patch(erin, "shared"); got != http.StatusOK {
		t.Fatalf("erin share status=%d, want 200", got)
	}
	// Frank is told about the newly shared item and now sees it.
	reader := bufio.NewReader(stream.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read event: %v", err)
		}
		if strings.HasPrefix(line, "data: ") {
			if !strings.Contains(line, `"item.updated"`) || !strings.Contains(line, item.ID) {
				t.Fatalf("frank event = %s", line)
			}
			break
		}
	}
	// The list endpoint returns the source too.
	if got := listedItem(t, erin, server.URL, item.ID); got.Source != sourceAPI || got.SourceName != "phone" {
		t.Fatalf("listed item source=%q name=%q", got.Source, got.SourceName)
	}
	if !itemsContain(t, frank, server.URL, item.ID) {
		t.Fatal("frank cannot see the item after it was shared")
	}
	if got := patch(erin, "private"); got != http.StatusOK {
		t.Fatalf("erin unshare status=%d", got)
	}
	if itemsContain(t, frank, server.URL, item.ID) {
		t.Fatal("frank still sees the item after it was made private")
	}
}

func itemsContain(t *testing.T, c *http.Client, baseURL, id string) bool {
	t.Helper()
	resp, err := c.Get(baseURL + "/api/items")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var items []Item
	_ = json.NewDecoder(resp.Body).Decode(&items)
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

func listedItem(t *testing.T, c *http.Client, baseURL, id string) Item {
	t.Helper()
	resp, err := c.Get(baseURL + "/api/items")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var items []Item
	_ = json.NewDecoder(resp.Body).Decode(&items)
	for _, it := range items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("item %s not listed", id)
	return Item{}
}

func TestMigrationAddsItemSourceColumns(t *testing.T) {
	dir := t.TempDir()
	old, err := sql.Open("sqlite", filepath.Join(dir, "clipboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE items (id TEXT PRIMARY KEY, message TEXT NOT NULL, created_at TEXT NOT NULL, created_by TEXT NOT NULL, visibility TEXT NOT NULL DEFAULT 'shared');
		INSERT INTO items VALUES ('old1', 'hi', '2026-11-20T00:00:00Z', 'tester', 'shared');`); err != nil {
		t.Fatal(err)
	}
	old.Close()
	application, err := New(Config{
		DataDir:                dir,
		BootstrapAdminUsername: "tester",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		AllowInsecureHTTP:      true,
	})
	if err != nil {
		t.Fatalf("New() on old schema: %v", err)
	}
	item, err := application.getItemByIDLocked("old1")
	if err != nil || item.Source != sourceWeb || item.SourceName != "" {
		t.Fatalf("migrated item = %+v, err=%v", item, err)
	}
}

func TestFormFieldNamesIgnoreSpacesAndCase(t *testing.T) {
	_, server, _ := newTestServer(t)
	admin := loggedInClient(t, server.URL, "tester", "secret-pass")
	token := createAPIToken(t, admin, server.URL, "phone").Token

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField(" Visibility ", "shared")
	part, _ := w.CreateFormFile("attachments ", "a.txt")
	_, _ = part.Write([]byte("first"))
	part, _ = w.CreateFormFile("Attachments", "b.txt")
	_, _ = part.Write([]byte("second"))
	_ = w.Close()
	resp, body := bearerDo(t, http.MethodPost, server.URL+"/api/items", token, w.FormDataContentType(), &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create with spaced field names status=%d body=%s", resp.StatusCode, body)
	}
	var item Item
	_ = json.Unmarshal([]byte(body), &item)
	if len(item.Attachments) != 2 || item.Visibility != visibilityShared {
		t.Fatalf("item = %+v, want 2 attachments and shared", item)
	}

	form := url.Values{"message ": {"spaced key"}}
	resp, body = bearerDo(t, http.MethodPost, server.URL+"/api/items", token, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if resp.StatusCode != http.StatusCreated || !strings.Contains(body, `"message":"spaced key"`) {
		t.Fatalf("urlencoded spaced key status=%d body=%s", resp.StatusCode, body)
	}
}
