package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postSend drives handleSend with a raw JSON body and returns the recorder.
// The bad-input paths (validation 400s) fire before any Gmail call, so no mock
// Gmail client is needed.
func postSend(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleSend(w, r)
	return w
}

func TestHandleSendRejectsInjectedInReplyTo(t *testing.T) {
	w := postSend(t, `{"to":"a@x.com","body":"hi","inReplyTo":"<a@b>\r\nBcc: evil@x.com"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("injected inReplyTo: status = %d; want 400\nbody: %s", w.Code, w.Body.String())
	}
}

func TestHandleSendRejectsBadRecipient(t *testing.T) {
	if w := postSend(t, `{"to":"not-an-email","body":"hi"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad recipient: status = %d; want 400", w.Code)
	}
}

func TestHandleSendRejectsEmptyBody(t *testing.T) {
	if w := postSend(t, `{"to":"a@x.com","body":"   "}`); w.Code != http.StatusBadRequest {
		t.Fatalf("empty body: status = %d; want 400", w.Code)
	}
}

func TestParseRecipients(t *testing.T) {
	cases := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"a@x.com", []string{"a@x.com"}, false},
		{"a@x.com, b@y.com", []string{"a@x.com", "b@y.com"}, false},
		{`"Liam" <liam@irrssue.com>`, []string{"liam@irrssue.com"}, false},
		{"  spaced@x.com  ", []string{"spaced@x.com"}, false},
		{"", nil, true},
		{"   ", nil, true},
		{"not-an-email", nil, true},
		{"a@x.com, garbage", nil, true},
	}
	for _, c := range cases {
		got, err := parseRecipients(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseRecipients(%q) = %v; want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseRecipients(%q) unexpected error: %v", c.in, err)
			continue
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("parseRecipients(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}

func TestParseOptionalRecipients(t *testing.T) {
	// Empty Cc/Bcc is valid (no recipients, no error).
	if got, err := parseOptionalRecipients("  "); err != nil || got != nil {
		t.Errorf("parseOptionalRecipients(blank) = (%v, %v); want (nil, nil)", got, err)
	}
	if got, err := parseOptionalRecipients("a@x.com"); err != nil || len(got) != 1 {
		t.Errorf("parseOptionalRecipients(addr) = (%v, %v)", got, err)
	}
	if _, err := parseOptionalRecipients("garbage"); err == nil {
		t.Error("parseOptionalRecipients(garbage) want error")
	}
}

func mustBuildMIME(t *testing.T, in mimeInput) string {
	t.Helper()
	raw, err := buildMIME(in)
	if err != nil {
		t.Fatalf("buildMIME error: %v", err)
	}
	return string(raw)
}

func TestBuildMIMEMultipart(t *testing.T) {
	raw := mustBuildMIME(t, mimeInput{
		to:      []string{"a@x.com", "b@y.com"},
		subject: "Hello",
		body:    "Line one\n\n**bold**",
	})

	wantSubstr := []string{
		"To: a@x.com, b@y.com\r\n",
		"Subject: Hello\r\n",
		"Content-Type: multipart/alternative; boundary=",
		`Content-Type: text/plain; charset="UTF-8"`,
		`Content-Type: text/html; charset="UTF-8"`,
		"Line one\r\n",      // plain part keeps the raw markdown
		"<strong>bold</strong>", // html part is rendered
	}
	for _, sub := range wantSubstr {
		if !strings.Contains(raw, sub) {
			t.Errorf("buildMIME missing %q\n--- got ---\n%s", sub, raw)
		}
	}
}

func TestBuildMIMECcBcc(t *testing.T) {
	raw := mustBuildMIME(t, mimeInput{
		to:      []string{"a@x.com"},
		cc:      []string{"c@x.com"},
		bcc:     []string{"d@x.com"},
		subject: "s",
		body:    "hi",
	})
	if !strings.Contains(raw, "Cc: c@x.com\r\n") {
		t.Errorf("missing Cc header:\n%s", raw)
	}
	if !strings.Contains(raw, "Bcc: d@x.com\r\n") {
		t.Errorf("missing Bcc header:\n%s", raw)
	}
}

func TestBuildMIMEOmitsEmptyCcBcc(t *testing.T) {
	raw := mustBuildMIME(t, mimeInput{to: []string{"a@x.com"}, subject: "s", body: "hi"})
	if strings.Contains(raw, "Cc:") {
		t.Errorf("Cc header should be absent when empty:\n%s", raw)
	}
	if strings.Contains(raw, "Bcc:") {
		t.Errorf("Bcc header should be absent when empty:\n%s", raw)
	}
}

func TestValidInReplyTo(t *testing.T) {
	good := []string{
		"<abc123@mail.gmail.com>",
		"<CADnq=+u_8@example.com>",
	}
	for _, s := range good {
		if !validInReplyTo(s) {
			t.Errorf("validInReplyTo(%q) = false; want true", s)
		}
	}
	bad := []string{
		"",                              // empty handled by caller, not valid here
		"abc@example.com",               // missing angle brackets
		"<a@b> <c@d>",                   // two ids / whitespace
		"<a@b>\r\nBcc: evil@x.com",      // CRLF injection
		"<a@b>\nSubject: forged",        // bare LF injection
		"<a b@example.com>",             // internal whitespace
		"<no-at-sign>",                  // not an addr-spec
	}
	for _, s := range bad {
		if validInReplyTo(s) {
			t.Errorf("validInReplyTo(%q) = true; want false", s)
		}
	}
}

func TestBuildMIMEHeaderInjectionStripped(t *testing.T) {
	// Defense-in-depth: even if a raw CRLF value reaches buildMIME, the header
	// writer must not fold a forged header onto the wire.
	raw := mustBuildMIME(t, mimeInput{
		to:        []string{"a@x.com"},
		subject:   "hi\r\nBcc: evil@x.com",
		body:      "hi",
		inReplyTo: "<a@b>\r\nX-Injected: 1",
	})
	// The payload may survive as inert text inside a value; what must NOT exist
	// is a folded header line — i.e. the injected token at the start of a line.
	if strings.Contains(raw, "\nX-Injected") {
		t.Errorf("injected header folded onto its own line:\n%s", raw)
	}
	// Subject CRLF is neutralized by RFC 2047 encoding; ensure no raw Bcc line
	// appears from the subject payload either.
	if strings.Contains(raw, "\nBcc: evil@x.com") {
		t.Errorf("forged Bcc survived:\n%s", raw)
	}
}

func TestBuildMIMEReplyHeaders(t *testing.T) {
	raw := mustBuildMIME(t, mimeInput{
		to:        []string{"a@x.com"},
		subject:   "Re: hi",
		body:      "reply",
		inReplyTo: "<abc123@mail.gmail.com>",
	})
	if !strings.Contains(raw, "In-Reply-To: <abc123@mail.gmail.com>\r\n") {
		t.Errorf("missing In-Reply-To:\n%s", raw)
	}
	if !strings.Contains(raw, "References: <abc123@mail.gmail.com>\r\n") {
		t.Errorf("missing References:\n%s", raw)
	}
}

func TestBuildMIMENoReplyHeadersWhenFresh(t *testing.T) {
	raw := mustBuildMIME(t, mimeInput{to: []string{"a@x.com"}, subject: "s", body: "hi"})
	if strings.Contains(raw, "In-Reply-To:") || strings.Contains(raw, "References:") {
		t.Errorf("fresh message must not carry reply headers:\n%s", raw)
	}
}

func TestBuildMIMEEncodesNonASCIISubject(t *testing.T) {
	raw := mustBuildMIME(t, mimeInput{to: []string{"a@x.com"}, subject: "Café — déjà vu", body: "body"})
	if strings.Contains(raw, "Subject: Café") {
		t.Errorf("non-ASCII subject not encoded:\n%s", raw)
	}
	if !strings.Contains(raw, "=?utf-8?q?") {
		t.Errorf("expected RFC 2047 encoded-word subject:\n%s", raw)
	}
}

func TestNormalizeCRLF(t *testing.T) {
	cases := map[string]string{
		"a\nb":     "a\r\nb",
		"a\r\nb":   "a\r\nb",
		"a\rb":     "a\r\nb",
		"a\r\n\nb": "a\r\n\r\nb",
		"plain":    "plain",
	}
	for in, want := range cases {
		if got := normalizeCRLF(in); got != want {
			t.Errorf("normalizeCRLF(%q) = %q; want %q", in, got, want)
		}
	}
}
