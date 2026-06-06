require("dotenv").config();
const express = require("express");
const cors = require("cors");
const crypto = require("crypto");
const path = require("path");
const fs = require("fs");
const { google } = require("googleapis");

const app = express();
const PORT = process.env.PORT || 3001;

app.use(
  cors({
    origin: process.env.CLIENT_URL || "http://localhost:5173",
    credentials: true,
  })
);

// Token store: sessionToken -> oauth tokens. Persisted to disk so sessions
// survive a server restart/deploy (pm2 restart would otherwise wipe everyone).
// SESSIONS_FILE defaults next to this file; keep it out of git (gitignored).
const SESSIONS_FILE =
  process.env.SESSIONS_FILE || path.join(__dirname, "sessions.json");

function loadSessions() {
  try {
    const raw = fs.readFileSync(SESSIONS_FILE, "utf-8");
    return new Map(Object.entries(JSON.parse(raw)));
  } catch {
    return new Map();
  }
}

// The sessions file holds live OAuth access/refresh tokens. Keep both the file
// and its parent dir owner-only (0600 / 0700) so other accounts on the host
// can't read them; tighten an existing file's mode on every boot too.
function secureSessionsPath() {
  try {
    fs.mkdirSync(path.dirname(SESSIONS_FILE), { recursive: true, mode: 0o700 });
    fs.chmodSync(path.dirname(SESSIONS_FILE), 0o700);
  } catch {}
  try {
    fs.chmodSync(SESSIONS_FILE, 0o600);
  } catch {}
}
secureSessionsPath();

const tokenStore = loadSessions();

// Debounced write so rapid mutations coalesce into one disk flush.
let saveTimer = null;
function saveSessions() {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => {
    const obj = Object.fromEntries(tokenStore);
    // Write to a temp file with owner-only mode, then atomically rename into
    // place. This guarantees the tokens file is never momentarily world-readable
    // and a crash mid-write can't truncate the real file.
    const tmp = `${SESSIONS_FILE}.${process.pid}.tmp`;
    fs.writeFile(tmp, JSON.stringify(obj), { mode: 0o600 }, (err) => {
      if (err) {
        console.error("Failed to persist sessions:", err.message);
        return;
      }
      fs.rename(tmp, SESSIONS_FILE, (rErr) => {
        if (rErr) console.error("Failed to persist sessions:", rErr.message);
      });
    });
  }, 200);
}

