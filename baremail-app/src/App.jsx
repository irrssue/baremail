import { useState, useEffect } from "react"

const API = import.meta.env.VITE_API_URL || ""

function getToken() {
  return sessionStorage.getItem("bm_token")
}

function authHeaders() {
  const token = getToken()
  return token ? { "x-session-token": token } : {}
}

function Shell({ children, onBrand, onSignOut }) {
  return (
    <>
      <header className="topbar">
        <button className="brand" onClick={onBrand}>
          baremail
        </button>
        {onSignOut && (
          <div className="right">
            <button className="signout-btn" onClick={onSignOut}>
              sign out
            </button>
          </div>
        )}
      </header>
      <main className="page-main">{children}</main>
      <footer className="site-footer">
        <div className="site-footer-inner">
          <span className="foot-brand">baremail</span>
          <nav className="foot-links">
            <a href="mailto:liam@irrssue.com">contact</a>
            <span className="foot-dot">·</span>
            <a href="https://github.com/irrssue" target="_blank" rel="noreferrer">
              github
            </a>
          </nav>
        </div>
      </footer>
    </>
  )
}

function App() {
  const [emails, setEmails] = useState([])
  const [selected, setSelected] = useState(null)
  const [authenticated, setAuthenticated] = useState(false)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    // Pick up token from URL after OAuth redirect
    const params = new URLSearchParams(window.location.search)
    const urlToken = params.get("token")
    if (urlToken) {
      sessionStorage.setItem("bm_token", urlToken)
      window.history.replaceState({}, "", window.location.pathname)
    }

    fetch(`${API}/auth/status`, { headers: authHeaders() })
      .then((r) => r.json())
      .then((data) => {
        setAuthenticated(data.authenticated)
        if (data.authenticated) loadEmails()
        else setLoading(false)
      })
      .catch(() => setLoading(false))
  }, [])

  function loadEmails() {
    setLoading(true)
    fetch(`${API}/api/emails`, { headers: authHeaders() })
      .then((r) => r.json())
      .then((data) => {
        setEmails(Array.isArray(data) ? data : [])
        setLoading(false)
      })
      .catch(() => setLoading(false))
  }

  function openEmail(email) {
    fetch(`${API}/api/emails/${email.id}`, { headers: authHeaders() })
      .then((r) => r.json())
      .then((data) => setSelected(data))
  }

  function signOut() {
    fetch(`${API}/auth/logout`, { headers: authHeaders() }).then(() => {
      sessionStorage.removeItem("bm_token")
      setAuthenticated(false)
      setEmails([])
      setSelected(null)
    })
  }

  if (loading) {
    return (
      <Shell onBrand={() => setSelected(null)}>
        <div className="status">Loading…</div>
      </Shell>
    )
  }

  if (!authenticated) {
    return (
      <Shell onBrand={() => setSelected(null)}>
        <div className="login">
          <h2>baremail</h2>
          <p>A bare, minimal reader for your inbox. Nothing more.</p>
          <a className="login-btn" href={`${API}/auth/google`}>
            Sign in with Google
          </a>
        </div>
      </Shell>
    )
  }

  if (selected) {
    return (
      <Shell onBrand={() => setSelected(null)} onSignOut={signOut}>
        <article className="reader">
          <button className="crumb" onClick={() => setSelected(null)}>
            ← inbox
          </button>
          <h1>{selected.subject || "(no subject)"}</h1>
          <div className="head">
            <div className="line">
              <span className="label">From</span> <b>{selected.sender}</b>
            </div>
            <div className="line">
              <span className="label">To</span> {selected.to}
            </div>
          </div>
          <div className="body">{selected.body || selected.snippet}</div>
        </article>
      </Shell>
    )
  }

  return (
    <Shell onBrand={() => setSelected(null)} onSignOut={signOut}>
      {emails.length === 0 ? (
        <div className="empty">Inbox empty.</div>
      ) : (
        <div className="maillist">
          {emails.map((email) => (
            <div
              key={email.id}
              className="mailrow"
              onClick={() => openEmail(email)}
            >
              <span className="who">{email.name}</span>
              <span className="subj">
                {email.subject}
                {email.snippet && <span className="snip">{email.snippet}</span>}
              </span>
              {email.date && <span className="when">{email.date}</span>}
            </div>
          ))}
        </div>
      )}
    </Shell>
  )
}

export default App
