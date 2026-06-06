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
  // renders as designed. A JS pass also strips the solid background-color off
  // full-width layout wrappers so the page bg shows through behind the mail
  // rather than the sender's own container panel. Height is reported up via postMessage so the parent
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
  // One walk over every element. For each we (a) tag real background-image els
  // so they get re-inverted back to true colors and keep their own paint, and
  // (b) strip the solid background-color off everything else so baremail's page
  // bg (--bg) shows through behind the whole mail — no sender container panels,
  // cards, or nested centered tables, regardless of width. Image-painted
  // elements (logos, hero graphics, buttons drawn via background-image) are left
  // alone so they render as designed.
  function cleanup(){
    var els=document.querySelectorAll("body *");
    for(var i=0;i<els.length;i++){
      var el=els[i];
      try{
        var cs=getComputedStyle(el);
        var hasBgImg=cs.backgroundImage && cs.backgroundImage!=="none" && cs.backgroundImage.indexOf("url(")!==-1;
        if(hasBgImg){ el.setAttribute("data-bm-bgimg",""); continue; }
        var bc=cs.backgroundColor;
        var paints=bc && bc!=="transparent" && bc!=="rgba(0, 0, 0, 0)";
        if(paints){
          el.style.setProperty("background-color","transparent","important");
        }
      }catch(e){}
    }
  }
  function report(){parent.postMessage({__bmHeight:document.body.scrollHeight},"*");}
  function pass(){cleanup();report();}
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

// Full-page compose view — a twin of the reader layout (820px column, serif
// heading, mono From/To-style labels). Send POSTs { to, cc, bcc, subject, body,
// inReplyTo, threadId } to /api/send; the backend renders the Markdown body and
// builds the RFC 2822 message. When `reply` is set the To/Subject are prefilled
// and the message threads onto the original conversation.
function Compose({ onClose, reply }) {
  const [to, setTo] = useState(reply?.to || "")
  const [cc, setCc] = useState("")
  const [bcc, setBcc] = useState("")
  const [subject, setSubject] = useState(reply?.subject || "")
  const [body, setBody] = useState("")
  // Cc/Bcc fields stay hidden until asked for, keeping the form minimal.
  const [showCc, setShowCc] = useState(false)
  const [status, setStatus] = useState("idle") // idle | sending | sent | error
  const [error, setError] = useState("")
  const toRef = useRef(null)
  const bodyRef = useRef(null)

  // Replies land focus in the body (recipient is known); new mail in To.
  useEffect(() => {
    if (reply) bodyRef.current?.focus()
    else toRef.current?.focus()
  }, [reply])

  const canSend = to.trim() && body.trim() && status !== "sending"

  const send = useCallback(async () => {
    setStatus("sending")
    setError("")
    try {
      const res = await fetch(`${API}/api/send`, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeaders() },
        body: JSON.stringify({
          to,
          cc,
          bcc,
          subject,
          body,
          inReplyTo: reply?.inReplyTo || "",
          threadId: reply?.threadId || "",
        }),
      })
      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new Error(data.error || "Failed to send")
      }
      setStatus("sent")
      // Brief confirmation, then drop back to the inbox.
      setTimeout(onClose, 700)
    } catch (e) {
      setStatus("error")
      setError(e.message)
    }
  }, [to, cc, bcc, subject, body, reply, onClose])

  // Cmd/Ctrl+Enter sends from anywhere in the form.
  const onFormKey = (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && canSend) {
      e.preventDefault()
      send()
    }
  }

  return (
    <article className="reader compose" onKeyDown={onFormKey}>
      <button className="crumb" onClick={onClose}>
        ← inbox
      </button>
      <h1>{reply ? "Reply" : "New message"}</h1>
      <div className="head">
        <div className="line">
          <label className="label" htmlFor="cmp-to">To</label>
          <input
            id="cmp-to"
            ref={toRef}
            className="compose-input"
            value={to}
            onChange={(e) => setTo(e.target.value)}
            placeholder="name@example.com"
            spellCheck={false}
            autoComplete="off"
          />
          {!showCc && (
            <button
              className="cc-toggle"
              type="button"
              onClick={() => setShowCc(true)}
            >
              Cc Bcc
            </button>
          )}
        </div>
        {showCc && (
          <>
            <div className="line">
              <label className="label" htmlFor="cmp-cc">Cc</label>
              <input
                id="cmp-cc"
                className="compose-input"
                value={cc}
                onChange={(e) => setCc(e.target.value)}
                placeholder="name@example.com"
                spellCheck={false}
                autoComplete="off"
              />
            </div>
            <div className="line">
              <label className="label" htmlFor="cmp-bcc">Bcc</label>
              <input
                id="cmp-bcc"
                className="compose-input"
                value={bcc}
                onChange={(e) => setBcc(e.target.value)}
                placeholder="name@example.com"
                spellCheck={false}
                autoComplete="off"
              />
            </div>
          </>
        )}
        <div className="line">
          <label className="label" htmlFor="cmp-subj">Subject</label>
          <input
            id="cmp-subj"
            className="compose-input"
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
            placeholder="subject"
            spellCheck={false}
            autoComplete="off"
          />
        </div>
      </div>
      <textarea
        ref={bodyRef}
        className="compose-body"
        value={body}
        onChange={(e) => setBody(e.target.value)}
        placeholder="Write your message…  *markdown* supported"
      />
      <div className="compose-actions">
        <div className="compose-meta">
          <span className="compose-hint">markdown · ⌘↵ to send</span>
          {error && <span className="compose-error">{error}</span>}
        </div>
        <button
          className="send-btn"
          type="button"
          onClick={send}
          disabled={!canSend}
        >
          {status === "sending" ? "sending…" : status === "sent" ? "sent" : "send"}
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <path d="M2.3 10.6 20.7 2.5c1-.45 2 .55 1.55 1.55l-8.1 18.4c-.5 1.13-2.15 1-2.47-.2l-1.9-6.85a1 1 0 0 0-.68-.69l-6.85-1.9c-1.2-.33-1.33-1.97-.2-2.47Z" />
          </svg>
        </button>
      </div>
    </article>
  )
}

