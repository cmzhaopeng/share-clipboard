package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthCreateListDeleteFlow(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "tester",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	server := httptest.NewServer(application.Handler())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}
	client := &http.Client{Jar: jar}

	resp, err := client.Get(server.URL + "/api/items")
	if err != nil {
		t.Fatalf("GET /api/items error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/items status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	loginJSON(t, client, server.URL, "tester", "secret-pass")

	textOnly := createTextOnlyItem(t, client, server.URL, "text-only message")
	if textOnly.Message != "text-only message" {
		t.Fatalf("text-only item message = %q, want %q", textOnly.Message, "text-only message")
	}
	if textOnly.Attachments == nil {
		t.Fatal("text-only item attachments should marshal as empty array, got nil")
	}
	if len(textOnly.Attachments) != 0 {
		t.Fatalf("text-only attachments len = %d, want 0", len(textOnly.Attachments))
	}

	itemResp, filePath := createItem(t, client, server.URL, tempDir, "hello clipboard", "note.txt", []byte("hello file"))
	if itemResp.Message != "hello clipboard" {
		t.Fatalf("item message = %q, want %q", itemResp.Message, "hello clipboard")
	}
	if len(itemResp.Attachments) != 1 {
		t.Fatalf("attachments len = %d, want 1", len(itemResp.Attachments))
	}
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("attachment file missing: %v", err)
	}

	resp, err = client.Get(server.URL + "/api/items")
	if err != nil {
		t.Fatalf("GET /api/items after create error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/items after create status = %d, want 200", resp.StatusCode)
	}
	var listed []Item
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed len = %d, want 2", len(listed))
	}
	if listed[0].ID != itemResp.ID {
		t.Fatalf("listed first item id = %q, want %q", listed[0].ID, itemResp.ID)
	}
	if listed[1].ID != textOnly.ID {
		t.Fatalf("listed second item id = %q, want %q", listed[1].ID, textOnly.ID)
	}
	if listed[1].Attachments == nil {
		t.Fatal("listed text-only item attachments should marshal as empty array, got nil")
	}

	req, err := http.NewRequest(http.MethodDelete, server.URL+"/api/items/"+itemResp.ID, nil)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	req.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/items/{id} error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", resp.StatusCode)
	}

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("attachment file should be removed, stat err = %v", err)
	}

	resp, err = client.Get(server.URL + "/api/items")
	if err != nil {
		t.Fatalf("GET /api/items after delete error = %v", err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode final list: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed len after delete = %d, want 1", len(listed))
	}
	if listed[0].ID != textOnly.ID {
		t.Fatalf("remaining item id after delete = %q, want %q", listed[0].ID, textOnly.ID)
	}
	if listed[0].Attachments == nil {
		t.Fatal("remaining text-only item attachments should marshal as empty array, got nil")
	}
}

func TestSessionPersistsInSecureCookieAndReturnsAdminFlag(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "tester",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	client := &http.Client{}
	loginBody := strings.NewReader(`{"username":"tester","password":"secret-pass"}`)
	loginReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/login", loginBody)
	if err != nil {
		t.Fatalf("new login request: %v", err)
	}
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err := client.Do(loginReq)
	if err != nil {
		t.Fatalf("login request error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login status = %d, want 204", resp.StatusCode)
	}

	cookies := resp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected auth cookie")
	}
	if !cookies[0].HttpOnly {
		t.Fatal("expected auth cookie to be HttpOnly")
	}
	if strings.TrimSpace(cookies[0].Value) == "" {
		t.Fatal("expected non-empty auth cookie value")
	}

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/session", nil)
	if err != nil {
		t.Fatalf("new session request: %v", err)
	}
	req.AddCookie(cookies[0])
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("session request error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session status = %d, want 200", resp.StatusCode)
	}
	var session sessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if !session.Authenticated || !session.IsAdmin || session.Username != "tester" {
		t.Fatalf("unexpected session response: %+v", session)
	}
}

