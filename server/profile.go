package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"

	"golang.org/x/oauth2"
)

// userInfoURL is Google's OpenID Connect userinfo endpoint. With the
// userinfo.profile + userinfo.email scopes it returns the signed-in account's
// display name, email, and avatar URL — exactly what the topbar profile chip
// renders. We hit it directly (rather than pulling in another generated API
// client) because the response is three fields.
const userInfoURL = "https://www.googleapis.com/oauth2/v3/userinfo"

// googleUserInfo is the subset of the userinfo response we surface. Field names
// follow the OIDC standard claims Google returns.
type googleUserInfo struct {
	Sub     string `json:"sub"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Picture string `json:"picture"`
}

// profileResponse is the JSON the frontend consumes for the avatar + menu.
type profileResponse struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Picture string `json:"picture"`
}

// handleProfile returns the signed-in Google account's name, email, and photo.
// It authenticates with the session's persisting token source (so a refreshed
// access token is saved like every other API call). Sessions consented before
// the userinfo scopes existed lack permission; Google answers 401/403 and we
// pass a 403 to the client, which falls back to an initials chip and prompts a
// re-sign-in — the same contract /api/send uses for the send scope.
func (s *server) handleProfile(w http.ResponseWriter, r *http.Request) {
	ts := tokenSourceFrom(r)
	client := oauth2.NewClient(r.Context(), ts)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, userInfoURL, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to build profile request"})
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Profile fetch error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch profile"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		log.Printf("Profile rejected (scope?): %d", resp.StatusCode)
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "Profile permission not granted. Log out and sign in again to load your account.",
		})
		return
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		log.Printf("Profile fetch bad status %d: %s", resp.StatusCode, body)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch profile"})
		return
	}

	var info googleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		log.Printf("Profile decode error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read profile"})
		return
	}

	writeJSON(w, http.StatusOK, profileResponse{
		Name:    info.Name,
		Email:   info.Email,
		Picture: info.Picture,
	})
}