// Derive up to two initials from a display name (or the email local-part as a
// fallback) for the avatar shown before the photo loads or when no photo exists.
function initialsOf(profile) {
  const src = (profile?.name || profile?.email || "").trim()
  if (!src) return "?"
  const parts = src.split(/[\s@._-]+/).filter(Boolean)
  if (parts.length === 0) return "?"
  const first = parts[0][0] || ""
  const last = parts.length > 1 ? parts[parts.length - 1][0] : ""
  return (first + last).toUpperCase()
}

// Settings view — reuses the reader shell (crumb, serif h1, mono head). baremail
// is deliberately bare, so settings is just the connected Google account and a
// sign-out control; the From/To-style head shows the signed-in identity.
function Settings({ profile, onClose, onSignOut }) {
  return (
    <article className="reader settings">
      <button className="crumb" onClick={onClose}>
        ← inbox
      </button>
      <h1>Settings</h1>
      <div className="head">
        <div className="line">
          <span className="label">Account</span>{" "}
          <b>{profile?.name || "Signed in"}</b>
        </div>
        {profile?.email && (
          <div className="line">
            <span className="label">Email</span> {profile.email}
          </div>
        )}
      </div>
      <div className="settings-row">
        <div className="settings-row-text">
          <span className="settings-row-title">Sign out</span>
          <span className="settings-row-sub">
            End this session on baremail. You can sign back in with Google any time.
          </span>
        </div>
        <button className="reply-btn" type="button" onClick={onSignOut}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
            <polyline points="16 17 21 12 16 7" />
            <line x1="21" y1="12" x2="9" y2="12" />
          </svg>
          Sign out
        </button>
      </div>
    </article>
  )
}

