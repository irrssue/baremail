import { useState, useEffect, useRef, useCallback } from "react"

const API = import.meta.env.VITE_API_URL || ""

function getToken() {
  return sessionStorage.getItem("bm_token")
}

// Bucket an email timestamp the way twobird.com does: Today, Yesterday,
// This Week, Last Week, then month names for anything older.
function bucketOf(ts, now = new Date()) {
  if (!ts) return "Earlier"
  const d = new Date(ts)
  const startOfDay = (x) =>
    new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime()
  const today = startOfDay(now)
  const dayMs = 86400000
  const dayStart = startOfDay(d)
  const diffDays = Math.round((today - dayStart) / dayMs)

  if (diffDays <= 0) return "Today"
  if (diffDays === 1) return "Yesterday"

  // Week starts Sunday. This week = since the most recent Sunday.
  const thisWeekStart = today - now.getDay() * dayMs
  const lastWeekStart = thisWeekStart - 7 * dayMs
  if (dayStart >= thisWeekStart) return "This Week"
  if (dayStart >= lastWeekStart) return "Last Week"

  // Older → month label; include year if not the current year.
  const sameYear = d.getFullYear() === now.getFullYear()
  return d.toLocaleDateString(undefined, {
    month: "long",
    ...(sameYear ? {} : { year: "numeric" }),
  })
}

// Group an ordered (newest-first) email list into [{ label, items }] sections,
// preserving order and only emitting a separator when the bucket changes.
function groupByDay(emails) {
  const now = new Date()
  const groups = []
  let current = null
  for (const email of emails) {
    const label = bucketOf(email.ts, now)
    if (!current || current.label !== label) {
      current = { label, items: [] }
      groups.push(current)
    }
    current.items.push(email)
  }
  return groups
}

function authHeaders() {
  const token = getToken()
  return token ? { "x-session-token": token } : {}
}

// Render an email's text/html body in a sandboxed iframe so the email's own
// CSS (background colors, layout, the "graphic" card) shows exactly as the
// sender designed it, without leaking into baremail's own dark design system.
// The iframe is sized to its content and re-measures on load + image loads.
function HtmlBody({ html }) {
  const ref = useRef(null)
  const [height, setHeight] = useState(240)

  // Self-sizing wrapper: report scrollHeight up via postMessage. We force
  // links to open in a new tab and break long words so nothing overflows.
  const srcDoc = `<!doctype html><html><head><meta charset="utf-8">
<base target="_blank">
<style>
  html,body{margin:0;padding:0;background:#fff;color:#1a1a1a;
    font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;}
  body{padding:24px;overflow-x:hidden;word-break:break-word;}
  img{max-width:100%;height:auto;}
  a{word-break:break-all;}
</style></head>
<body>${html}
<script>
  function report(){parent.postMessage({__bmHeight:document.documentElement.scrollHeight},"*");}
  window.addEventListener("load",report);
  window.addEventListener("resize",report);
  new ResizeObserver(report).observe(document.body);
  report();
<\/script>
</body></html>`

  useEffect(() => {
    function onMsg(e) {
      const h = e.data && e.data.__bmHeight
      if (typeof h === "number" && h > 0) setHeight(h)
    }
    window.addEventListener("message", onMsg)
    return () => window.removeEventListener("message", onMsg)
  }, [])

  return (
    <iframe
      ref={ref}
      className="body-html"
      title="email"
      sandbox="allow-popups allow-popups-to-escape-sandbox"
      srcDoc={srcDoc}
      style={{ height }}
    />
  )
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
  const [nextPageToken, setNextPageToken] = useState(null)
  const [loadingMore, setLoadingMore] = useState(false)
  const sentinelRef = useRef(null)
  const tokenRef = useRef(null)
  const loadingMoreRef = useRef(false)

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
        setEmails(data.emails || [])
        setNextPageToken(data.nextPageToken || null)
        tokenRef.current = data.nextPageToken || null
        setLoading(false)
      })
      .catch(() => setLoading(false))
  }

  const loadMore = useCallback(() => {
    const token = tokenRef.current
    if (!token || loadingMoreRef.current) return
    loadingMoreRef.current = true
    setLoadingMore(true)
    fetch(`${API}/api/emails?pageToken=${encodeURIComponent(token)}`, {
      headers: authHeaders(),
    })
      .then((r) => r.json())
      .then((data) => {
        setEmails((prev) => [...prev, ...(data.emails || [])])
        setNextPageToken(data.nextPageToken || null)
        tokenRef.current = data.nextPageToken || null
        loadingMoreRef.current = false
        setLoadingMore(false)
      })
      .catch(() => {
        loadingMoreRef.current = false
        setLoadingMore(false)
      })
  }, [])

  useEffect(() => {
    const el = sentinelRef.current
    if (!el) return
    const obs = new IntersectionObserver(
      (entries) => {
        if (entries[0].isIntersecting) loadMore()
      },
      { rootMargin: "400px" }
    )
    obs.observe(el)
    return () => obs.disconnect()
  }, [loadMore, emails.length, nextPageToken])

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
      setNextPageToken(null)
      tokenRef.current = null
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
          {selected.bodyHtml ? (
            <HtmlBody html={selected.bodyHtml} />
          ) : (
            <div className="body">{selected.body || selected.snippet}</div>
          )}
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
          {groupByDay(emails).map((group) => (
            <section key={group.label} className="daygroup">
              <div className="day-sep">{group.label}</div>
              {group.items.map((email) => (
                <div
                  key={email.id}
                  className="mailrow"
                  onClick={() => openEmail(email)}
                >
                  <span className="who">{email.name}</span>
                  <span className="subj">
                    {email.subject}
                    {email.snippet && (
                      <span className="snip">{email.snippet}</span>
                    )}
                  </span>
                  {email.date && <span className="when">{email.date}</span>}
                </div>
              ))}
            </section>
          ))}
          {nextPageToken && (
            <div ref={sentinelRef} className="status">
              {loadingMore ? "Loading more…" : ""}
            </div>
          )}
        </div>
      )}
    </Shell>
  )
}

export default App
