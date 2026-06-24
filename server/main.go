package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type server struct {
	store     *tokenStore
	staticDir string
	clientURL string
	oauthCfg  *oauth2.Config
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	// .env is loaded by the process manager / shell in prod; load it locally too
	// so `go run .` picks up CLIENT_ID etc. the same way dotenv did for Node.
	loadDotEnv()

	port := env("PORT", "3001")
	exeDir := execDir()

	store := newTokenStore(env("SESSIONS_FILE", filepath.Join(exeDir, "sessions.json")))

	srv := &server{
		store:     store,
		staticDir: env("STATIC_DIR", filepath.Join(exeDir, "..", "baremail-app", "dist")),
		clientURL: env("CLIENT_URL", "http://localhost:5173"),
		oauthCfg: &oauth2.Config{
			ClientID:     os.Getenv("CLIENT_ID"),
			ClientSecret: os.Getenv("CLIENT_SECRET"),
			RedirectURL:  env("REDIRECT_URI", "http://localhost:3001/auth/google/callback"),
			// userinfo.profile + .email back the avatar (name, email, photo) shown
			// in the topbar profile menu. Sessions consented before these scopes
			// existed get a 403 from /api/profile and fall back to an initials chip.
			Scopes: []string{
				gmail.GmailReadonlyScope,
				gmail.GmailSendScope,
				"https://www.googleapis.com/auth/userinfo.profile",
				"https://www.googleapis.com/auth/userinfo.email",
			},
			Endpoint: google.Endpoint,
		},
	}

	mux := http.NewServeMux()

	// --- Auth routes ---
	mux.HandleFunc("/auth/google", srv.handleAuthGoogle)
	mux.HandleFunc("/auth/google/callback", srv.handleAuthCallback)
	mux.HandleFunc("/auth/status", srv.handleAuthStatus)
	mux.HandleFunc("/auth/logout", srv.handleLogout)

	// --- Email routes (require auth) ---
	mux.HandleFunc("/api/emails", srv.requireAuth(srv.handleEmails))
	mux.HandleFunc("/api/emails/", srv.requireAuth(srv.handleEmailByID))
	mux.HandleFunc("/api/send", srv.requireAuth(srv.handleSend))
	mux.HandleFunc("/api/profile", srv.requireAuth(srv.handleProfile))

	// --- Static frontend + SPA fallback ---
	mux.HandleFunc("/", srv.handleStatic)

	handler := withCORS(srv.clientURL, mux)

	log.Printf("Server running on http://localhost:%s", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatal(err)
	}
}

