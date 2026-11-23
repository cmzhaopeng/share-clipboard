package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
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
