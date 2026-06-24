package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// tokenStore maps sessionToken -> session (one or more linked Google accounts).
// Persisted to disk so sessions survive a server restart/deploy (a container
// restart would otherwise wipe everyone). SESSIONS_FILE defaults next to the
// binary; keep it out of git (gitignored).
type tokenStore struct {
	mu        sync.Mutex
	path      string
	data      map[string]*session
	saveTimer *time.Timer
}

func newTokenStore(path string) *tokenStore {
	s := &tokenStore{path: path, data: map[string]*session{}}
	s.secureSessionsPath()
	s.load()
	return s
}

// secureSessionsPath keeps the sessions file and its parent dir owner-only
// (0600 / 0700) so other accounts on the host can't read the live OAuth tokens;
// it also tightens an existing file's mode on every boot.
func (s *tokenStore) secureSessionsPath() {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err == nil {
		_ = os.Chmod(dir, 0o700)
	}
	_ = os.Chmod(s.path, 0o600)
}

func (s *tokenStore) load() {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return // no file yet — start empty
	}
	m := map[string]*session{}
	if err := json.Unmarshal(raw, &m); err != nil {
		log.Printf("Failed to parse sessions: %v", err)
		return
	}
	s.data = m
}

// --- Session-level access (multi-account aware) ---

// getSession returns a deep copy of the session so the caller can iterate its
// accounts without holding the lock (token refreshes write back independently).
func (s *tokenStore) getSession(token string) (*session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.data[token]
	if !ok || len(sess.accounts) == 0 {
		return nil, false
	}
	return sess.clone(), true
}

// upsertAccount adds acct to the session (creating the session if needed),
// replacing any existing account with the same non-empty email — so a re-login
// to an already-linked account refreshes its token rather than duplicating it.
func (s *tokenStore) upsertAccount(token string, acct *account) {
	s.mu.Lock()
	sess, ok := s.data[token]
	if !ok {
		sess = &session{}
		s.data[token] = sess
	}
	replaced := false
	if acct.Email != "" {
		for i, a := range sess.accounts {
			if a.Email == acct.Email {
				sess.accounts[i] = acct
				replaced = true
				break
			}
		}
	}
	if !replaced {
		sess.accounts = append(sess.accounts, acct)
	}
	s.mu.Unlock()
	s.save()
}

// removeAccount unlinks the account with the given email and returns how many
// accounts remain. When the last one is removed the whole session is deleted
// (equivalent to signing out).
func (s *tokenStore) removeAccount(token, email string) (remaining int, existed bool) {
	s.mu.Lock()
	sess, ok := s.data[token]
	if !ok {
		s.mu.Unlock()
		return 0, false
	}
	kept := make([]*account, 0, len(sess.accounts))
	for _, a := range sess.accounts {
		if email != "" && a.Email == email {
			existed = true
			continue
		}
		kept = append(kept, a)
	}
	sess.accounts = kept
	remaining = len(kept)
	if remaining == 0 {
		delete(s.data, token)
	}
	s.mu.Unlock()
	s.save()
	return remaining, existed
}

// setAccountToken persists a refreshed token for one account, preserving any
// extra on-disk fields (e.g. scope) the account carried. An empty email targets
// the primary account (the legacy single-account case).
func (s *tokenStore) setAccountToken(token, email string, t *oauth2.Token) {
	s.mu.Lock()
	if sess, ok := s.data[token]; ok {
		if target := sess.find(email); target != nil {
			extra := map[string]json.RawMessage{}
			if target.tok != nil {
				extra = target.tok.extra
			}
			target.tok = &googleToken{Token: t, extra: extra}
		}
	}
	s.mu.Unlock()
	s.save()
}

// setAccountIdentity backfills email/name/photo onto an account that lacked them
// (old sessions written before identity was recorded). matchEmail selects the
// account; an empty matchEmail targets the first account still missing an email.
func (s *tokenStore) setAccountIdentity(token, matchEmail, email, name, picture string) {
	s.mu.Lock()
	if sess, ok := s.data[token]; ok {
		var target *account
		if matchEmail != "" {
			target = sess.find(matchEmail)
		} else {
			for _, a := range sess.accounts {
				if a.Email == "" {
					target = a
					break
				}
			}
		}
		if target != nil {
			target.Email, target.Name, target.Picture = email, name, picture
		}
	}
	s.mu.Unlock()
	s.save()
}

// --- Legacy single-account API (used by tests and the simple status path) ---

// get returns the primary account's token. Retained for the auth-status check
// and the test suite, which predate multi-account sessions.
func (s *tokenStore) get(token string) (*oauth2.Token, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.data[token]
	if !ok || len(sess.accounts) == 0 {
		return nil, false
	}
	return sess.accounts[0].tok.Token, true
}

func (s *tokenStore) has(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.data[token]
	return ok && len(sess.accounts) > 0
}

// set replaces the primary account's token, or creates a single-account session
// when none exists. Preserves the primary's extra on-disk fields (e.g. scope).
func (s *tokenStore) set(token string, t *oauth2.Token) {
	s.mu.Lock()
	if sess, ok := s.data[token]; ok && len(sess.accounts) > 0 {
		a := sess.accounts[0]
		extra := map[string]json.RawMessage{}
		if a.tok != nil {
			extra = a.tok.extra
		}
		a.tok = &googleToken{Token: t, extra: extra}
	} else {
		s.data[token] = &session{accounts: []*account{{tok: wrapToken(t)}}}
	}
	s.mu.Unlock()
	s.save()
}

func (s *tokenStore) delete(token string) bool {
	s.mu.Lock()
	_, ok := s.data[token]
	if ok {
		delete(s.data, token)
	}
	s.mu.Unlock()
	if ok {
		s.save()
	}
	return ok
}

// save debounces writes (200ms) so rapid mutations coalesce into one flush.
func (s *tokenStore) save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveTimer != nil {
		s.saveTimer.Stop()
	}
	s.saveTimer = time.AfterFunc(200*time.Millisecond, s.flush)
}

// flush writes to a temp file (owner-only mode) then atomically renames it into
// place, so the tokens file is never momentarily world-readable and a crash
// mid-write can't truncate the real file.
func (s *tokenStore) flush() {
	s.mu.Lock()
	snapshot := make(map[string]*session, len(s.data))
	for k, v := range s.data {
		snapshot[k] = v
	}
	s.mu.Unlock()

	buf, err := json.Marshal(snapshot)
	if err != nil {
		log.Printf("Failed to persist sessions: %v", err)
		return
	}
	tmp := s.path + "." + itoa(os.Getpid()) + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		log.Printf("Failed to persist sessions: %v", err)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		log.Printf("Failed to persist sessions: %v", err)
	}
}