func TestLogoutRevokesCurrentSessionToken(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "tester",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	client := &http.Client{}
	loginBody := strings.NewReader(`{"username":"tester","password":"secret-pass"}`)
	loginReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/login", loginBody)
	if err != nil {
		t.Fatalf("new login request: %v", err)
	}
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err := client.Do(loginReq)
	if err != nil {
		t.Fatalf("login request error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status = %d body=%s", resp.StatusCode, string(b))
	}
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected auth cookie")
	}
	authCookie := cookies[0]

	logoutReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/logout", nil)
	if err != nil {
		t.Fatalf("new logout request: %v", err)
	}
	logoutReq.Header.Set("Origin", "http://127.0.0.1:9999")
	logoutReq.AddCookie(authCookie)
	resp, err = client.Do(logoutReq)
	if err != nil {
		t.Fatalf("logout request error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("logout status = %d body=%s", resp.StatusCode, string(b))
	}

	sessionReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/session", nil)
	if err != nil {
		t.Fatalf("new session request: %v", err)
	}
	sessionReq.AddCookie(authCookie)
	resp, err = client.Do(sessionReq)
	if err != nil {
		t.Fatalf("session request after logout error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("session status after logout = %d body=%s", resp.StatusCode, string(b))
	}
}

func TestUserManagementAndSQLitePersistence(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "admin",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}
	client := &http.Client{Jar: jar}

	loginJSON(t, client, server.URL, "admin", "secret-pass")
	upsertUser(t, client, server.URL, "alice", "alice-pass-123", false)

	resp, err := client.Get(server.URL + "/api/users")
	if err != nil {
		t.Fatalf("GET /api/users error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/users status=%d body=%s", resp.StatusCode, string(b))
	}
	var users []UserRecord
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		t.Fatalf("decode users: %v", err)
	}
	if len(users) < 2 {
		t.Fatalf("expected at least 2 users, got %d", len(users))
	}

	logoutReq, _ := http.NewRequest(http.MethodPost, server.URL+"/api/logout", nil)
	logoutReq.Header.Set("Origin", "http://127.0.0.1:9999")
	if _, err := client.Do(logoutReq); err != nil {
		t.Fatalf("logout error: %v", err)
	}

	loginJSON(t, client, server.URL, "alice", "alice-pass-123")
	created, _ := createItem(t, client, server.URL, tempDir, "sqlite persisted", "demo.txt", []byte("demo"))

	dbPath := filepath.Join(tempDir, "clipboard.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected sqlite db at %s: %v", dbPath, err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM items WHERE id = ?`, created.ID).Scan(&count); err != nil {
		t.Fatalf("query items count: %v", err)
	}
	if count != 1 {
		t.Fatalf("sqlite item count = %d, want 1", count)
	}

	application2, err := New(cfg)
	if err != nil {
		t.Fatalf("New() second instance error = %v", err)
	}
	server2 := httptest.NewServer(application2.Handler())
	defer server2.Close()
	client2 := &http.Client{Jar: jar}
	resp, err = client2.Get(server2.URL + "/api/items")
	if err != nil {
		t.Fatalf("GET /api/items after restart error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/items after restart status=%d body=%s", resp.StatusCode, string(b))
	}
	var items []Item
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("decode items after restart: %v", err)
	}
	if len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("unexpected items after restart: %+v", items)
	}
}

func TestNonAdminCannotManageUsers(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "admin",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	adminJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New admin: %v", err)
	}
	adminClient := &http.Client{Jar: adminJar}
	loginJSON(t, adminClient, server.URL, "admin", "secret-pass")
	upsertUser(t, adminClient, server.URL, "alice", "alice-pass-123", false)

	userJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New user: %v", err)
	}
	userClient := &http.Client{Jar: userJar}
	loginJSON(t, userClient, server.URL, "alice", "alice-pass-123")

	resp, err := userClient.Get(server.URL + "/api/users")
	if err != nil {
		t.Fatalf("GET /api/users as non-admin error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/users as non-admin status=%d body=%s", resp.StatusCode, string(b))
	}

	body := strings.NewReader(`{"username":"bob","password":"bob-pass-123","isAdmin":false}`)
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/users", body)
	if err != nil {
		t.Fatalf("new non-admin POST /api/users request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err = userClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/users as non-admin error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/users as non-admin status=%d body=%s", resp.StatusCode, string(b))
	}
}

func TestNonAdminSessionStillListsItems(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "admin",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	adminJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New admin: %v", err)
	}
	adminClient := &http.Client{Jar: adminJar}
	loginJSON(t, adminClient, server.URL, "admin", "secret-pass")
	upsertUser(t, adminClient, server.URL, "alice", "alice-pass-123", false)
	createItem(t, adminClient, server.URL, tempDir, "visible to users", "note.txt", []byte("demo"))

	userJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New user: %v", err)
	}
	userClient := &http.Client{Jar: userJar}
	loginJSON(t, userClient, server.URL, "alice", "alice-pass-123")

	resp, err := userClient.Get(server.URL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session as non-admin error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/session as non-admin status=%d body=%s", resp.StatusCode, string(b))
	}
	var session sessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		t.Fatalf("decode non-admin session: %v", err)
	}
	if !session.Authenticated || session.IsAdmin || session.Username != "alice" {
		t.Fatalf("unexpected non-admin session response: %+v", session)
	}

	resp, err = userClient.Get(server.URL + "/api/items")
	if err != nil {
		t.Fatalf("GET /api/items as non-admin error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/items as non-admin status=%d body=%s", resp.StatusCode, string(b))
	}
	var items []Item
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("decode non-admin items: %v", err)
	}
	if len(items) != 1 || items[0].Message != "visible to users" {
		t.Fatalf("unexpected non-admin items: %+v", items)
	}
}

func TestItemVisibilityScopesForUsersAndAdmin(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "admin",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	adminJar, _ := cookiejar.New(nil)
	adminClient := &http.Client{Jar: adminJar}
	loginJSON(t, adminClient, server.URL, "admin", "secret-pass")
	upsertUser(t, adminClient, server.URL, "alice", "alice-pass-123", false)
	upsertUser(t, adminClient, server.URL, "bob", "bob-pass-123", false)

	aliceJar, _ := cookiejar.New(nil)
	aliceClient := &http.Client{Jar: aliceJar}
	loginJSON(t, aliceClient, server.URL, "alice", "alice-pass-123")
	aliceShared := createTextOnlyItemWithVisibility(t, aliceClient, server.URL, "alice shared", "shared")
	alicePrivate := createTextOnlyItemWithVisibility(t, aliceClient, server.URL, "alice private", "private")

	bobJar, _ := cookiejar.New(nil)
	bobClient := &http.Client{Jar: bobJar}
	loginJSON(t, bobClient, server.URL, "bob", "bob-pass-123")
	bobShared := createTextOnlyItemWithVisibility(t, bobClient, server.URL, "bob shared", "shared")
	bobPrivate := createTextOnlyItemWithVisibility(t, bobClient, server.URL, "bob private", "private")

	aliceItems := listItemsForTest(t, aliceClient, server.URL)
	assertItemIDs(t, aliceItems, aliceShared.ID, alicePrivate.ID, bobShared.ID)
	assertItemAbsent(t, aliceItems, bobPrivate.ID)

	bobItems := listItemsForTest(t, bobClient, server.URL)
	assertItemIDs(t, bobItems, aliceShared.ID, bobShared.ID, bobPrivate.ID)
	assertItemAbsent(t, bobItems, alicePrivate.ID)

	adminItems := listItemsForTest(t, adminClient, server.URL)
	assertItemIDs(t, adminItems, aliceShared.ID, alicePrivate.ID, bobShared.ID, bobPrivate.ID)
	assertItemVisibility(t, adminItems, aliceShared.ID, "shared")
	assertItemVisibility(t, adminItems, alicePrivate.ID, "private")
	assertItemVisibility(t, adminItems, bobShared.ID, "shared")
	assertItemVisibility(t, adminItems, bobPrivate.ID, "private")
}

func TestPrivateAttachmentsAreHiddenFromOtherUsersButVisibleToAdmin(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "admin",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	adminJar, _ := cookiejar.New(nil)
	adminClient := &http.Client{Jar: adminJar}
	loginJSON(t, adminClient, server.URL, "admin", "secret-pass")
	upsertUser(t, adminClient, server.URL, "alice", "alice-pass-123", false)
	upsertUser(t, adminClient, server.URL, "bob", "bob-pass-123", false)

	aliceJar, _ := cookiejar.New(nil)
	aliceClient := &http.Client{Jar: aliceJar}
	loginJSON(t, aliceClient, server.URL, "alice", "alice-pass-123")
	item, _ := createItemWithVisibility(t, aliceClient, server.URL, tempDir, "alice private attachment", "secret.txt", []byte("secret payload"), "private")
	attachmentURL := server.URL + item.Attachments[0].URL

	bobJar, _ := cookiejar.New(nil)
	bobClient := &http.Client{Jar: bobJar}
	loginJSON(t, bobClient, server.URL, "bob", "bob-pass-123")

	resp, err := bobClient.Get(attachmentURL)
	if err != nil {
		t.Fatalf("GET private attachment as bob error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET private attachment as bob status=%d want=404 body=%s", resp.StatusCode, string(b))
	}

	resp, err = adminClient.Get(attachmentURL)
	if err != nil {
		t.Fatalf("GET private attachment as admin error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET private attachment as admin status=%d want=200 body=%s", resp.StatusCode, string(b))
	}
}

func TestNonAdminCannotDeleteOtherUsersItems(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "admin",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	adminJar, _ := cookiejar.New(nil)
	adminClient := &http.Client{Jar: adminJar}
	loginJSON(t, adminClient, server.URL, "admin", "secret-pass")
	upsertUser(t, adminClient, server.URL, "alice", "alice-pass-123", false)
	upsertUser(t, adminClient, server.URL, "bob", "bob-pass-123", false)

	aliceJar, _ := cookiejar.New(nil)
	aliceClient := &http.Client{Jar: aliceJar}
	loginJSON(t, aliceClient, server.URL, "alice", "alice-pass-123")
	aliceItem := createTextOnlyItemWithVisibility(t, aliceClient, server.URL, "alice shared", "shared")

	bobJar, _ := cookiejar.New(nil)
	bobClient := &http.Client{Jar: bobJar}
	loginJSON(t, bobClient, server.URL, "bob", "bob-pass-123")

	req, err := http.NewRequest(http.MethodDelete, server.URL+"/api/items/"+aliceItem.ID, nil)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	req.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err := bobClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE alice item as bob error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE alice item as bob status=%d want=403 body=%s", resp.StatusCode, string(b))
	}

	items := listItemsForTest(t, aliceClient, server.URL)
	assertItemIDs(t, items, aliceItem.ID)
}

func TestSVGDoesNotExposePreviewURL(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "tester",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}
	client := &http.Client{Jar: jar}
	loginJSON(t, client, server.URL, "tester", "secret-pass")

	item, _ := createItemWithContentType(t, client, server.URL, tempDir, "svg upload", "evil.svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	if len(item.Attachments) != 1 {
		t.Fatalf("attachments len = %d, want 1", len(item.Attachments))
	}
	if item.Attachments[0].PreviewURL != "" {
		t.Fatalf("svg preview URL = %q, want empty", item.Attachments[0].PreviewURL)
	}

	resp, err := client.Get(server.URL + "/api/previews/" + item.ID + "/" + item.Attachments[0].StoredName)
	if err != nil {
		t.Fatalf("GET /api/previews svg error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/previews svg status=%d body=%s", resp.StatusCode, string(b))
	}

	resp, err = client.Get(server.URL + item.Attachments[0].URL)
	if err != nil {
		t.Fatalf("GET /api/files svg download error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/files svg status=%d body=%s", resp.StatusCode, string(b))
	}
	if got := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Fatalf("svg download Content-Disposition = %q, want attachment", got)
	}
}

func TestOversizedMultipartBodyReturnsRequestEntityTooLarge(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "tester",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}
	client := &http.Client{Jar: jar}
	loginJSON(t, client, server.URL, "tester", "secret-pass")

	bigPayload := bytes.Repeat([]byte("a"), (maxAttachmentBytes*20)+(1<<20))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("message", "too large"); err != nil {
		t.Fatalf("WriteField: %v", err)
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="attachments"; filename="huge.bin"`)
	header.Set("Content-Type", "application/octet-stream")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write(bigPayload); err != nil {
		t.Fatalf("part.Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/items", &body)
	if err != nil {
		t.Fatalf("new oversized create request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/items oversized error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/items oversized status=%d body=%s", resp.StatusCode, string(b))
	}
}

func TestMalformedMultipartReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "tester",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}
	client := &http.Client{Jar: jar}
	loginJSON(t, client, server.URL, "tester", "secret-pass")

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/items", strings.NewReader("not-a-multipart-body"))
	if err != nil {
		t.Fatalf("new malformed create request: %v", err)
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary=broken-boundary")
	req.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/items malformed error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/items malformed status=%d body=%s", resp.StatusCode, string(b))
	}
}

func TestPasswordResetInvalidatesExistingSession(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "admin",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	adminJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New admin: %v", err)
	}
	adminClient := &http.Client{Jar: adminJar}
	loginJSON(t, adminClient, server.URL, "admin", "secret-pass")
	upsertUser(t, adminClient, server.URL, "alice", "alice-pass-123", false)

	userJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New user: %v", err)
	}
	userClient := &http.Client{Jar: userJar}
	loginJSON(t, userClient, server.URL, "alice", "alice-pass-123")

	resp, err := userClient.Get(server.URL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session before reset error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/session before reset status=%d body=%s", resp.StatusCode, string(b))
	}

	upsertUser(t, adminClient, server.URL, "alice", "alice-new-pass-456", false)

	resp, err = userClient.Get(server.URL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session after reset error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/session after reset status=%d body=%s", resp.StatusCode, string(b))
	}

	freshJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New fresh user: %v", err)
	}
	freshClient := &http.Client{Jar: freshJar}
	loginJSON(t, freshClient, server.URL, "alice", "alice-new-pass-456")
	resp, err = freshClient.Get(server.URL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session with new password error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/session with new password status=%d body=%s", resp.StatusCode, string(b))
	}
}

