package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestGoogleTokenLoadsNodeShape feeds the EXACT on-disk shape the old Node
// googleapis store wrote (access_token + expiry_date millis + scope + token_type,
// no refresh_token on rotated sessions) and asserts it loads with the right
// expiry — the homelab-session-survival guarantee for the migration.
func TestGoogleTokenLoadsNodeShape(t *testing.T) {
	raw := `{
		"access_token": "ya29.abc",
		"scope": "https://www.googleapis.com/auth/gmail.readonly",
		"token_type": "Bearer",
		"expiry_date": 1749037320000
	}`
	var gt googleToken
	if err := json.Unmarshal([]byte(raw), &gt); err != nil {
		t.Fatalf("unmarshal node-shape token: %v", err)
	}
	if gt.AccessToken != "ya29.abc" {
		t.Errorf("access_token = %q", gt.AccessToken)
	}
	if gt.TokenType != "Bearer" {
		t.Errorf("token_type = %q", gt.TokenType)
	}
	wantExpiry := time.UnixMilli(1749037320000)
	if !gt.Expiry.Equal(wantExpiry) {
		t.Errorf("expiry = %v; want %v (from expiry_date millis)", gt.Expiry, wantExpiry)
	}
}

// TestGoogleTokenRoundTripPreservesScope ensures a load→save cycle keeps the
// `scope` field (and any extra) and re-emits expiry as millis, so the file stays
// readable by both the Go server and the old library.
func TestGoogleTokenRoundTripPreservesScope(t *testing.T) {
	raw := `{"access_token":"a","refresh_token":"r","token_type":"Bearer","scope":"gmail.readonly","expiry_date":1749037320000}`
	var gt googleToken
	if err := json.Unmarshal([]byte(raw), &gt); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(&gt)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["scope"] != "gmail.readonly" {
		t.Errorf("scope lost on round-trip: %v", m)
	}
	if m["refresh_token"] != "r" {
		t.Errorf("refresh_token lost: %v", m)
	}
	// expiry must serialize back as the millis number, not an RFC3339 string.
	if ed, ok := m["expiry_date"].(float64); !ok || int64(ed) != 1749037320000 {
		t.Errorf("expiry_date = %v (%T); want 1749037320000", m["expiry_date"], m["expiry_date"])
	}
}

// TestStoreReadsNodeSessionsFile writes a multi-session file in the Node shape
// to disk, then loads it through the real tokenStore — end-to-end proof the
// homelab sessions.json will be honored after the swap.
func TestStoreReadsNodeSessionsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	nodeFile := `{
		"sessAAA": {"access_token":"tok-a","token_type":"Bearer","scope":"s","expiry_date":1749037320000},
		"sessBBB": {"access_token":"tok-b","refresh_token":"ref-b","token_type":"Bearer","expiry_date":1749037320000}
	}`
	if err := os.WriteFile(path, []byte(nodeFile), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newTokenStore(path)
	if !s.has("sessAAA") || !s.has("sessBBB") {
		t.Fatal("store did not load both node sessions")
	}
	a, _ := s.get("sessAAA")
	if a.AccessToken != "tok-a" {
		t.Errorf("sessAAA access = %q", a.AccessToken)
	}
	b, _ := s.get("sessBBB")
	if b.RefreshToken != "ref-b" {
		t.Errorf("sessBBB refresh = %q", b.RefreshToken)
	}

	// Mutate one session and flush; the file must still be valid Node-shape JSON.
	s.set("sessAAA", a)
	s.flush()
	reread, _ := os.ReadFile(path)
	var check map[string]map[string]any
	if err := json.Unmarshal(reread, &check); err != nil {
		t.Fatalf("flushed file not valid json: %v", err)
	}
	if _, ok := check["sessAAA"]["expiry_date"]; !ok {
		t.Errorf("flushed sessAAA lost expiry_date: %v", check["sessAAA"])
	}
}
