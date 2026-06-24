package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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
	// links resolves an "add account" OAuth round-trip back to the session the
	// new Google account should attach to. In-memory, short-lived.
	links *linkStore
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
		links:     newLinkStore(),
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
	mux.HandleFunc("/api/accounts", srv.requireAuth(srv.handleAccounts))
	mux.HandleFunc("/api/accounts/remove", srv.requireAuth(srv.handleUnlink))

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
	// "Add account" arrives as ?link=<current session token>. We mint a random
	// OAuth state that maps to it server-side, so the real session token never
	// travels to Google — only the opaque state does. A plain sign-in has no link
	// param and maps state to "".
	linkSession := r.URL.Query().Get("link")
	if linkSession != "" && !s.store.has(linkSession) {
		linkSession = "" // unknown session → treat as a fresh login
	}
	state := randomToken()
	s.links.put(state, linkSession)

	// access_type=offline yields a refresh_token so sessions outlive the ~1h
	// access token. ApprovalForce (prompt=consent) is essential: Google only
	// returns a refresh_token on the *first* consent for access_type=offline
	// alone, so a re-login would otherwise overwrite the session with a
	// refresh-token-less credential that dies ~1h later. Forcing the consent
	// prompt guarantees every login mints a fresh refresh_token.
	url := s.oauthCfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
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

	// Identify the account so re-logins de-duplicate by email and the merged
	// inbox can label each row + route replies. Best-effort: a userinfo failure
	// (e.g. a consent without the userinfo scopes) just yields an unlabeled
	// account, still usable as a single inbox.
	acct := &account{tok: wrapToken(tok)}
	if info, _, err := fetchUserInfo(r.Context(), s.oauthCfg.TokenSource(r.Context(), tok)); err == nil {
		acct.Email, acct.Name, acct.Picture = info.Email, info.Name, info.Picture
	} else {
		log.Printf("userinfo on callback failed: %v", err)
	}

	// An add-account flow attaches to the existing session and keeps its token;
	// a fresh sign-in mints a new session token.
	if linkSession, ok := s.links.take(r.URL.Query().Get("state")); ok && linkSession != "" && s.store.has(linkSession) {
		s.store.upsertAccount(linkSession, acct)
		http.Redirect(w, r, s.clientURL+"?linked=1", http.StatusFound)
		return
	}
	sessionToken := randomToken()
	s.store.upsertAccount(sessionToken, acct)
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

const sessionKey ctxKey = "session"

// errNoAccount is returned when a request names an account email that isn't
// linked to the session (vs. a credential that's present but dead).
var errNoAccount = errors.New("account not linked")

// sessionCtx is the per-request handle the auth middleware injects: the session
// token plus a snapshot of its linked accounts. Handlers build a Gmail client
// (or token source) for a specific account through it.
type sessionCtx struct {
	srv   *server
	token string
	sess  *session
}

// requireAuth validates the session token and injects a sessionCtx. Per-account
// credential validation happens lazily when a handler builds a client, so one
// dead account doesn't sink a request that can still serve the others.
func (s *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("x-session-token")
		sess, ok := s.store.getSession(token)
		if token == "" || !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Not authenticated"})
			return
		}
		sc := &sessionCtx{srv: s, token: token, sess: sess}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionKey, sc)))
	}
}

func sessionFrom(r *http.Request) *sessionCtx {
	return r.Context().Value(sessionKey).(*sessionCtx)
}

// tokenSource builds a refreshing, store-persisting token source for one
// account. A refresh failure means the credential is dead (refresh token
// missing, revoked, or expired): the account is evicted from the store — the
// whole session when it was the last one — and the error is returned so callers
// surface a 401, mirroring the old single-account zombie eviction.
func (sc *sessionCtx) tokenSource(ctx context.Context, acct *account) (oauth2.TokenSource, error) {
	base := sc.srv.oauthCfg.TokenSource(ctx, acct.tok.Token)
	ts := &persistingSource{store: sc.srv.store, sessionToken: sc.token, email: acct.Email, prev: acct.tok.Token, src: base}
	if _, err := ts.Token(); err != nil {
		log.Printf("Session %s account token refresh failed, evicting: %v", sc.token[:min(8, len(sc.token))], err)
		if len(sc.sess.accounts) <= 1 {
			sc.srv.store.delete(sc.token)
		} else {
			sc.srv.store.removeAccount(sc.token, acct.Email)
		}
		return nil, err
	}
	return ts, nil
}

// clientFor builds a Gmail client for the account with the given email (empty →
// the primary account). errNoAccount means the email isn't linked.
func (sc *sessionCtx) clientFor(ctx context.Context, email string) (*gmail.Service, *account, error) {
	acct := sc.sess.find(email)
	if acct == nil {
		return nil, nil, errNoAccount
	}
	ts, err := sc.tokenSource(ctx, acct)
	if err != nil {
		return nil, acct, err
	}
	svc, err := gmail.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, acct, err
	}
	return svc, acct, nil
}

// --- Email handlers ---