func TestCurrentUserPasswordChangeInvalidatesExistingSession(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "admin",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New admin: %v", err)
	}
	client := &http.Client{Jar: jar}
	loginJSON(t, client, server.URL, "admin", "secret-pass")

	changeOwnPassword(t, client, server.URL, "secret-pass", "admin-new-pass-456")

	resp, err := client.Get(server.URL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session after self password change error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/session after self password change status=%d body=%s", resp.StatusCode, string(b))
	}

	oldJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New old creds: %v", err)
	}
	oldClient := &http.Client{Jar: oldJar}
	assertLoginStatus(t, oldClient, server.URL, "admin", "secret-pass", http.StatusUnauthorized)

	freshJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New fresh creds: %v", err)
	}
	freshClient := &http.Client{Jar: freshJar}
	loginJSON(t, freshClient, server.URL, "admin", "admin-new-pass-456")
	resp, err = freshClient.Get(server.URL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session with new self password error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/session with new self password status=%d body=%s", resp.StatusCode, string(b))
	}
}

func TestAdminCanDeleteUserAndRevokeExistingSessions(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "admin",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	adminJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New admin: %v", err)
	}
	adminClient := &http.Client{Jar: adminJar}
	loginJSON(t, adminClient, server.URL, "admin", "secret-pass")
	upsertUser(t, adminClient, server.URL, "alice", "alice-pass-123", false)

	aliceJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New alice: %v", err)
	}
	aliceClient := &http.Client{Jar: aliceJar}
	loginJSON(t, aliceClient, server.URL, "alice", "alice-pass-123")

	resp, err := aliceClient.Get(server.URL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session before delete error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/session before delete status=%d body=%s", resp.StatusCode, string(b))
	}

	deleteUser(t, adminClient, server.URL, "alice", http.StatusNoContent)

	resp, err = adminClient.Get(server.URL + "/api/users")
	if err != nil {
		t.Fatalf("GET /api/users after delete error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/users after delete status=%d body=%s", resp.StatusCode, string(b))
	}
	var users []UserRecord
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		t.Fatalf("decode users after delete: %v", err)
	}
	for _, user := range users {
		if user.Username == "alice" {
			t.Fatalf("deleted user still returned in list: %+v", user)
		}
	}

	resp, err = aliceClient.Get(server.URL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session after delete error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/session after delete status=%d body=%s", resp.StatusCode, string(b))
	}

	freshJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New deleted user: %v", err)
	}
	freshClient := &http.Client{Jar: freshJar}
	assertLoginStatus(t, freshClient, server.URL, "alice", "alice-pass-123", http.StatusUnauthorized)
}

