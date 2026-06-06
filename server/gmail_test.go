package main

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	gmail "google.golang.org/api/gmail/v1"
)

func TestSplitFrom(t *testing.T) {
	cases := []struct {
		in         string
		wantName   string
		wantSender string
	}{
		{`"Liam" <liam@irrssue.com>`, "Liam", "liam@irrssue.com"},
		{`Liam <liam@irrssue.com>`, "Liam", "liam@irrssue.com"},
		{`<bare@x.com>`, "", "bare@x.com"},
		{`noangle@x.com`, "noangle@x.com", "noangle@x.com"},
		{`"GitHub" <notifications@github.com>`, "GitHub", "notifications@github.com"},
		{``, "", ""},
		{`  Padded Name   <p@x.com>`, "Padded Name", "p@x.com"},
	}
	for _, c := range cases {
		name, sender := splitFrom(c.in)
		if name != c.wantName || sender != c.wantSender {
			t.Errorf("splitFrom(%q) = (%q, %q); want (%q, %q)", c.in, name, sender, c.wantName, c.wantSender)
		}
	}
}

func TestHeaderValue(t *testing.T) {
	hs := []*gmail.MessagePartHeader{
		{Name: "From", Value: "a@x.com"},
		{Name: "Subject", Value: "hi"},
	}
	if got := headerValue(hs, "Subject"); got != "hi" {
		t.Errorf("Subject = %q", got)
	}
	if got := headerValue(hs, "Missing"); got != "" {
		t.Errorf("Missing should be empty, got %q", got)
	}
}

func TestHasLabel(t *testing.T) {
	if !hasLabel([]string{"INBOX", "UNREAD"}, "UNREAD") {
		t.Error("UNREAD should be present")
	}
	if hasLabel([]string{"INBOX"}, "UNREAD") {
		t.Error("UNREAD should be absent")
	}
	if hasLabel(nil, "UNREAD") {
		t.Error("nil labels should be absent")
	}
}

func TestRelTimeSameDay(t *testing.T) {
	// A time earlier today should format as clock time, no leading zero on hour.
	now := time.Now()
	morning := time.Date(now.Year(), now.Month(), now.Day(), 9, 5, 0, 0, now.Location())
	got := relTime(morning.Format(time.RFC1123Z))
	want := "9:05 AM"
	if got != want {
		t.Errorf("relTime today = %q; want %q", got, want)
	}
}

func TestRelTimeOtherDay(t *testing.T) {
	// A fixed past date should format as "Mon D".
	got := relTime("Wed, 04 Jun 2025 11:42:00 +0000")
	if got != "Jun 4" {
		t.Errorf("relTime past = %q; want %q", got, "Jun 4")
	}
}

func TestRelTimeEmptyAndGarbage(t *testing.T) {
	if got := relTime(""); got != "" {
		t.Errorf("empty = %q; want empty", got)
	}
	if got := relTime("not a date"); got != "" {
		t.Errorf("garbage = %q; want empty", got)
	}
}

func TestTsOf(t *testing.T) {
	// 2025-06-04T11:42:00Z = 1749037320000 ms.
	got := tsOf("Wed, 04 Jun 2025 11:42:00 +0000")
	want := int64(1749037320000)
	if got != want {
		t.Errorf("tsOf = %d; want %d", got, want)
	}
	if got := tsOf("garbage"); got != 0 {
		t.Errorf("tsOf garbage = %d; want 0", got)
	}
}

func TestWalkBodyPrefersFirstOfEach(t *testing.T) {
	enc := func(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }
	payload := &gmail.MessagePart{
		MimeType: "multipart/alternative",
		Parts: []*gmail.MessagePart{
			{MimeType: "text/plain", Body: &gmail.MessagePartBody{Data: enc("plain one")}},
			{MimeType: "text/plain", Body: &gmail.MessagePartBody{Data: enc("plain two")}},
			{MimeType: "text/html", Body: &gmail.MessagePartBody{Data: enc("<p>html one</p>")}},
		},
	}
	var body, html string
	walkBody(payload, &body, &html)
	if body != "plain one" {
		t.Errorf("body = %q; want %q", body, "plain one")
	}
	if html != "<p>html one</p>" {
		t.Errorf("html = %q; want %q", html, "<p>html one</p>")
	}
}

func TestWalkBodyNested(t *testing.T) {
	enc := func(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }
	payload := &gmail.MessagePart{
		MimeType: "multipart/mixed",
		Parts: []*gmail.MessagePart{
			{MimeType: "multipart/alternative", Parts: []*gmail.MessagePart{
				{MimeType: "text/html", Body: &gmail.MessagePartBody{Data: enc("<b>nested</b>")}},
			}},
		},
	}
	var body, html string
	walkBody(payload, &body, &html)
	if html != "<b>nested</b>" {
		t.Errorf("nested html = %q", html)
	}
	if body != "" {
		t.Errorf("body should be empty, got %q", body)
	}
}

// TestSummaryJSONShape locks the exact JSON keys the frontend reads.
func TestSummaryJSONShape(t *testing.T) {
	b, _ := json.Marshal(emailSummary{ID: "1", Name: "n", Sender: "s", Subject: "sub", Snippet: "snip", Date: "9:05 AM", Ts: 5, Unread: true})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"id", "name", "sender", "subject", "snippet", "date", "ts", "unread"} {
		if _, ok := m[k]; !ok {
			t.Errorf("summary JSON missing key %q (got %v)", k, m)
		}
	}
}

func TestFullJSONShape(t *testing.T) {
	b, _ := json.Marshal(emailFull{ID: "1", Name: "n", Sender: "s", Subject: "sub", To: "t", Body: "b", BodyHTML: "<p>", Snippet: "snip"})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"id", "name", "sender", "subject", "to", "body", "bodyHtml", "snippet"} {
		if _, ok := m[k]; !ok {
			t.Errorf("full JSON missing key %q (got %v)", k, m)
		}
	}
}