// Topbar profile chip: a round Google avatar that opens a small dropdown with
// the account identity, a Settings entry, and Sign out. The photo comes from
// /api/profile; if it's missing (or 404s on a pre-userinfo-scope session) the
// chip falls back to initials. Closes on outside-click, Escape, or blur.
function Profile({ profile, onSettings, onSignOut }) {
  const [open, setOpen] = useState(false)
  const [imgOk, setImgOk] = useState(true)
  const wrapRef = useRef(null)

  // Outside-click + Escape close the menu.
  useEffect(() => {
    if (!open) return
    function onDown(e) {
      if (wrapRef.current && !wrapRef.current.contains(e.target)) setOpen(false)
    }
    function onKey(e) {
      if (e.key === "Escape") setOpen(false)
    }
    document.addEventListener("mousedown", onDown)
    document.addEventListener("keydown", onKey)
    return () => {
      document.removeEventListener("mousedown", onDown)
      document.removeEventListener("keydown", onKey)
    }
  }, [open])

  const initials = initialsOf(profile)
  const showImg = imgOk && profile?.picture

  return (
    <div className="profile" ref={wrapRef}>
      <button
        className="avatar"
        onClick={() => setOpen((o) => !o)}
        aria-label="account menu"
        aria-haspopup="menu"
        aria-expanded={open}
        title={profile?.email || "account"}
      >
        {showImg ? (
          <img
            src={profile.picture}
            alt=""
            referrerPolicy="no-referrer"
            onError={() => setImgOk(false)}
          />
        ) : (
          <span className="avatar-initials">{initials}</span>
        )}
      </button>
      {open && (
        <div className="profile-menu" role="menu">
          <div className="profile-id">
            <span className="profile-name">{profile?.name || "Signed in"}</span>
            {profile?.email && (
              <span className="profile-email">{profile.email}</span>
            )}
          </div>
          <div className="profile-sep" />
          <button
            className="menu-item"
            role="menuitem"
            onClick={() => {
              setOpen(false)
              onSettings?.()
            }}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <circle cx="12" cy="12" r="3" />
              <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
            </svg>
            Settings
          </button>
          <button
            className="menu-item"
            role="menuitem"
            onClick={() => {
              setOpen(false)
              onSignOut?.()
            }}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
              <polyline points="16 17 21 12 16 7" />
              <line x1="21" y1="12" x2="9" y2="12" />
            </svg>
            Sign out
          </button>
        </div>
      )}
    </div>
  )
}