func TestAdminCannotDeleteCurrentBootstrapAdmin(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "admin",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New admin: %v", err)
	}
	client := &http.Client{Jar: jar}
	loginJSON(t, client, server.URL, "admin", "secret-pass")

	deleteUser(t, client, server.URL, "admin", http.StatusBadRequest)
}

func TestNonAdminCannotDeleteUsers(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "admin",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	adminJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New admin: %v", err)
	}
	adminClient := &http.Client{Jar: adminJar}
	loginJSON(t, adminClient, server.URL, "admin", "secret-pass")
	upsertUser(t, adminClient, server.URL, "alice", "alice-pass-123", false)
	upsertUser(t, adminClient, server.URL, "bob", "bob-pass-123", false)

	aliceJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New alice: %v", err)
	}
	aliceClient := &http.Client{Jar: aliceJar}
	loginJSON(t, aliceClient, server.URL, "alice", "alice-pass-123")

	deleteUser(t, aliceClient, server.URL, "bob", http.StatusForbidden)
}

func TestLoginTrimsUsernameWhitespace(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "admin",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{Jar: jar}
	loginJSON(t, client, server.URL, " admin ", "secret-pass")

	resp, err := client.Get(server.URL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session after spaced login error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/session after spaced login status=%d body=%s", resp.StatusCode, string(b))
	}
	var session sessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		t.Fatalf("decode spaced-login session: %v", err)
	}
	if session.Username != "admin" {
		t.Fatalf("session username = %q, want admin", session.Username)
	}
}

