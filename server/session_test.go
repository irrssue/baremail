package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/oauth2"
)

// TestSingleAccountStaysBareToken proves a lone anonymous account (the legacy
// shape) still serializes as a bare googleapis token — the on-disk
// interop guarantee for sessions written before multi-account existed.
func TestSingleAccountStaysBareToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	s := newTokenStore(path)
	s.set("sess", &oauth2.Token{AccessToken: "a", RefreshToken: "r"})
	s.flush()

	raw, _ := os.ReadFile(path)
	var m map[string]map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("file not bare-token shape: %v (%s)", err, raw)
	}
	if _, ok := m["sess"]["access_token"]; !ok {
		t.Errorf("expected bare token with access_token, got %v", m["sess"])
	}
	if _, ok := m["sess"]["accounts"]; ok {
		t.Errorf("lone anonymous account should not use the accounts shape: %v", m["sess"])
	}
}

// TestMultiAccountRoundTrip checks that a session with two identified accounts
// writes the richer accounts shape and reloads with both identities + tokens.
func TestMultiAccountRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	s := newTokenStore(path)

	s.upsertAccount("sess", &account{
		Email: "a@gmail.com", Name: "A", Picture: "pa",
		tok: wrapToken(&oauth2.Token{AccessToken: "tok-a", RefreshToken: "ref-a"}),
	})
	s.upsertAccount("sess", &account{
		Email: "b@gmail.com", Name: "B",
		tok: wrapToken(&oauth2.Token{AccessToken: "tok-b", RefreshToken: "ref-b"}),
	})
	s.flush()

	s2 := newTokenStore(path)
	sess, ok := s2.getSession("sess")
	if !ok {
		t.Fatal("reloaded store missing sess")
	}
	if len(sess.accounts) != 2 {
		t.Fatalf("want 2 accounts, got %d", len(sess.accounts))
	}
	a := sess.find("a@gmail.com")
	if a == nil || a.Name != "A" || a.tok.AccessToken != "tok-a" {
		t.Errorf("account a reloaded wrong: %+v", a)
	}
	b := sess.find("b@gmail.com")
	if b == nil || b.tok.RefreshToken != "ref-b" {
		t.Errorf("account b reloaded wrong: %+v", b)
	}
}

// TestUpsertAccountDeDupesByEmail re-links the same email and asserts the token
// is replaced in place rather than appended.
func TestUpsertAccountDeDupesByEmail(t *testing.T) {
	s := newTokenStore(filepath.Join(t.TempDir(), "s.json"))
	s.upsertAccount("sess", &account{Email: "a@gmail.com", tok: wrapToken(&oauth2.Token{AccessToken: "old"})})
	s.upsertAccount("sess", &account{Email: "a@gmail.com", tok: wrapToken(&oauth2.Token{AccessToken: "new"})})

	sess, _ := s.getSession("sess")
	if len(sess.accounts) != 1 {
		t.Fatalf("re-login should not duplicate; got %d accounts", len(sess.accounts))
	}
	if sess.accounts[0].tok.AccessToken != "new" {
		t.Errorf("token not refreshed: %q", sess.accounts[0].tok.AccessToken)
	}
}

// TestRemoveAccount unlinks one account and reports the remainder, deleting the
// whole session when the last account goes.
func TestRemoveAccount(t *testing.T) {
	s := newTokenStore(filepath.Join(t.TempDir(), "s.json"))
	s.upsertAccount("sess", &account{Email: "a@gmail.com", tok: wrapToken(&oauth2.Token{AccessToken: "a"})})
	s.upsertAccount("sess", &account{Email: "b@gmail.com", tok: wrapToken(&oauth2.Token{AccessToken: "b"})})

	remaining, existed := s.removeAccount("sess", "a@gmail.com")
	if !existed || remaining != 1 {
		t.Fatalf("remove a: existed=%v remaining=%d", existed, remaining)
	}
	remaining, existed = s.removeAccount("sess", "b@gmail.com")
	if !existed || remaining != 0 {
		t.Fatalf("remove b: existed=%v remaining=%d", existed, remaining)
	}
	if s.has("sess") {
		t.Error("session should be gone after its last account is removed")
	}
	if _, existed := s.removeAccount("sess", "missing@gmail.com"); existed {
		t.Error("removing from a gone session should report not-existed")
	}
}

// TestSetAccountIdentityBackfill upgrades a legacy anonymous account in place.
func TestSetAccountIdentityBackfill(t *testing.T) {
	s := newTokenStore(filepath.Join(t.TempDir(), "s.json"))
	s.set("sess", &oauth2.Token{AccessToken: "a", RefreshToken: "r"}) // anonymous
	s.setAccountIdentity("sess", "", "me@gmail.com", "Me", "pic")

	sess, _ := s.getSession("sess")
	if len(sess.accounts) != 1 {
		t.Fatalf("want 1 account, got %d", len(sess.accounts))
	}
	a := sess.accounts[0]
	if a.Email != "me@gmail.com" || a.Name != "Me" || a.tok.AccessToken != "a" {
		t.Errorf("identity not backfilled in place: %+v", a)
	}
}

// TestGetSessionReturnsClone confirms callers can't mutate stored state through
// the returned snapshot.
func TestGetSessionReturnsClone(t *testing.T) {
	s := newTokenStore(filepath.Join(t.TempDir(), "s.json"))
	s.upsertAccount("sess", &account{Email: "a@gmail.com", tok: wrapToken(&oauth2.Token{AccessToken: "a"})})

	snap, _ := s.getSession("sess")
	snap.accounts[0].Email = "mutated"

	again, _ := s.getSession("sess")
	if again.accounts[0].Email != "a@gmail.com" {
		t.Errorf("store mutated through snapshot: %q", again.accounts[0].Email)
	}
}

// TestCursorRoundTrip covers the composite pagination token encode/decode.
func TestCursorRoundTrip(t *testing.T) {
	in := map[string]string{"a@gmail.com": "ptA", "b@gmail.com": "ptB"}
	out := decodeCursor(encodeCursor(in))
	if len(out) != 2 || out["a@gmail.com"] != "ptA" || out["b@gmail.com"] != "ptB" {
		t.Errorf("cursor round-trip lost data: %v", out)
	}
	if decodeCursor("") != nil {
		t.Error("empty cursor should decode to nil (first page)")
	}
	if decodeCursor("!!! not base64 !!!") != nil {
		t.Error("garbage cursor should decode to nil, not error")
	}
}
