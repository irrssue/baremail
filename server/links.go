package main

import (
	"sync"
	"time"
)

// linkStore maps a random OAuth `state` value to the session token an
// add-account flow should attach the new Google account to. Keeping the mapping
// server-side means the real session token never travels to Google (only the
// opaque random state does). Entries are short-lived — an OAuth round-trip is
// seconds — and live only in memory; a lost entry across a restart degrades to a
// fresh login, never a leak. A fresh sign-in stores an empty session.
type linkStore struct {
	mu sync.Mutex
	m  map[string]linkEntry
}

type linkEntry struct {
	session string
	exp     time.Time
}

const linkTTL = 10 * time.Minute

func newLinkStore() *linkStore {
	return &linkStore{m: map[string]linkEntry{}}
}

// put records that the given OAuth state should link into session (which may be
// "" for a plain sign-in), sweeping expired entries first.
func (l *linkStore) put(state, session string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep()
	l.m[state] = linkEntry{session: session, exp: time.Now().Add(linkTTL)}
}

// take consumes the entry for state and returns its session target. ok is false
// for an unknown or expired state (handled as a fresh login by the caller).
func (l *linkStore) take(state string) (session string, ok bool) {
	if state == "" {
		return "", false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e, found := l.m[state]
	if found {
		delete(l.m, state)
	}
	if !found || time.Now().After(e.exp) {
		return "", false
	}
	return e.session, true
}

func (l *linkStore) sweep() {
	now := time.Now()
	for k, e := range l.m {
		if now.After(e.exp) {
			delete(l.m, k)
		}
	}
}