func TestBootstrapAdminRestartPreservesExistingSessionWhenUnchanged(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "admin",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New admin: %v", err)
	}
	client := &http.Client{Jar: jar}
	loginJSON(t, client, server.URL, "admin", "secret-pass")

	resp, err := client.Get(server.URL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session before restart error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/session before restart status=%d body=%s", resp.StatusCode, string(b))
	}

	server.Close()

	application2, err := New(cfg)
	if err != nil {
		t.Fatalf("New() restart instance error = %v", err)
	}
	server2 := httptest.NewServer(application2.Handler())
	defer server2.Close()

	resp, err = client.Get(server2.URL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session after restart error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/session after restart status=%d body=%s", resp.StatusCode, string(b))
	}
}

func TestBootstrapAdminPromotionInvalidatesExistingSession(t *testing.T) {
	tempDir := t.TempDir()
	baseCfg := Config{
		DataDir:                tempDir,
		BootstrapAdminUsername: "admin",
		BootstrapAdminPassword: "secret-pass",
		SessionSecret:          "test-session-secret-123456",
		PublicBaseURL:          "http://127.0.0.1:9999",
		AllowInsecureHTTP:      true,
	}

	application, err := New(baseCfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	adminJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New admin: %v", err)
	}
	adminClient := &http.Client{Jar: adminJar}
	loginJSON(t, adminClient, server.URL, "admin", "secret-pass")
	upsertUser(t, adminClient, server.URL, "alice", "alice-pass-123", false)

	aliceJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New alice: %v", err)
	}
	aliceClient := &http.Client{Jar: aliceJar}
	loginJSON(t, aliceClient, server.URL, "alice", "alice-pass-123")

	resp, err := aliceClient.Get(server.URL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session before promotion error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/session before promotion status=%d body=%s", resp.StatusCode, string(b))
	}

	server.Close()

	promoteCfg := baseCfg
	promoteCfg.BootstrapAdminUsername = "alice"
	promoteCfg.BootstrapAdminPassword = "alice-bootstrap-pass"
	application2, err := New(promoteCfg)
	if err != nil {
		t.Fatalf("New() promoted instance error = %v", err)
	}
	server2 := httptest.NewServer(application2.Handler())
	defer server2.Close()

	resp, err = aliceClient.Get(server2.URL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session after promotion error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/session after promotion status=%d body=%s", resp.StatusCode, string(b))
	}

	freshJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New promoted alice: %v", err)
	}
	freshClient := &http.Client{Jar: freshJar}
	loginJSON(t, freshClient, server2.URL, "alice", "alice-bootstrap-pass")
	resp, err = freshClient.Get(server2.URL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session with bootstrap password error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/session with bootstrap password status=%d body=%s", resp.StatusCode, string(b))
	}
}

