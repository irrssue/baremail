package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/mail"
	"net/textproto"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"google.golang.org/api/googleapi"
	gmail "google.golang.org/api/gmail/v1"
)

// sendRequest is the compose payload. `to`/`cc`/`bcc` may each be a
// comma-separated address list. `body` is Markdown. The three reply fields are
// optional — when set, the message is threaded onto an existing conversation.
type sendRequest struct {
	To        string `json:"to"`
	Cc        string `json:"cc"`
	Bcc       string `json:"bcc"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
	InReplyTo string `json:"inReplyTo"` // Message-ID of the message being replied to
	ThreadID  string `json:"threadId"`  // Gmail thread to attach the reply to
	Account   string `json:"account"`   // which linked account to send from ("" → primary)
}

// md renders Markdown → HTML. GFM gives tables, strikethrough, autolinks, and
// task lists — the things a person actually types in a day-to-day email.
var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

// msgIDRe matches a single RFC 5322 msg-id: one angle-bracketed addr-spec with
// no whitespace. inReplyTo reaches us from the Message-ID header of an *inbound*
// (attacker-controllable) email, so it must be validated before it's written to
// a raw header — otherwise a crafted value could inject CRLF and forge headers
// (Bcc, extra body, etc.) into the outgoing reply. Recipients (net/mail) and the
// subject (RFC 2047 encoding) are already CRLF-safe; this is the one raw path.
var msgIDRe = regexp.MustCompile(`^<[^\s<>@]+@[^\s<>@]+>$`)

// validInReplyTo reports whether s is safe to write into In-Reply-To/References.
func validInReplyTo(s string) bool {
	return !strings.ContainsAny(s, "\r\n") && msgIDRe.MatchString(s)
}

// handleSend builds an RFC 2822 multipart/alternative message (Markdown source
// as text/plain + rendered HTML as text/html) and hands it to Gmail's
// Users.Messages.Send. Gmail fills From/Date/Message-ID. To/Cc are validated;
// Bcc recipients ride in the header (Gmail honors and then strips it). Reply
// fields thread the message via In-Reply-To/References + ThreadId. Requires the
// gmail.send scope; sessions consented under readonly-only get a 403.
func (s *server) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}

	to, err := parseRecipients(req.To)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Cc/Bcc are optional — only validate when present.
	cc, err := parseOptionalRecipients(req.Cc)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid Cc address"})
		return
	}
	bcc, err := parseOptionalRecipients(req.Bcc)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid Bcc address"})
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Message body is empty"})
		return
	}

	// inReplyTo comes from an inbound email's Message-ID and is written to a raw
	// header — reject anything that isn't a clean single msg-id (CRLF-injection
	// guard). Empty is fine: it just means "not a reply".
	inReplyTo := strings.TrimSpace(req.InReplyTo)
	if inReplyTo != "" && !validInReplyTo(inReplyTo) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid In-Reply-To"})
		return
	}

	raw, err := buildMIME(mimeInput{
		to:        to,
		cc:        cc,
		bcc:       bcc,
		subject:   req.Subject,
		body:      req.Body,
		inReplyTo: inReplyTo,
	})
	if err != nil {
		log.Printf("Build MIME error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to build message"})
		return
	}

	msg := &gmail.Message{Raw: base64.URLEncoding.EncodeToString(raw)}
	if tid := strings.TrimSpace(req.ThreadID); tid != "" {
		msg.ThreadId = tid
	}

	// Send from the account that owns the message (a reply goes out from the
	// mailbox that received it). Empty Account → the session's primary account.
	sc := sessionFrom(r)
	svc, _, err := sc.clientFor(r.Context(), req.Account)
	if err != nil {
		if errors.Is(err, errNoAccount) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Account not linked"})
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Not authenticated"})
		return
	}
	sent, err := svc.Users.Messages.Send("me", msg).Do()
	if err != nil {
		// Insufficient scope (readonly-only session): tell the client to re-auth.
		var apiErr *googleapi.Error
		if errors.As(err, &apiErr) && (apiErr.Code == http.StatusForbidden || apiErr.Code == http.StatusUnauthorized) {
			log.Printf("Send rejected (scope?): %v", err)
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "Send permission not granted. Log out and sign in again to allow sending.",
			})
			return
		}
		log.Printf("Send email error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to send email"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"id": sent.Id, "threadId": sent.ThreadId})
}

// parseRecipients splits a comma-separated address field and validates each
// entry with net/mail. At least one recipient is required.
func parseRecipients(to string) ([]string, error) {
	if strings.TrimSpace(to) == "" {
		return nil, errors.New("Recipient is required")
	}
	addrs, err := mail.ParseAddressList(to)
	if err != nil {
		return nil, errors.New("Invalid recipient address")
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.Address)
	}
	if len(out) == 0 {
		return nil, errors.New("Recipient is required")
	}
	return out, nil
}

// parseOptionalRecipients is parseRecipients for Cc/Bcc: an empty field yields
// no recipients and no error (these fields are optional).
func parseOptionalRecipients(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	return parseRecipients(s)
}

// mimeInput is the parsed, validated set of fields buildMIME assembles.
type mimeInput struct {
	to, cc, bcc []string
	subject     string
	body        string // Markdown source
	inReplyTo   string // bare Message-ID, or "" for a fresh thread
}

// buildMIME assembles a multipart/alternative RFC 2822 message: the Markdown
// source as text/plain, and its rendered HTML as text/html. Subjects are RFC
// 2047 encoded-word wrapped. From/Date/Message-ID are left for Gmail to fill.
// In-Reply-To/References are emitted when replying so clients thread the mail.
func buildMIME(in mimeInput) ([]byte, error) {
	var rendered bytes.Buffer
	if err := md.Convert([]byte(in.body), &rendered); err != nil {
		return nil, err
	}

	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	// --- Top-level headers, written by hand before the multipart body. ---
	// Strip CR/LF from every value as a last-ditch header-injection guard: even
	// if a caller skips validation, a stray newline can't fold in a forged
	// header here. (Callers still validate up front — this is defense in depth.)
	header := func(k, v string) {
		v = strings.NewReplacer("\r", "", "\n", "").Replace(v)
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}
	header("To", strings.Join(in.to, ", "))
	if len(in.cc) > 0 {
		header("Cc", strings.Join(in.cc, ", "))
	}
	if len(in.bcc) > 0 {
		// Gmail reads Bcc from the header, delivers to those recipients, and
		// strips the header from every delivered copy.
		header("Bcc", strings.Join(in.bcc, ", "))
	}
	header("Subject", mime.QEncoding.Encode("utf-8", in.subject))
	if in.inReplyTo != "" {
		// Both headers per RFC 5322 threading. We don't have the original
		// References chain here, so seed References with the parent id — enough
		// for Gmail and most clients to thread correctly.
		header("In-Reply-To", in.inReplyTo)
		header("References", in.inReplyTo)
	}
	header("MIME-Version", "1.0")
	header("Content-Type", "multipart/alternative; boundary="+w.Boundary())
	b.WriteString("\r\n") // end of top-level headers

	// --- text/plain part: the raw Markdown. ---
	plainHead := textproto.MIMEHeader{}
	plainHead.Set("Content-Type", `text/plain; charset="UTF-8"`)
	plainHead.Set("Content-Transfer-Encoding", "8bit")
	pw, err := w.CreatePart(plainHead)
	if err != nil {
		return nil, err
	}
	if _, err := pw.Write([]byte(normalizeCRLF(in.body))); err != nil {
		return nil, err
	}

	// --- text/html part: the rendered Markdown. ---
	htmlHead := textproto.MIMEHeader{}
	htmlHead.Set("Content-Type", `text/html; charset="UTF-8"`)
	htmlHead.Set("Content-Transfer-Encoding", "8bit")
	hw, err := w.CreatePart(htmlHead)
	if err != nil {
		return nil, err
	}
	if _, err := hw.Write(rendered.Bytes()); err != nil {
		return nil, err
	}

	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// normalizeCRLF converts any lone LF / CR to CRLF so the plain-text part matches
// RFC 2822 line-ending rules regardless of what the browser sent.
func normalizeCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}
