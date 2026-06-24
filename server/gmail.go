package main

import (
	"encoding/base64"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	gmail "google.golang.org/api/gmail/v1"
)

// emailSummary is one inbox list row: who | subject·snippet | relative-time.
// A row now represents a whole conversation (Gmail thread): the latest message
// supplies who/when/snippet, the first message supplies the subject, and Count
// is how many messages the thread holds (the frontend shows a chip when > 1).
type emailSummary struct {
	ID      string `json:"id"` // thread id
	Name    string `json:"name"`
	Sender  string `json:"sender"`
	Subject string `json:"subject"`
	Snippet string `json:"snippet"`
	Date    string `json:"date"`
	Ts      int64  `json:"ts"`
	Unread  bool   `json:"unread"`
	Count   int    `json:"count"`
	// Account is the Google account (email) this thread belongs to. The list
	// handler sets it when more than one account is linked so the frontend can
	// tag the row and route the reader/reply to the right mailbox. Empty for a
	// lone account (single-inbox behavior, unchanged).
	Account string `json:"account,omitempty"`
}

// emailFull is the reader payload: serif subject, mono From/To head, prose body.
// ThreadID + MessageID + References are carried so the frontend can open a
// threaded reply (they feed /api/send's inReplyTo/references/threadId).
type emailFull struct {
	ID         string `json:"id"`
	ThreadID   string `json:"threadId"`
	MessageID  string `json:"messageId"`
	References string `json:"references"`
	Name       string `json:"name"`
	Sender     string `json:"sender"`
	Subject    string `json:"subject"`
	To         string `json:"to"`
	Body       string `json:"body"`
	BodyHTML   string `json:"bodyHtml"`
	Snippet    string `json:"snippet"`
}

// threadMessage is one message inside a conversation reader. Same per-message
// fields the single-message reader used, plus an Unread flag so the frontend
// can leave unread messages expanded.
type threadMessage struct {
	ID        string `json:"id"`
	MessageID string `json:"messageId"`
	Name      string `json:"name"`
	Sender    string `json:"sender"`
	To        string `json:"to"`
	Date      string `json:"date"`
	Ts        int64  `json:"ts"`
	Body      string `json:"body"`
	BodyHTML  string `json:"bodyHtml"`
	Snippet   string `json:"snippet"`
	Unread    bool   `json:"unread"`
}

// threadFull is the reader payload for a conversation: every message in
// chronological order plus thread-level subject. The top-level Sender/MessageID
// (the latest message's) and ThreadID feed /api/send so the keyboard `r` reply
// threads into the same conversation.
type threadFull struct {
	ID         string          `json:"id"` // thread id (== ThreadID)
	ThreadID   string          `json:"threadId"`
	Subject    string          `json:"subject"`
	Name       string          `json:"name"`
	Sender     string          `json:"sender"`
	To         string          `json:"to"`
	MessageID  string          `json:"messageId"`
	References string          `json:"references"`
	Snippet    string          `json:"snippet"`
	Messages   []threadMessage `json:"messages"`
	// Account is the Google account (email) this conversation belongs to, echoed
	// back so a reply sends from the mailbox that received it.
	Account string `json:"account,omitempty"`
}

// partHeaders safely returns a message's MIME headers, tolerating a nil payload
// (some Gmail responses omit it for empty/draft parts).
func partHeaders(m *gmail.Message) []*gmail.MessagePartHeader {
	if m == nil || m.Payload == nil {
		return nil
	}
	return m.Payload.Headers
}

// summarizeThread collapses a Gmail thread into one inbox row. The latest
// message gives who/when/snippet; the subject is the first non-empty Subject in
// the thread (replies often carry "Re:" or an empty subject); the row is unread
// if any message in the thread is unread.
func summarizeThread(th *gmail.Thread) emailSummary {
	msgs := th.Messages
	if len(msgs) == 0 {
		return emailSummary{ID: th.Id, Snippet: th.Snippet}
	}
	latest := msgs[len(msgs)-1]
	name, sender := splitFrom(headerValue(partHeaders(latest), "From"))

	subject := ""
	unread := false
	for _, m := range msgs {
		if subject == "" {
			subject = headerValue(partHeaders(m), "Subject")
		}
		if hasLabel(m.LabelIds, "UNREAD") {
			unread = true
		}
	}

	dateHeader := headerValue(partHeaders(latest), "Date")
	return emailSummary{
		ID:      th.Id,
		Name:    name,
		Sender:  sender,
		Subject: subject,
		Snippet: latest.Snippet,
		Date:    relTime(dateHeader),
		Ts:      tsOf(dateHeader),
		Unread:  unread,
		Count:   len(msgs),
	}
}

