import { useState, useEffect, useRef, useCallback } from "react"

const API = import.meta.env.VITE_API_URL || ""

// localStorage (not sessionStorage) so the session survives a full page
// refresh and tab close, not just an in-tab navigation.
function getToken() {
  return localStorage.getItem("bm_token")
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
// sender designed it, without leaking into baremail's own design system.
// The iframe is sized to its content and re-measures on load + image loads.
function HtmlBody({ html }) {
  const ref = useRef(null)
  const [height, setHeight] = useState(240)

  // Force the email dark with full color inversion — the same technique Outlook /
  // Windows Mail use to force-dark email. A single `filter: invert(1)
  // hue-rotate(180deg)` on the root flips every light surface dark regardless of
  // how the sender set it (inline style, bgcolor attribute, embedded <style>, or
  // !important), so there is no white background to chase. The html/body
  // backgrounds are left transparent so baremail's own page bg (--bg) shows
  // through behind the mail rather than a solid dark block.
  // Media that already carries true colors (images, video, canvas, svg, and any
  // element painted with a background-image) is inverted a second time so it
  // renders as designed. Height is reported up via postMessage so the parent
  // sizes the iframe to exact content; we never clip overflow (that collapses
  // scrollHeight and cuts the body off).
  const srcDoc = `<!doctype html><html><head><meta charset="utf-8">
<base target="_blank">
<style>
  html{filter:invert(1) hue-rotate(180deg);background:transparent;}
  html,body{margin:0;padding:0;background:transparent;
    font-family:'Inter',-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;}
  body{padding:0;overflow-x:hidden;word-break:break-word;
    -webkit-font-smoothing:antialiased;}
  /* Re-invert real media so photos and logos keep their true colors. The
     [data-bm-bgimg] hook is added in JS for elements painted via background-image. */
  img,video,canvas,svg,picture,image,[data-bm-bgimg]{
    filter:invert(1) hue-rotate(180deg);
  }
  img{max-width:100%;height:auto;}
</style></head>
<body>${html}
<script>
  // Tag any element that paints a real background-image so it gets re-inverted
  // back to its true colors (the root filter would otherwise negate it).
  function markBgImages(){
    var els=document.querySelectorAll("body *");
    for(var i=0;i<els.length;i++){
      try{
        var bi=getComputedStyle(els[i]).backgroundImage;
        if(bi && bi!=="none" && bi.indexOf("url(")!==-1) els[i].setAttribute("data-bm-bgimg","");
      }catch(e){}
    }
  }
  function report(){parent.postMessage({__bmHeight:document.body.scrollHeight},"*");}
  function pass(){markBgImages();report();}
  window.addEventListener("load",pass);
  window.addEventListener("resize",report);
  new ResizeObserver(report).observe(document.body);
  // Late images can change height; re-measure as each loads.
  document.querySelectorAll("img").forEach(function(im){im.addEventListener("load",report)});
  pass();
<\/script>
</body></html>`

  useEffect(() => {
    function onMsg(e) {
      // The email runs in an opaque-origin sandbox, so it can postMessage
      // arbitrary payloads. Only trust a height message that actually came
      // from THIS iframe's window and is a plain positive number.
      if (e.source !== ref.current?.contentWindow) return
      const h = e.data && e.data.__bmHeight
      if (typeof h === "number" && isFinite(h) && h > 0 && h < 100000) {
        setHeight(h)
      }
    }
    window.addEventListener("message", onMsg)
    return () => window.removeEventListener("message", onMsg)
  }, [])

  return (
    <iframe
      ref={ref}
      className="body-html"
      title="email"
      scrolling="no"
      sandbox="allow-scripts allow-popups"
      srcDoc={srcDoc}
      style={{ height: `${height}px` }}
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
      localStorage.setItem("bm_token", urlToken)
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
    // Push a history entry so the browser Back button returns to the inbox
    // (this in-app view) instead of unwinding past the app to the OAuth login.
    window.history.pushState({ bmReader: true }, "")
    fetch(`${API}/api/emails/${email.id}`, { headers: authHeaders() })
      .then((r) => r.json())
      .then((data) => setSelected(data))
  }

  // Back button while reading: close the reader instead of leaving the app.
  useEffect(() => {
    function onPop() {
      setSelected(null)
    }
    window.addEventListener("popstate", onPop)
    return () => window.removeEventListener("popstate", onPop)
  }, [])

  // Closing the reader via an in-app control (← inbox, brand). Walk the
  // history entry back so the Back button doesn't have a stale reader step.
  const closeReader = useCallback(() => {
    if (window.history.state?.bmReader) window.history.back()
    else setSelected(null)
  }, [])

  function signOut() {
    fetch(`${API}/auth/logout`, { headers: authHeaders() }).then(() => {
      localStorage.removeItem("bm_token")
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
      <Shell onBrand={closeReader} onSignOut={signOut}>
        <article className="reader">
          <button className="crumb" onClick={closeReader}>
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
                  className={`mailrow ${email.unread ? "is-unread" : "is-read"}`}
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
