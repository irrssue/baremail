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

// tokenStore maps sessionToken -> OAuth tokens. Persisted to disk so sessions
// survive a server restart/deploy (a pm2 restart would otherwise wipe everyone).
// SESSIONS_FILE defaults next to the binary; keep it out of git (gitignored).
type tokenStore struct {
	mu        sync.Mutex
	path      string
	data      map[string]*googleToken
	saveTimer *time.Timer
}

func newTokenStore(path string) *tokenStore {
	s := &tokenStore{path: path, data: map[string]*googleToken{}}
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
	m := map[string]*googleToken{}
	if err := json.Unmarshal(raw, &m); err != nil {
		log.Printf("Failed to parse sessions: %v", err)
		return
	}
	s.data = m
}

func (s *tokenStore) get(token string) (*oauth2.Token, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.data[token]
	if !ok {
		return nil, false
	}
	return t.Token, true
}

func (s *tokenStore) has(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.data[token]
	return ok
}

func (s *tokenStore) set(token string, t *oauth2.Token) {
	s.mu.Lock()
	// Preserve any extra on-disk fields (e.g. scope) the previous entry carried.
	if prev, ok := s.data[token]; ok && prev != nil {
		s.data[token] = &googleToken{Token: t, extra: prev.extra}
	} else {
		s.data[token] = wrapToken(t)
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
	snapshot := make(map[string]*googleToken, len(s.data))
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