func loginJSON(t *testing.T, client *http.Client, baseURL, username, password string) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	if err != nil {
		t.Fatalf("marshal login payload: %v", err)
	}
	loginReq, err := http.NewRequest(http.MethodPost, baseURL+"/api/login", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new login request: %v", err)
	}
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err := client.Do(loginReq)
	if err != nil {
		t.Fatalf("login request error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status=%d body=%s", resp.StatusCode, string(b))
	}
}

func upsertUser(t *testing.T, client *http.Client, baseURL, username, password string, isAdmin bool) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"username": username,
		"password": password,
		"isAdmin":  isAdmin,
	})
	if err != nil {
		t.Fatalf("marshal user payload: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/users", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new user request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/users error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/users status=%d body=%s", resp.StatusCode, string(b))
	}
}

func changeOwnPassword(t *testing.T, client *http.Client, baseURL, currentPassword, newPassword string) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"currentPassword": currentPassword,
		"newPassword":     newPassword,
	})
	if err != nil {
		t.Fatalf("marshal change-own-password payload: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/users/change-password", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new change-own-password request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/users/change-password error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/users/change-password status=%d body=%s", resp.StatusCode, string(b))
	}
}

func deleteUser(t *testing.T, client *http.Client, baseURL, username string, expectedStatus int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, baseURL+"/api/users/"+url.PathEscape(username), nil)
	if err != nil {
		t.Fatalf("new delete user request: %v", err)
	}
	req.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/users/{username} error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != expectedStatus {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE /api/users/{username} status=%d want=%d body=%s", resp.StatusCode, expectedStatus, string(b))
	}
}