// buildThread expands a full Gmail thread into the reader payload: every
// message in conversation order, the thread subject, and the latest message's
// identity/threading headers for a reply.
func buildThread(th *gmail.Thread) threadFull {
	out := threadFull{ID: th.Id, ThreadID: th.Id}
	for _, msg := range th.Messages {
		headers := partHeaders(msg)
		name, sender := splitFrom(headerValue(headers, "From"))
		var body, html string
		walkBody(msg.Payload, &body, &html)
		dateHeader := headerValue(headers, "Date")
		out.Messages = append(out.Messages, threadMessage{
			ID:        msg.Id,
			MessageID: headerValue(headers, "Message-ID"),
			Name:      name,
			Sender:    sender,
			To:        headerValue(headers, "To"),
			Date:      relTime(dateHeader),
			Ts:        tsOf(dateHeader),
			Body:      body,
			BodyHTML:  html,
			Snippet:   msg.Snippet,
			Unread:    hasLabel(msg.LabelIds, "UNREAD"),
		})
		if out.Subject == "" {
			out.Subject = headerValue(headers, "Subject")
		}
	}
	if n := len(th.Messages); n > 0 {
		last := th.Messages[n-1]
		lastOut := out.Messages[n-1]
		out.Name = lastOut.Name
		out.Sender = lastOut.Sender
		out.To = lastOut.To
		out.MessageID = lastOut.MessageID
		out.Snippet = lastOut.Snippet
		out.References = headerValue(partHeaders(last), "References")
	}
	return out
}

var fromRe = regexp.MustCompile(`^"?([^"<]*)"?\s*<(.+)>$`)

// splitFrom parses a From header into display name + email. Mirrors the JS
// regex: `^"?([^"<]*)"?\s*<(.+)>$`. No angle brackets → name and sender both
// equal the raw header.
func splitFrom(from string) (name, sender string) {
	m := fromRe.FindStringSubmatch(from)
	if m != nil {
		return strings.TrimSpace(m[1]), m[2]
	}
	return from, from
}

func headerValue(headers []*gmail.MessagePartHeader, name string) string {
	for _, h := range headers {
		if h.Name == name {
			return h.Value
		}
	}
	return ""
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

// relTime formats an email Date header into a short label.
// Today → clock time (3:04 PM); older → month/day (Jan 2). Matches the JS
// toLocaleTimeString / toLocaleDateString output for the en-US default.
func relTime(dateHeader string) string {
	then, ok := parseDate(dateHeader)
	if !ok {
		return ""
	}
	now := time.Now()
	sameDay := then.Year() == now.Year() && then.YearDay() == now.YearDay()
	if sameDay {
		// "3:04 PM" — strip a leading zero from the hour to match JS "numeric".
		return strings.TrimPrefix(then.Format("3:04 PM"), "0")
	}
	return then.Format("Jan 2")
}

// tsOf returns the Date header as a unix-millis timestamp, or 0 if unparseable.
func tsOf(dateHeader string) int64 {
	then, ok := parseDate(dateHeader)
	if !ok {
		return 0
	}
	return then.UnixMilli()
}

// parseDate tries the RFC formats Gmail Date headers actually use, plus a few
// lenient fallbacks, so we tolerate the same range JS `new Date()` accepts.
func parseDate(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC1123Z, // Mon, 02 Jan 2006 15:04:05 -0700
		time.RFC1123,  // Mon, 02 Jan 2006 15:04:05 MST
		"2 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04:05 -0700",
		time.RFC822Z,
		time.RFC822,
	}
	// Gmail often appends "(UTC)" / "(PST)" comments — strip a trailing paren group.
	clean := s
	if i := strings.LastIndex(clean, " ("); i != -1 {
		clean = strings.TrimSpace(clean[:i])
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, clean); err == nil {
			return t, true
		}
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// decodeBody decodes a Gmail base64url body part. Gmail uses URL-safe base64
// (sometimes unpadded), so try that first and fall back to standard.
func decodeBody(data string) string {
	if b, err := base64.URLEncoding.DecodeString(data); err == nil {
		return string(b)
	}
	if b, err := base64.RawURLEncoding.DecodeString(data); err == nil {
		return string(b)
	}
	if b, err := base64.StdEncoding.DecodeString(data); err == nil {
		return string(b)
	}
	return ""
}

// walkBody walks the MIME tree and collects the first text/plain and text/html
// bodies. GitHub-style notifications carry the rich card in text/html, so we
// surface that when present and keep plain text as a fallback.
func walkBody(part *gmail.MessagePart, body, html *string) {
	if part == nil {
		return
	}
	mime := part.MimeType
	if mime == "text/plain" && *body == "" && part.Body != nil && part.Body.Data != "" {
		*body = decodeBody(part.Body.Data)
	} else if mime == "text/html" && *html == "" && part.Body != nil && part.Body.Data != "" {
		*html = decodeBody(part.Body.Data)
	}
	for _, p := range part.Parts {
		walkBody(p, body, html)
	}
}

// itoa is a tiny strconv.Itoa wrapper used by sessions.go's temp-file naming.
func itoa(i int) string { return strconv.Itoa(i) }

// clampInt64 keeps a float in int64 range when converting (defensive; unused
// hot path but avoids surprises if a future Date math overflows).
func clampInt64(f float64) int64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return int64(f)
}