// withCORS mirrors the Express cors({ origin: CLIENT_URL, credentials: true }).
func withCORS(origin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, x-session-token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// --- Auth handlers ---

func (s *server) handleAuthGoogle(w http.ResponseWriter, r *http.Request) {
	// access_type=offline yields a refresh_token so sessions outlive the ~1h
	// access token. ApprovalForce (prompt=consent) is essential: Google only
	// returns a refresh_token on the *first* consent for access_type=offline
	// alone, so a re-login would otherwise overwrite the session with a
	// refresh-token-less credential that dies ~1h later. Forcing the consent
	// prompt guarantees every login mints a fresh refresh_token.
	url := s.oauthCfg.AuthCodeURL("", oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing auth code", http.StatusBadRequest)
		return
	}
	tok, err := s.oauthCfg.Exchange(r.Context(), code)
	if err != nil {
		log.Printf("OAuth error: %v", err)
		http.Error(w, "Authentication failed", http.StatusInternalServerError)
		return
	}
	sessionToken := randomToken()
	s.store.set(sessionToken, tok)
	http.Redirect(w, r, s.clientURL+"?token="+sessionToken, http.StatusFound)
}

func (s *server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("x-session-token")
	writeJSON(w, http.StatusOK, map[string]bool{
		"authenticated": token != "" && s.store.has(token),
	})
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("x-session-token")
	if token != "" {
		s.store.delete(token)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Auth middleware ---

type ctxKey string

const (
	gmailClientKey ctxKey = "gmail"
	tokenSourceKey ctxKey = "tokensrc"
)

// requireAuth validates the session token, builds a Gmail client whose token
// source persists rotated access tokens back to the store (the Node version
// did this via oauth2Client.on("tokens", ...)).
func (s *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("x-session-token")
		stored, ok := s.store.get(token)
		if token == "" || !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Not authenticated"})
			return
		}

		// persistingSource wraps the standard refreshing token source and writes
		// the merged credentials back whenever Google mints a fresh access token.
		base := s.oauthCfg.TokenSource(r.Context(), stored)
		ts := &persistingSource{store: s.store, sessionToken: token, prev: stored, src: base}

		// Validate the credential up front. Token() returns the cached access
		// token when still valid (cheap, no network) and only hits Google when a
		// refresh is needed. If that refresh fails — refresh token missing,
		// revoked, or expired — the session is a zombie: present in the store but
		// useless. Evict it and report 401 so the client drops to the sign-in
		// screen instead of silently rendering an empty inbox.
		if _, err := ts.Token(); err != nil {
			log.Printf("Session %s token refresh failed, evicting: %v", token[:min(8, len(token))], err)
			s.store.delete(token)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Not authenticated"})
			return
		}

		svc, err := gmail.NewService(r.Context(), option.WithTokenSource(ts))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to init Gmail"})
			return
		}
		ctx := context.WithValue(r.Context(), gmailClientKey, svc)
		// Expose the same persisting token source so non-Gmail handlers (e.g. the
		// userinfo profile fetch) can authenticate without a Gmail client.
		ctx = context.WithValue(ctx, tokenSourceKey, oauth2.TokenSource(ts))
		next(w, r.WithContext(ctx))
	}
}

func gmailFrom(r *http.Request) *gmail.Service {
	return r.Context().Value(gmailClientKey).(*gmail.Service)
}

func tokenSourceFrom(r *http.Request) oauth2.TokenSource {
	return r.Context().Value(tokenSourceKey).(oauth2.TokenSource)
}

// --- Email handlers ---

func (s *server) handleEmails(w http.ResponseWriter, r *http.Request) {
	svc := gmailFrom(r)
	call := svc.Users.Messages.List("me").MaxResults(20)
	if pt := r.URL.Query().Get("pageToken"); pt != "" {
		call = call.PageToken(pt)
	}
	// Optional Gmail search. Passed straight through as the Gmail `q` query
	// (supports the full operator set: from:, subject:, is:unread, etc.).
	if q := r.URL.Query().Get("q"); q != "" {
		call = call.Q(q)
	}
	list, err := call.Do()
	if err != nil {
		log.Printf("Fetch emails error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch emails"})
		return
	}
	if len(list.Messages) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"emails": []emailSummary{}, "nextPageToken": nil})
		return
	}

	// Fetch each message's metadata concurrently (mirrors Promise.all). Preserve
	// the original list order by indexing into a fixed-size slice.
	emails := make([]emailSummary, len(list.Messages))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i, msg := range list.Messages {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			full, err := svc.Users.Messages.Get("me", id).
				Format("metadata").
				MetadataHeaders("From", "Subject", "Date").
				Do()
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			headers := full.Payload.Headers
			from := headerValue(headers, "From")
			name, sender := splitFrom(from)
			dateHeader := headerValue(headers, "Date")
			emails[i] = emailSummary{
				ID:      id,
				Name:    name,
				Sender:  sender,
				Subject: headerValue(headers, "Subject"),
				Snippet: full.Snippet,
				Date:    relTime(dateHeader),
				Ts:      tsOf(dateHeader),
				Unread:  hasLabel(full.LabelIds, "UNREAD"),
			}
		}(i, msg.Id)
	}
	wg.Wait()

	if firstErr != nil {
		log.Printf("Fetch emails error: %v", firstErr)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch emails"})
		return
	}

	var nextPage any
	if list.NextPageToken != "" {
		nextPage = list.NextPageToken
	}
	writeJSON(w, http.StatusOK, map[string]any{"emails": emails, "nextPageToken": nextPage})
}

