package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
	"net/mail"
	"strings"

	"google.golang.org/api/googleapi"
	gmail "google.golang.org/api/gmail/v1"
)

// sendRequest is the compose payload from the frontend: { to, subject, body }.
// `to` may be a comma-separated list; each address is validated.
type sendRequest struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// handleSend builds an RFC 2822 message from the compose form and hands it to
// Gmail's Users.Messages.Send. Gmail fills From/Date and the message-id itself,
// so the wire body only carries To/Subject + a plain-text body. Requires the
// gmail.send scope — sessions consented under readonly-only get a 403 from
// Google, which we surface as 403 so the frontend can tell the user to re-auth.
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

	recipients, err := parseRecipients(req.To)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Message body is empty"})
		return
	}

	raw := buildMIME(recipients, req.Subject, req.Body)

	svc := gmailFrom(r)
	sent, err := svc.Users.Messages.Send("me", &gmail.Message{
		Raw: base64.URLEncoding.EncodeToString([]byte(raw)),
	}).Do()
	if err != nil {
		// Insufficient scope (readonly-only session): tell the client to re-auth
		// rather than reporting a generic 500.
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

	writeJSON(w, http.StatusOK, map[string]string{"id": sent.Id})
}

// parseRecipients splits a comma-separated To field and validates each address
// with net/mail. Returns the cleaned address list, or an error naming the first
// bad entry. At least one recipient is required.
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

// buildMIME assembles a minimal RFC 2822 plain-text message. The subject is
// RFC 2047 encoded-word wrapped (so non-ASCII subjects survive), and the body
// goes out as UTF-8 text/plain. CRLF line endings per the spec. Headers Gmail
// supplies (From, Date, Message-ID) are intentionally omitted.
func buildMIME(recipients []string, subject, body string) string {
	var b strings.Builder
	b.WriteString("To: ")
	b.WriteString(strings.Join(recipients, ", "))
	b.WriteString("\r\n")

	b.WriteString("Subject: ")
	b.WriteString(mime.QEncoding.Encode("utf-8", subject))
	b.WriteString("\r\n")

	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")

	// Normalize body line endings to CRLF.
	b.WriteString(normalizeCRLF(body))
	return b.String()
}

// normalizeCRLF converts any lone LF / CR to CRLF so the body matches RFC 2822
// line-ending rules regardless of what the browser sent.
func normalizeCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}
