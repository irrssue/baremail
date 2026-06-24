package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"golang.org/x/oauth2"
)

// userInfoURL is Google's OpenID Connect userinfo endpoint. With the
// userinfo.profile + userinfo.email scopes it returns the signed-in account's
// display name, email, and avatar URL — exactly what the topbar profile chip
// and account switcher render. We hit it directly (rather than pulling in
// another generated API client) because the response is three fields.
const userInfoURL = "https://www.googleapis.com/oauth2/v3/userinfo"

// googleUserInfo is the subset of the userinfo response we surface. Field names
// follow the OIDC standard claims Google returns.
type googleUserInfo struct {
	Sub     string `json:"sub"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Picture string `json:"picture"`
}

// profileResponse is the JSON the legacy /api/profile endpoint returns.
type profileResponse struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Picture string `json:"picture"`
}

// accountInfo is one entry in /api/accounts: the identity of a linked Google
// account for the switcher and per-row inbox tags (no token material).
type accountInfo struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

// fetchUserInfo calls the OIDC userinfo endpoint with the given token source.
// It returns the parsed identity and the HTTP status (so callers can map a
// 401/403 — a session consented before the userinfo scopes — to their own 403).
func fetchUserInfo(ctx context.Context, ts oauth2.TokenSource) (*googleUserInfo, int, error) {
	client := oauth2.NewClient(ctx, ts)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoURL, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("userinfo status %d", resp.StatusCode)
	}
	var info googleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, resp.StatusCode, err
	}
	return &info, http.StatusOK, nil
}

// handleProfile returns the primary (or ?account-selected) Google account's
// name, email, and photo, freshly from userinfo. Sessions consented before the
// userinfo scopes existed get a 403, which the client treats as "fall back to an
// initials chip" — the same contract /api/send uses for the send scope.
func (s *server) handleProfile(w http.ResponseWriter, r *http.Request) {
	sc := sessionFrom(r)
	acct := sc.sess.find(r.URL.Query().Get("account"))
	if acct == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Not authenticated"})
		return
	}
	ts, err := sc.tokenSource(r.Context(), acct)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Not authenticated"})
		return
	}

	info, status, err := fetchUserInfo(r.Context(), ts)
	if err != nil {
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			log.Printf("Profile rejected (scope?): %d", status)
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "Profile permission not granted. Log out and sign in again to load your account.",
			})
			return
		}
		log.Printf("Profile fetch error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch profile"})
		return
	}

	writeJSON(w, http.StatusOK, profileResponse{
		Name:    info.Name,
		Email:   info.Email,
		Picture: info.Picture,
	})
}

// handleAccounts lists every Google account linked to the session (identity
// only) for the topbar switcher and Settings. Identity is recorded at link time,
// so this is normally a no-network read; an account missing its identity (a
// session written before identity was stored) is backfilled once via userinfo.
func (s *server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	sc := sessionFrom(r)
	out := make([]accountInfo, 0, len(sc.sess.accounts))
	for _, a := range sc.sess.accounts {
		info := accountInfo{Email: a.Email, Name: a.Name, Picture: a.Picture}
		if info.Email == "" {
			if ts, err := sc.tokenSource(r.Context(), a); err == nil {
				if ui, _, err := fetchUserInfo(r.Context(), ts); err == nil && ui.Email != "" {
					info = accountInfo{Email: ui.Email, Name: ui.Name, Picture: ui.Picture}
					s.store.setAccountIdentity(sc.token, "", ui.Email, ui.Name, ui.Picture)
				}
			}
		}
		out = append(out, info)
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

// handleUnlink removes one account from the session. Removing the last account
// deletes the session entirely (remaining: 0), which the client treats as a
// full sign-out.
func (s *server) handleUnlink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	sc := sessionFrom(r)
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Account email is required"})
		return
	}
	remaining, existed := s.store.removeAccount(sc.token, body.Email)
	if !existed {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Account not linked"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "remaining": remaining})
}
