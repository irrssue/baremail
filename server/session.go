package main

import (
	"encoding/json"
)

// A session is one browser login. It holds one or more linked Google accounts;
// the inbox merges mail across all of them. The first account is the primary
// (its avatar heads the topbar, new compose defaults to it).
//
// On disk a single anonymous account (one with no identity yet — the shape every
// pre-multi-account session and the old Node store wrote) still serializes as a
// bare googleapis token object, so existing sessions.json files load unchanged.
// Sessions with a known account email — every fresh login now records one — or
// more than one account use the richer { "accounts": [...] } shape.
type session struct {
	accounts []*account
}

// account is one linked Google identity inside a session: its OAuth token plus
// the email/name/photo (from the OIDC userinfo endpoint) used to label inbox
// rows, de-duplicate re-logins, and render the account switcher.
type account struct {
	Email   string
	Name    string
	Picture string
	tok     *googleToken
}

// accountWire is the on-disk shape of one account inside the multi-account form.
type accountWire struct {
	Email   string       `json:"email,omitempty"`
	Name    string       `json:"name,omitempty"`
	Picture string       `json:"picture,omitempty"`
	Token   *googleToken `json:"token"`
}

// anonymous reports whether the account carries no identity yet — the legacy
// single-account shape (token only). Such a session is written back in the old
// bare-token form for byte-compatibility with the pre-multi-account store.
func (a *account) anonymous() bool {
	return a.Email == "" && a.Name == "" && a.Picture == ""
}

// find returns the account with the given email, or the primary (first) account
// when email is empty. A non-empty email that isn't linked returns nil.
func (sess *session) find(email string) *account {
	if email == "" {
		if len(sess.accounts) > 0 {
			return sess.accounts[0]
		}
		return nil
	}
	for _, a := range sess.accounts {
		if a.Email == email {
			return a
		}
	}
	return nil
}

// clone deep-copies the session so a caller can read accounts outside the store
// lock without racing a concurrent token refresh writing them back.
func (sess *session) clone() *session {
	out := &session{accounts: make([]*account, len(sess.accounts))}
	for i, a := range sess.accounts {
		ac := &account{Email: a.Email, Name: a.Name, Picture: a.Picture}
		if a.tok != nil && a.tok.Token != nil {
			tokCopy := *a.tok.Token
			ac.tok = &googleToken{Token: &tokCopy, extra: a.tok.extra}
		} else {
			ac.tok = wrapToken(nil)
		}
		out.accounts[i] = ac
	}
	return out
}

func (sess *session) UnmarshalJSON(b []byte) error {
	// Probe for the multi-account shape (an "accounts" array). Anything else is
	// the legacy bare-token object the old store wrote.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	if rawAccts, ok := probe["accounts"]; ok {
		var wires []accountWire
		if err := json.Unmarshal(rawAccts, &wires); err != nil {
			return err
		}
		sess.accounts = make([]*account, 0, len(wires))
		for _, w := range wires {
			tok := w.Token
			if tok == nil {
				tok = wrapToken(nil)
			}
			sess.accounts = append(sess.accounts, &account{
				Email: w.Email, Name: w.Name, Picture: w.Picture, tok: tok,
			})
		}
		return nil
	}

	// Legacy shape: the object is a bare googleapis token. Load it as a single
	// anonymous account.
	var gt googleToken
	if err := json.Unmarshal(b, &gt); err != nil {
		return err
	}
	sess.accounts = []*account{{tok: &gt}}
	return nil
}

func (sess *session) MarshalJSON() ([]byte, error) {
	// A lone anonymous account writes back in the old bare-token shape so the
	// file stays readable by anything that read the pre-multi-account store.
	if len(sess.accounts) == 1 && sess.accounts[0].anonymous() {
		return json.Marshal(sess.accounts[0].tok)
	}
	wires := make([]accountWire, 0, len(sess.accounts))
	for _, a := range sess.accounts {
		wires = append(wires, accountWire{
			Email: a.Email, Name: a.Name, Picture: a.Picture, Token: a.tok,
		})
	}
	return json.Marshal(struct {
		Accounts []accountWire `json:"accounts"`
	}{wires})
}