func assertLoginStatus(t *testing.T, client *http.Client, baseURL, username, password string, expectedStatus int) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	if err != nil {
		t.Fatalf("marshal login payload: %v", err)
	}
	loginReq, err := http.NewRequest(http.MethodPost, baseURL+"/api/login", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new login request: %v", err)
	}
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err := client.Do(loginReq)
	if err != nil {
		t.Fatalf("login request error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != expectedStatus {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status=%d want=%d body=%s", resp.StatusCode, expectedStatus, string(b))
	}
}

func createTextOnlyItem(t *testing.T, client *http.Client, baseURL, message string) Item {
	t.Helper()
	item := createItemRequest(t, client, baseURL, message, "", "", nil, "")
	return item
}

func createTextOnlyItemWithVisibility(t *testing.T, client *http.Client, baseURL, message, visibility string) Item {
	t.Helper()
	item := createItemRequest(t, client, baseURL, message, "", "", nil, visibility)
	return item
}

func createItem(t *testing.T, client *http.Client, baseURL, dataDir, message, fileName string, content []byte) (Item, string) {
	t.Helper()
	return createItemWithContentType(t, client, baseURL, dataDir, message, fileName, "application/octet-stream", content)
}

func createItemWithVisibility(t *testing.T, client *http.Client, baseURL, dataDir, message, fileName string, content []byte, visibility string) (Item, string) {
	t.Helper()
	item := createItemRequest(t, client, baseURL, message, fileName, "application/octet-stream", content, visibility)
	filePath := filepath.Join(dataDir, "files", item.ID, item.Attachments[0].StoredName)
	return item, filePath
}

func createItemWithContentType(t *testing.T, client *http.Client, baseURL, dataDir, message, fileName, contentType string, content []byte) (Item, string) {
	t.Helper()
	item := createItemRequest(t, client, baseURL, message, fileName, contentType, content, "")
	filePath := filepath.Join(dataDir, "files", item.ID, item.Attachments[0].StoredName)
	return item, filePath
}

func createItemRequest(t *testing.T, client *http.Client, baseURL, message, fileName, contentType string, content []byte, visibility string) Item {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("message", message); err != nil {
		t.Fatalf("WriteField: %v", err)
	}
	if strings.TrimSpace(visibility) != "" {
		if err := writer.WriteField("visibility", visibility); err != nil {
			t.Fatalf("WriteField visibility: %v", err)
		}
	}
	if fileName != "" {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="attachments"; filename="`+fileName+`"`)
		header.Set("Content-Type", contentType)
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatalf("CreatePart: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("part.Write: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/items", &body)
	if err != nil {
		t.Fatalf("new create request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/items error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/items status = %d, want 201, body=%s", resp.StatusCode, string(b))
	}
	var item Item
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	return item
}

func listItemsForTest(t *testing.T, client *http.Client, baseURL string) []Item {
	t.Helper()
	resp, err := client.Get(baseURL + "/api/items")
	if err != nil {
		t.Fatalf("GET /api/items error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/items status=%d body=%s", resp.StatusCode, string(b))
	}
	var items []Item
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	return items
}

func assertItemIDs(t *testing.T, items []Item, expectedIDs ...string) {
	t.Helper()
	if len(items) != len(expectedIDs) {
		t.Fatalf("items len=%d want=%d items=%+v", len(items), len(expectedIDs), items)
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[item.ID] = true
	}
	for _, id := range expectedIDs {
		if !seen[id] {
			t.Fatalf("expected item id %s in %+v", id, items)
		}
	}
}

func assertItemAbsent(t *testing.T, items []Item, itemID string) {
	t.Helper()
	for _, item := range items {
		if item.ID == itemID {
			t.Fatalf("did not expect item id %s in %+v", itemID, items)
		}
	}
}

func assertItemVisibility(t *testing.T, items []Item, itemID, visibility string) {
	t.Helper()
	for _, item := range items {
		if item.ID == itemID {
			if item.Visibility != visibility {
				t.Fatalf("item %s visibility=%q want=%q", itemID, item.Visibility, visibility)
			}
			return
		}
	}
	t.Fatalf("item %s not found in %+v", itemID, items)
}
