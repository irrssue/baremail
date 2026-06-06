package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestTokenStorePersistAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	s := newTokenStore(path)
	tok := &oauth2.Token{AccessToken: "abc", RefreshToken: "ref"}
	s.set("sess1", tok)
	s.flush() // force synchronous write (skip the 200ms debounce)

	// File must exist and be owner-only (0600).
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("sessions file not written: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("sessions file mode = %o; want 600", mode)
	}

	// A fresh store over the same path must see the session.
	s2 := newTokenStore(path)
	got, ok := s2.get("sess1")
	if !ok {
		t.Fatal("reloaded store missing sess1")
	}
	if got.AccessToken != "abc" || got.RefreshToken != "ref" {
		t.Errorf("reloaded token = %+v", got)
	}
}

func TestTokenStoreDelete(t *testing.T) {
	s := newTokenStore(filepath.Join(t.TempDir(), "s.json"))
	s.set("x", &oauth2.Token{AccessToken: "a"})
	if !s.has("x") {
		t.Fatal("should have x")
	}
	if !s.delete("x") {
		t.Error("delete should report true")
	}
	if s.has("x") {
		t.Error("x should be gone")
	}
	if s.delete("missing") {
		t.Error("deleting missing should report false")
	}
}

func TestMergeTokensKeepsRefresh(t *testing.T) {
	old := &oauth2.Token{AccessToken: "old", RefreshToken: "keepme"}
	fresh := &oauth2.Token{AccessToken: "new"} // refresh omitted on rotation
	merged := mergeTokens(old, fresh)
	if merged.AccessToken != "new" {
		t.Errorf("access = %q; want new", merged.AccessToken)
	}
	if merged.RefreshToken != "keepme" {
		t.Errorf("refresh = %q; want keepme", merged.RefreshToken)
	}
}

func TestMergeTokensNilOld(t *testing.T) {
	fresh := &oauth2.Token{AccessToken: "new", RefreshToken: "r"}
	if merged := mergeTokens(nil, fresh); merged != fresh {
		t.Error("nil old should return fresh unchanged")
	}
}

func newTestServer(t *testing.T) *server {
	t.Helper()
	return &server{
		store:     newTokenStore(filepath.Join(t.TempDir(), "s.json")),
		staticDir: t.TempDir(),
		clientURL: "http://localhost:5173",
		oauthCfg:  &oauth2.Config{},
	}
}

func TestAuthStatus(t *testing.T) {
	s := newTestServer(t)
	s.store.set("good", &oauth2.Token{AccessToken: "a", Expiry: time.Now().Add(time.Hour)})

	// No token → false.
	r := httptest.NewRequest(http.MethodGet, "/auth/status", nil)
	w := httptest.NewRecorder()
	s.handleAuthStatus(w, r)
	assertAuth(t, w, false)

	// Valid token → true.
	r = httptest.NewRequest(http.MethodGet, "/auth/status", nil)
	r.Header.Set("x-session-token", "good")
	w = httptest.NewRecorder()
	s.handleAuthStatus(w, r)
	assertAuth(t, w, true)
}

func assertAuth(t *testing.T, w *httptest.ResponseRecorder, want bool) {
	t.Helper()
	var body struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad json: %v (%s)", err, w.Body.String())
	}
	if body.Authenticated != want {
		t.Errorf("authenticated = %v; want %v", body.Authenticated, want)
	}
}

func TestLogout(t *testing.T) {
	s := newTestServer(t)
	s.store.set("tok", &oauth2.Token{AccessToken: "a"})
	r := httptest.NewRequest(http.MethodGet, "/auth/logout", nil)
	r.Header.Set("x-session-token", "tok")
	w := httptest.NewRecorder()
	s.handleLogout(w, r)

	var body struct {
		Ok bool `json:"ok"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if !body.Ok {
		t.Error("logout should return ok:true")
	}
	if s.store.has("tok") {
		t.Error("token should be deleted after logout")
	}
}

func TestRequireAuthRejectsMissingToken(t *testing.T) {
	s := newTestServer(t)
	called := false
	h := s.requireAuth(func(w http.ResponseWriter, r *http.Request) { called = true })

	r := httptest.NewRequest(http.MethodGet, "/api/emails", nil)
	w := httptest.NewRecorder()
	h(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", w.Code)
	}
	if called {
		t.Error("handler should not run without auth")
	}
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error != "Not authenticated" {
		t.Errorf("error = %q; want 'Not authenticated'", body.Error)
	}
}

func TestCORSHeaders(t *testing.T) {
	h := withCORS("http://localhost:5173", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Preflight OPTIONS short-circuits with 204 and the allow-origin header.
	r := httptest.NewRequest(http.MethodOptions, "/api/emails", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d; want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("allow-origin = %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("allow-credentials = %q; want true", got)
	}
}

func TestStaticSPAFallback(t *testing.T) {
	s := newTestServer(t)
	// Write an index.html into the static dir.
	idx := filepath.Join(s.staticDir, "index.html")
	if err := os.WriteFile(idx, []byte("<html>SPA</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Also a real asset.
	if err := os.WriteFile(filepath.Join(s.staticDir, "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Unknown route → index.html (SPA fallback).
	r := httptest.NewRequest(http.MethodGet, "/inbox/123", nil)
	w := httptest.NewRecorder()
	s.handleStatic(w, r)
	if w.Body.String() != "<html>SPA</html>" {
		t.Errorf("SPA fallback body = %q", w.Body.String())
	}

	// Real asset → served directly.
	r = httptest.NewRequest(http.MethodGet, "/app.js", nil)
	w = httptest.NewRecorder()
	s.handleStatic(w, r)
	if w.Body.String() != "console.log(1)" {
		t.Errorf("asset body = %q", w.Body.String())
	}
}

func TestStaticBlocksTraversal(t *testing.T) {
	s := newTestServer(t)
	idx := filepath.Join(s.staticDir, "index.html")
	_ = os.WriteFile(idx, []byte("SPA"), 0o644)

	// Attempt to escape the static dir. http.ServeFile rejects a raw ".." path
	// outright; even if it didn't, filepath.Clean + the within() guard keep the
	// resolution inside root. Either way the response must never be the contents
	// of a file outside staticDir — only the SPA index or a 4xx.
	r := httptest.NewRequest(http.MethodGet, "/../../etc/passwd", nil)
	w := httptest.NewRecorder()
	s.handleStatic(w, r)
	body := w.Body.String()
	if w.Code == http.StatusOK && body != "SPA" {
		t.Errorf("traversal leaked content (status %d): %q", w.Code, body)
	}
}

func TestRandomTokenUniqueAndHex(t *testing.T) {
	a, b := randomToken(), randomToken()
	if a == b {
		t.Error("tokens should differ")
	}
	if len(a) != 64 { // 32 bytes hex-encoded
		t.Errorf("token len = %d; want 64", len(a))
	}
}