// Format an email Date header into a short relative label (e.g. "3h", "2d").
function relTime(dateHeader) {
  if (!dateHeader) return "";
  const then = new Date(dateHeader);
  if (isNaN(then)) return "";
  // Today → clock time (11:42 AM); older → month/day (Jun 4).
  const now = new Date();
  const sameDay =
    then.getFullYear() === now.getFullYear() &&
    then.getMonth() === now.getMonth() &&
    then.getDate() === now.getDate();
  if (sameDay) {
    return then.toLocaleTimeString(undefined, {
      hour: "numeric",
      minute: "2-digit",
    });
  }
  return then.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function tsOf(dateHeader) {
  const t = new Date(dateHeader).getTime();
  return isNaN(t) ? 0 : t;
}

function makeOAuthClient() {
  return new google.auth.OAuth2(
    process.env.CLIENT_ID,
    process.env.CLIENT_SECRET,
    process.env.REDIRECT_URI || "http://localhost:3001/auth/google/callback"
  );
}

// --- Auth Routes ---

app.get("/auth/google", (req, res) => {
  const oauth2Client = makeOAuthClient();
  const url = oauth2Client.generateAuthUrl({
    access_type: "offline",
    scope: ["https://www.googleapis.com/auth/gmail.readonly"],
  });
  res.redirect(url);
});

app.get("/auth/google/callback", async (req, res) => {
  const { code } = req.query;
  if (!code) return res.status(400).send("Missing auth code");

  try {
    const oauth2Client = makeOAuthClient();
    const { tokens } = await oauth2Client.getToken(code);

    const sessionToken = crypto.randomBytes(32).toString("hex");
    tokenStore.set(sessionToken, tokens);
    saveSessions();

    const clientUrl = process.env.CLIENT_URL || "http://localhost:5173";
    res.redirect(`${clientUrl}?token=${sessionToken}`);
  } catch (err) {
    console.error("OAuth error:", err.message);
    res.status(500).send("Authentication failed");
  }
});

app.get("/auth/status", (req, res) => {
  const token = req.headers["x-session-token"];
  res.json({ authenticated: !!(token && tokenStore.has(token)) });
});

app.get("/auth/logout", (req, res) => {
  const token = req.headers["x-session-token"];
  if (token && tokenStore.delete(token)) saveSessions();
  res.json({ ok: true });
});

// --- Middleware ---

function requireAuth(req, res, next) {
  const token = req.headers["x-session-token"];
  if (!token || !tokenStore.has(token)) {
    return res.status(401).json({ error: "Not authenticated" });
  }
  const oauth2Client = makeOAuthClient();
  oauth2Client.setCredentials(tokenStore.get(token));
  // When Google rotates the access token (the refresh_token mints a fresh
  // access_token after ~1h), persist the merged credentials so the on-disk
  // session stays valid across restarts instead of going stale.
  oauth2Client.on("tokens", (fresh) => {
    tokenStore.set(token, { ...tokenStore.get(token), ...fresh });
    saveSessions();
  });
  req.oauth2Client = oauth2Client;
  next();
}

// --- Email Routes ---

app.get("/api/emails", requireAuth, async (req, res) => {
  try {
    const gmail = google.gmail({ version: "v1", auth: req.oauth2Client });
    const list = await gmail.users.messages.list({
      userId: "me",
      maxResults: 20,
      pageToken: req.query.pageToken || undefined,
    });

    if (!list.data.messages) return res.json({ emails: [], nextPageToken: null });

    const emails = await Promise.all(
      list.data.messages.map(async (msg) => {
        const full = await gmail.users.messages.get({
          userId: "me",
          id: msg.id,
          format: "metadata",
          metadataHeaders: ["From", "Subject", "Date"],
        });

        const headers = full.data.payload.headers;
        const from = headers.find((h) => h.name === "From")?.value || "";
        const subject = headers.find((h) => h.name === "Subject")?.value || "";
        const dateHeader = headers.find((h) => h.name === "Date")?.value || "";

        const nameMatch = from.match(/^"?([^"<]*)"?\s*<(.+)>$/);
        const name = nameMatch ? nameMatch[1].trim() : from;
        const sender = nameMatch ? nameMatch[2] : from;

        const unread = full.data.labelIds?.includes("UNREAD") || false;

        return { id: msg.id, name, sender, subject, snippet: full.data.snippet, date: relTime(dateHeader), ts: tsOf(dateHeader), unread };
      })
    );

    res.json({ emails, nextPageToken: list.data.nextPageToken || null });
  } catch (err) {
    console.error("Fetch emails error:", err.message);
    res.status(500).json({ error: "Failed to fetch emails" });
  }
});

app.get("/api/emails/:id", requireAuth, async (req, res) => {
  try {
    const gmail = google.gmail({ version: "v1", auth: req.oauth2Client });
    const msg = await gmail.users.messages.get({
      userId: "me",
      id: req.params.id,
      format: "full",
    });

    const headers = msg.data.payload.headers;
    const from = headers.find((h) => h.name === "From")?.value || "";
    const subject = headers.find((h) => h.name === "Subject")?.value || "";
    const to = headers.find((h) => h.name === "To")?.value || "";

    // Walk the MIME tree and collect the first text/plain and text/html bodies.
    // GitHub-style notifications carry the rich "graphic" card in text/html,
    // so we surface that when present and keep plain text as a fallback.
    const decode = (d) => Buffer.from(d, "base64").toString("utf-8");
    let body = "";
    let bodyHtml = "";
    const walk = (part) => {
      if (!part) return;
      const mime = part.mimeType || "";
      if (mime === "text/plain" && !body && part.body?.data) {
        body = decode(part.body.data);
      } else if (mime === "text/html" && !bodyHtml && part.body?.data) {
        bodyHtml = decode(part.body.data);
      }
      if (part.parts) part.parts.forEach(walk);
    };
    walk(msg.data.payload);

    const nameMatch = from.match(/^"?([^"<]*)"?\s*<(.+)>$/);
    const name = nameMatch ? nameMatch[1].trim() : from;
    const sender = nameMatch ? nameMatch[2] : from;

    res.json({ id: msg.data.id, name, sender, subject, to, body, bodyHtml, snippet: msg.data.snippet });
  } catch (err) {
    console.error("Fetch email error:", err.message);
    res.status(500).json({ error: "Failed to fetch email" });
  }
});

// --- Static frontend (single-origin deploy) ---
// Serve the built React app. STATIC_DIR defaults to ../baremail-app/dist.
const STATIC_DIR =
  process.env.STATIC_DIR || path.join(__dirname, "..", "baremail-app", "dist");
app.use(express.static(STATIC_DIR));

// SPA fallback: any non-API GET serves index.html
app.get(/^(?!\/(api|auth)\/).*/, (req, res) => {
  res.sendFile(path.join(STATIC_DIR, "index.html"));
});

app.listen(PORT, () => {
  console.log(`Server running on http://localhost:${PORT}`);
});