func (s *server) handleEmails(w http.ResponseWriter, r *http.Request) {
	sc := sessionFrom(r)
	q := r.URL.Query().Get("q")

	// The inbox merges every linked account. Pagination uses a composite cursor:
	// a base64 map of account-email -> that account's Gmail pageToken. On the
	// first page (no cursor) every account is fetched from the top; on later
	// pages only the accounts that still had a pageToken are advanced.
	cursor := decodeCursor(r.URL.Query().Get("pageToken"))
	type plan struct {
		acct      *account
		pageToken string
	}
	var plans []plan
	for _, a := range sc.sess.accounts {
		if cursor == nil {
			plans = append(plans, plan{a, ""})
		} else if pt, ok := cursor[a.Email]; ok && pt != "" {
			plans = append(plans, plan{a, pt})
		}
	}

	var (
		mu         sync.Mutex
		all        []emailSummary
		nextCursor = map[string]string{}
		authFails  int
		fetchErr   error
		labelRows  = len(sc.sess.accounts) > 1
		wg         sync.WaitGroup
	)
	for _, p := range plans {
		wg.Add(1)
		go func(p plan) {
			defer wg.Done()
			svc, acct, err := sc.clientFor(r.Context(), p.acct.Email)
			if err != nil {
				// A dead credential (already evicted by clientFor) — count it so
				// an all-dead session still drops the client to the login screen.
				mu.Lock()
				authFails++
				mu.Unlock()
				return
			}
			summaries, next, err := fetchThreadPage(r.Context(), svc, q, p.pageToken)
			if err != nil {
				mu.Lock()
				if fetchErr == nil {
					fetchErr = err
				}
				mu.Unlock()
				return
			}
			if labelRows {
				for i := range summaries {
					summaries[i].Account = acct.Email
				}
			}
			mu.Lock()
			all = append(all, summaries...)
			if next != "" {
				nextCursor[acct.Email] = next
			}
			mu.Unlock()
		}(p)
	}
	wg.Wait()

	// Every account's credential is dead → the session is useless; report 401 so
	// the client re-authenticates instead of rendering a phantom empty inbox.
	if len(plans) > 0 && authFails == len(plans) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Not authenticated"})
		return
	}
	if fetchErr != nil && len(all) == 0 {
		log.Printf("Fetch emails error: %v", fetchErr)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch emails"})
		return
	}

	// Interleave accounts newest-first. A single account keeps Gmail's native
	// order (already newest-first) so the lone-inbox view is byte-identical.
	if labelRows {
		sortSummaries(all)
	}
	if all == nil {
		all = []emailSummary{}
	}

	var nextPage any
	if len(nextCursor) > 0 {
		nextPage = encodeCursor(nextCursor)
	}
	writeJSON(w, http.StatusOK, map[string]any{"emails": all, "nextPageToken": nextPage})
}

// fetchThreadPage lists one page of conversations (Gmail threads) for a single
// account and resolves each thread's metadata concurrently into a summary row.
func fetchThreadPage(ctx context.Context, svc *gmail.Service, q, pageToken string) ([]emailSummary, string, error) {
	call := svc.Users.Threads.List("me").MaxResults(20)
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}
	// Optional Gmail search, passed straight through as the `q` query (supports
	// the full operator set: from:, subject:, is:unread, etc.).
	if q != "" {
		call = call.Q(q)
	}
	list, err := call.Context(ctx).Do()
	if err != nil {
		return nil, "", err
	}
	if len(list.Threads) == 0 {
		return nil, list.NextPageToken, nil
	}

	emails := make([]emailSummary, len(list.Threads))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i, th := range list.Threads {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			full, err := svc.Users.Threads.Get("me", id).
				Format("metadata").
				MetadataHeaders("From", "Subject", "Date").
				Context(ctx).Do()
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			emails[i] = summarizeThread(full)
		}(i, th.Id)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, "", firstErr
	}
	return emails, list.NextPageToken, nil
}

// encodeCursor / decodeCursor serialize the per-account pagination map as an
// opaque base64 token the frontend echoes back unchanged.
func encodeCursor(m map[string]string) string {
	b, _ := json.Marshal(m)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) map[string]string {
	if s == "" {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil // garbage / a stale raw Gmail token → restart from page one
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

var emailIDRe = regexp.MustCompile(`^/api/emails/([^/]+)$`)

func (s *server) handleEmailByID(w http.ResponseWriter, r *http.Request) {
	m := emailIDRe.FindStringSubmatch(r.URL.Path)
	if m == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	id := m[1]

	sc := sessionFrom(r)
	// ?account selects which linked mailbox the thread id lives in (the list row
	// carried it). Empty → the primary account (single-inbox behavior).
	email := r.URL.Query().Get("account")
	svc, acct, err := sc.clientFor(r.Context(), email)
	if err != nil {
		if errors.Is(err, errNoAccount) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Account not linked"})
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Not authenticated"})
		return
	}

	// `id` is a thread id (the inbox list returns threads now). Fetch the whole
	// conversation so the reader can stack every message.
	th, err := svc.Users.Threads.Get("me", id).Format("full").Context(r.Context()).Do()
	if err != nil {
		log.Printf("Fetch thread error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch email"})
		return
	}

	out := buildThread(th)
	out.Account = acct.Email
	writeJSON(w, http.StatusOK, out)
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
// minted token (merged over the previous one) back to the owning account in the
// store. email selects that account (empty → the session's primary account).
type persistingSource struct {
	store        *tokenStore
	sessionToken string
	email        string
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
		p.store.setAccountToken(p.sessionToken, p.email, merged)
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

// sortSummaries orders rows newest-first by timestamp. Used when more than one
// account is merged into a single inbox so the accounts interleave by date.
func sortSummaries(in []emailSummary) {
	sort.Slice(in, func(i, j int) bool { return in[i].Ts > in[j].Ts })
}