var emailIDRe = regexp.MustCompile(`^/api/emails/([^/]+)$`)

func (s *server) handleEmailByID(w http.ResponseWriter, r *http.Request) {
	m := emailIDRe.FindStringSubmatch(r.URL.Path)
	if m == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	id := m[1]

	svc := gmailFrom(r)
	msg, err := svc.Users.Messages.Get("me", id).Format("full").Do()
	if err != nil {
		log.Printf("Fetch email error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch email"})
		return
	}

	headers := msg.Payload.Headers
	from := headerValue(headers, "From")
	name, sender := splitFrom(from)

	var body, html string
	walkBody(msg.Payload, &body, &html)

	writeJSON(w, http.StatusOK, emailFull{
		ID:         msg.Id,
		ThreadID:   msg.ThreadId,
		MessageID:  headerValue(headers, "Message-ID"),
		References: headerValue(headers, "References"),
		Name:       name,
		Sender:     sender,
		Subject:    headerValue(headers, "Subject"),
		To:         headerValue(headers, "To"),
		Body:       body,
		BodyHTML:   html,
		Snippet:    msg.Snippet,
	})
}

// --- Static frontend (single-origin deploy) ---

// handleStatic serves the built React app and falls back to index.html for any
// non-API GET (SPA routing), mirroring express.static + the catch-all route.
func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	// /api and /auth are handled by their own mux entries; anything reaching
	// here that looks like those is a 404 (the Node regex excluded them).
	clean := filepath.Clean(r.URL.Path)
	candidate := filepath.Join(s.staticDir, clean)

	// Guard against path traversal escaping staticDir.
	if !within(s.staticDir, candidate) {
		http.NotFound(w, r)
		return
	}

	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		http.ServeFile(w, r, candidate)
		return
	}
	// SPA fallback.
	http.ServeFile(w, r, filepath.Join(s.staticDir, "index.html"))
}

// --- helpers ---

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func execDir() string {
	exe, err := os.Executable()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	return filepath.Dir(exe)
}

func within(root, target string) bool {
	rootAbs, err1 := filepath.Abs(root)
	tgtAbs, err2 := filepath.Abs(target)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, tgtAbs)
	if err != nil {
		return false
	}
	return rel == "." || (len(rel) > 0 && rel[0] != '.' && !filepath.IsAbs(rel))
}

// persistingSource refreshes via the wrapped source and persists any newly
// minted token (merged over the previous one) back to the store.
type persistingSource struct {
	store        *tokenStore
	sessionToken string
	prev         *oauth2.Token
	src          oauth2.TokenSource
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	tok, err := p.src.Token()
	if err != nil {
		return nil, err
	}
	if p.prev == nil || tok.AccessToken != p.prev.AccessToken {
		// Merge so a refresh that omits the refresh_token keeps the old one,
		// matching `{ ...old, ...fresh }` in the Node handler.
		merged := mergeTokens(p.prev, tok)
		p.store.set(p.sessionToken, merged)
		p.prev = merged
		return merged, nil
	}
	return tok, nil
}

func mergeTokens(old, fresh *oauth2.Token) *oauth2.Token {
	if old == nil {
		return fresh
	}
	out := *fresh
	if out.RefreshToken == "" {
		out.RefreshToken = old.RefreshToken
	}
	return &out
}

// sortsummaries is unused by the handlers (the list order from Gmail is kept)
// but documents that ts is the field a client would sort on; kept tiny so the
// import stays meaningful if a future endpoint needs server-side ordering.
func sortSummaries(in []emailSummary) {
	sort.Slice(in, func(i, j int) bool { return in[i].Ts > in[j].Ts })
}

var _ = sortSummaries
var _ = errors.New
