package main

import (
	"encoding/json"
	"time"

	"golang.org/x/oauth2"
)

// googleToken is an oauth2.Token that serializes in the SAME on-disk shape the
// Node `googleapis` library uses, so sessions written by the old server (and any
// future Go writes) interoperate. Google's token JSON looks like:
//
//	{ "access_token": "...", "refresh_token": "...", "scope": "...",
//	  "token_type": "Bearer", "expiry_date": 1749037320000 }
//
// Note `expiry_date` is unix MILLIS, whereas x/oauth2's own JSON uses an RFC3339
// `expiry` string. We bridge both directions and round-trip any extra fields
// (e.g. `scope`, `id_token`) so nothing the old store held is silently dropped.
type googleToken struct {
	*oauth2.Token
	extra map[string]json.RawMessage
}

// googleWire mirrors the field names googleapis writes.
type googleWire struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	Scope        string `json:"scope,omitempty"`
	ExpiryDate   int64  `json:"expiry_date,omitempty"` // unix millis
}

func (g *googleToken) UnmarshalJSON(b []byte) error {
	// First pass: known Google fields.
	var w googleWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	tok := &oauth2.Token{
		AccessToken:  w.AccessToken,
		RefreshToken: w.RefreshToken,
		TokenType:    w.TokenType,
	}
	if w.ExpiryDate > 0 {
		tok.Expiry = time.UnixMilli(w.ExpiryDate)
	}

	// Second pass: also tolerate x/oauth2's native `expiry` (RFC3339) in case a
	// token was ever written in that shape.
	var native struct {
		Expiry time.Time `json:"expiry"`
	}
	_ = json.Unmarshal(b, &native)
	if tok.Expiry.IsZero() && !native.Expiry.IsZero() {
		tok.Expiry = native.Expiry
	}

	// Preserve every original field so writes round-trip losslessly.
	all := map[string]json.RawMessage{}
	_ = json.Unmarshal(b, &all)
	delete(all, "access_token")
	delete(all, "refresh_token")
	delete(all, "token_type")
	delete(all, "expiry")
	delete(all, "expiry_date")

	g.Token = tok
	g.extra = all
	return nil
}

func (g *googleToken) MarshalJSON() ([]byte, error) {
	out := map[string]json.RawMessage{}
	for k, v := range g.extra {
		out[k] = v
	}
	put := func(k string, v any) {
		raw, err := json.Marshal(v)
		if err == nil {
			out[k] = raw
		}
	}
	if g.Token != nil {
		if g.AccessToken != "" {
			put("access_token", g.AccessToken)
		}
		if g.RefreshToken != "" {
			put("refresh_token", g.RefreshToken)
		}
		if g.TokenType != "" {
			put("token_type", g.TokenType)
		}
		if !g.Expiry.IsZero() {
			// Write in Google's millis shape so the old library could also read it.
			put("expiry_date", g.Expiry.UnixMilli())
		}
	}
	return json.Marshal(out)
}

func wrapToken(t *oauth2.Token) *googleToken {
	return &googleToken{Token: t, extra: map[string]json.RawMessage{}}
}
