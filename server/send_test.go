package main

import (
	"strings"
	"testing"
)

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

func TestBuildMIME(t *testing.T) {
	raw := buildMIME([]string{"a@x.com", "b@y.com"}, "Hello", "Line one\nLine two")

	wantSubstr := []string{
		"To: a@x.com, b@y.com\r\n",
		"Subject: Hello\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=\"UTF-8\"\r\n",
		"\r\nLine one\r\nLine two", // header/body separator + CRLF-joined body
	}
	for _, sub := range wantSubstr {
		if !strings.Contains(raw, sub) {
			t.Errorf("buildMIME missing %q\n--- got ---\n%q", sub, raw)
		}
	}

	// No bare LF should survive (every \n must be part of a \r\n).
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\n' && (i == 0 || raw[i-1] != '\r') {
			t.Fatalf("bare LF at byte %d in MIME output", i)
		}
	}
}

func TestBuildMIMEEncodesNonASCIISubject(t *testing.T) {
	raw := buildMIME([]string{"a@x.com"}, "Café — déjà vu", "body")
	// The raw subject must not leak verbatim; it should be RFC 2047 encoded.
	if strings.Contains(raw, "Café") {
		t.Errorf("non-ASCII subject not encoded:\n%q", raw)
	}
	if !strings.Contains(raw, "=?utf-8?q?") {
		t.Errorf("expected RFC 2047 encoded-word subject, got:\n%q", raw)
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