function Shell({ children, onBrand, onCompose, search, profile, onSettings, onSignOut }) {
  return (
    <>
      <header className="topbar">
        <button className="brand" onClick={onBrand}>
          baremail
        </button>
        <div className="right">
          {profile && (
            <Profile
              profile={profile}
              onSettings={onSettings}
              onSignOut={onSignOut}
            />
          )}
          {search && (
            <div className="search-box">
              <svg
                className="icon"
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <circle cx="11" cy="11" r="7" />
                <line x1="21" y1="21" x2="16.65" y2="16.65" />
              </svg>
              <input
                ref={search.inputRef}
                value={search.value}
                onChange={(e) => search.onChange(e.target.value)}
                onKeyDown={search.onKeyDown}
                placeholder="search mail…"
                spellCheck={false}
                autoComplete="off"
              />
              {search.value ? (
                <button
                  className="clear"
                  onClick={search.onClear}
                  aria-label="clear search"
                >
                  ✕
                </button>
              ) : (
                <span className="slash">/</span>
              )}
            </div>
          )}
          {onCompose && (
            <button
              className="compose-btn"
              onClick={onCompose}
              aria-label="compose"
              title="compose"
            >
              {/* Paper-plane / send arrow, flat — twin of the search magnifier. */}
              <svg
                className="icon"
                width="17"
                height="17"
                viewBox="0 0 24 24"
                fill="currentColor"
                aria-hidden="true"
              >
                <path d="M2.3 10.6 20.7 2.5c1-.45 2 .55 1.55 1.55l-8.1 18.4c-.5 1.13-2.15 1-2.47-.2l-1.9-6.85a1 1 0 0 0-.68-.69l-6.85-1.9c-1.2-.33-1.33-1.97-.2-2.47Z" />
              </svg>
            </button>
          )}
        </div>
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
  const [query, setQuery] = useState("")
  const [composing, setComposing] = useState(false)
  // When set, the compose view opens as a threaded reply (prefilled To/Subject).
  const [replyCtx, setReplyCtx] = useState(null)
  // Google account (name/email/photo) for the topbar profile chip + settings.
  const [profile, setProfile] = useState(null)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [activeIdx, setActiveIdx] = useState(-1)
  const sentinelRef = useRef(null)
  const tokenRef = useRef(null)
  const loadingMoreRef = useRef(false)
  const queryRef = useRef("")
  const searchInputRef = useRef(null)
  const rowRefs = useRef([])

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
        // When authenticated, the search effect performs the initial load
        // (empty query → plain inbox) once `authenticated` flips true.
        if (!data.authenticated) setLoading(false)
      })
      .catch(() => setLoading(false))
  }, [])

  // Load the Google account profile (name/email/photo) once authenticated. A
  // 403 means the session predates the userinfo scopes — leave profile null and
  // the chip renders a neutral fallback; everything else still works.
  useEffect(() => {
    if (!authenticated) {
      setProfile(null)
      return
    }
    fetch(`${API}/api/profile`, { headers: authHeaders() })
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => data && setProfile(data))
      .catch(() => {})
  }, [authenticated])

  // Sign out: drop the session server-side, clear the local token, and reset to
  // the logged-out view. Mirrors the old logout button behavior.
  const signOut = useCallback(() => {
    fetch(`${API}/auth/logout`, { method: "POST", headers: authHeaders() }).finally(() => {
      localStorage.removeItem("bm_token")
      setAuthenticated(false)
      setProfile(null)
      setSelected(null)
      setComposing(false)
      setSettingsOpen(false)
      setEmails([])
    })
  }, [])

  const openSettings = useCallback(() => {
    setSelected(null)
    setComposing(false)
    window.history.pushState({ bmSettings: true }, "")
    setSettingsOpen(true)
  }, [])

  const closeSettings = useCallback(() => {
    if (window.history.state?.bmSettings) window.history.back()
    else setSettingsOpen(false)
  }, [])

  function loadEmails(q = "") {
    setLoading(true)
    queryRef.current = q
    const qs = q ? `?q=${encodeURIComponent(q)}` : ""
    fetch(`${API}/api/emails${qs}`, { headers: authHeaders() })
      .then((r) => r.json())
      .then((data) => {
        // A stale response from a superseded query (user kept typing) must not
        // clobber the current results.
        if (queryRef.current !== q) return
        setEmails(data.emails || [])
        setNextPageToken(data.nextPageToken || null)
        tokenRef.current = data.nextPageToken || null
        setActiveIdx(-1)
        setLoading(false)
      })
      .catch(() => setLoading(false))
  }

  const loadMore = useCallback(() => {
    const token = tokenRef.current
    if (!token || loadingMoreRef.current) return
    loadingMoreRef.current = true
    setLoadingMore(true)
    const q = queryRef.current
    const qs = q ? `&q=${encodeURIComponent(q)}` : ""
    fetch(`${API}/api/emails?pageToken=${encodeURIComponent(token)}${qs}`, {
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

  // Back button while reading/composing: close the overlay instead of leaving
  // the app. Both reader and compose push a history entry, so one popstate
  // handler closes whichever is open.
  useEffect(() => {
    function onPop() {
      setSelected(null)
      setComposing(false)
      setReplyCtx(null)
      setSettingsOpen(false)
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

  // Open the full-page compose view. Push a history entry so Back / Esc returns
  // to the inbox, same as the reader.
  const openCompose = useCallback(() => {
    setReplyCtx(null)
    window.history.pushState({ bmCompose: true }, "")
    setComposing(true)
  }, [])

  // Reply to the open email: build the prefill context (To = original sender,
  // subject Re:-prefixed, threading headers from the message) and open compose.
  const openReply = useCallback((email) => {
    if (!email) return
    const subj = email.subject || ""
    setReplyCtx({
      to: email.sender,
      subject: /^re:/i.test(subj) ? subj : `Re: ${subj}`,
      inReplyTo: email.messageId || "",
      threadId: email.threadId || "",
    })
    window.history.pushState({ bmCompose: true }, "")
    setComposing(true)
  }, [])

  const closeCompose = useCallback(() => {
    setReplyCtx(null)
    if (window.history.state?.bmCompose) window.history.back()
    else setComposing(false)
  }, [])

  // Debounced search: refetch the inbox 250ms after typing stops. An empty
  // query falls back to the plain inbox list. Only runs while authenticated.
  useEffect(() => {
    if (!authenticated) return
    const id = setTimeout(() => loadEmails(query.trim()), 250)
    return () => clearTimeout(id)
  }, [query, authenticated])

  // Keyboard nav. `/` focuses search from anywhere; j/k move the list cursor;
  // Enter opens the active row; Esc closes the reader or blurs/clears search.
  // Typing keys are ignored while focus is in the search input (except Esc).
  useEffect(() => {
    function onKey(e) {
      const inSearch = document.activeElement === searchInputRef.current
      // `/` jumps to search unless already typing in a field or composing.
      if (e.key === "/" && !inSearch && !composing) {
        e.preventDefault()
        searchInputRef.current?.focus()
        return
      }
      if (e.key === "Escape") {
        if (composing) closeCompose()
        else if (selected) closeReader()
        else if (inSearch) searchInputRef.current?.blur()
        return
      }
      // `r` replies to the open email.
      if (e.key === "r" && selected && !composing && !inSearch) {
        e.preventDefault()
        openReply(selected)
        return
      }
      // List nav only on the inbox view, and not while typing a search.
      if (selected || composing || inSearch || emails.length === 0) return
      if (e.key === "j" || e.key === "ArrowDown") {
        e.preventDefault()
        setActiveIdx((i) => Math.min(i + 1, emails.length - 1))
      } else if (e.key === "k" || e.key === "ArrowUp") {
        e.preventDefault()
        setActiveIdx((i) => Math.max(i - 1, 0))
      } else if (e.key === "Enter") {
        if (activeIdx >= 0 && emails[activeIdx]) openEmail(emails[activeIdx])
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [selected, composing, emails, activeIdx, closeReader, closeCompose, openReply])

  // Keep the active row scrolled into view as the cursor moves.
  useEffect(() => {
    if (activeIdx >= 0) rowRefs.current[activeIdx]?.scrollIntoView({ block: "nearest" })
  }, [activeIdx])

  // Props for the search box in the topbar.
  const searchProps = {
    value: query,
    onChange: setQuery,
    onClear: () => {
      setQuery("")
      searchInputRef.current?.focus()
    },
    onKeyDown: (e) => {
      if (e.key === "Enter") searchInputRef.current?.blur()
    },
    inputRef: searchInputRef,
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

  if (settingsOpen) {
    return (
      <Shell
        onBrand={closeSettings}
        profile={profile}
        onSettings={openSettings}
        onSignOut={signOut}
      >
        <Settings profile={profile} onClose={closeSettings} onSignOut={signOut} />
      </Shell>
    )
  }

  if (composing) {
    return (
      <Shell
        onBrand={closeCompose}
        profile={profile}
        onSettings={openSettings}
        onSignOut={signOut}
      >
        <Compose onClose={closeCompose} reply={replyCtx} />
      </Shell>
    )
  }

  if (selected) {
    return (
      <Shell
        onBrand={closeReader}
        onCompose={openCompose}
        profile={profile}
        onSettings={openSettings}
        onSignOut={signOut}
      >
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
          <div className="reader-actions">
            <button className="reply-btn" type="button" onClick={() => openReply(selected)}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <polyline points="9 17 4 12 9 7" />
                <path d="M20 18v-2a4 4 0 0 0-4-4H4" />
              </svg>
              reply
            </button>
          </div>
        </article>
      </Shell>
    )
  }

  // Map each email id to its flat index so keyboard nav (which works off the
  // flat list) can mark the right row active inside the day-grouped render.
  const flatIndex = new Map(emails.map((e, i) => [e.id, i]))

  return (
    <Shell
      onBrand={() => setSelected(null)}
      onCompose={openCompose}
      search={searchProps}
      profile={profile}
      onSettings={openSettings}
      onSignOut={signOut}
    >
      {emails.length === 0 ? (
        <div className="empty">
          {queryRef.current ? "No matches." : "Inbox empty."}
        </div>
      ) : (
        <div className="maillist">
          {groupByDay(emails).map((group) => (
            <section key={group.label} className="daygroup">
              <div className="day-sep">{group.label}</div>
              {group.items.map((email) => {
                const idx = flatIndex.get(email.id)
                return (
                <div
                  key={email.id}
                  ref={(el) => (rowRefs.current[idx] = el)}
                  className={`mailrow ${email.unread ? "is-unread" : "is-read"}${
                    idx === activeIdx ? " is-active" : ""
                  }`}
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
                )
              })}
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
