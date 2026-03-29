require("dotenv").config();
const express = require("express");
const cors = require("cors");
const crypto = require("crypto");
const { google } = require("googleapis");

const app = express();
const PORT = process.env.PORT || 3001;

app.use(
  cors({
    origin: process.env.CLIENT_URL || "http://localhost:5173",
    credentials: true,
  })
);

// In-memory token store: token -> oauth tokens
const tokenStore = new Map();

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
  if (token) tokenStore.delete(token);
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
    });

    if (!list.data.messages) return res.json([]);

    const emails = await Promise.all(
      list.data.messages.map(async (msg) => {
        const full = await gmail.users.messages.get({
          userId: "me",
          id: msg.id,
          format: "metadata",
          metadataHeaders: ["From", "Subject"],
        });

        const headers = full.data.payload.headers;
        const from = headers.find((h) => h.name === "From")?.value || "";
        const subject = headers.find((h) => h.name === "Subject")?.value || "";

        const nameMatch = from.match(/^"?([^"<]*)"?\s*<(.+)>$/);
        const name = nameMatch ? nameMatch[1].trim() : from;
        const sender = nameMatch ? nameMatch[2] : from;

        return { id: msg.id, name, sender, subject, snippet: full.data.snippet };
      })
    );

    res.json(emails);
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

    let body = "";
    const payload = msg.data.payload;

    if (payload.parts) {
      const textPart = payload.parts.find((p) => p.mimeType === "text/plain");
      if (textPart && textPart.body.data) {
        body = Buffer.from(textPart.body.data, "base64").toString("utf-8");
      }
    } else if (payload.body && payload.body.data) {
      body = Buffer.from(payload.body.data, "base64").toString("utf-8");
    }

    const nameMatch = from.match(/^"?([^"<]*)"?\s*<(.+)>$/);
    const name = nameMatch ? nameMatch[1].trim() : from;
    const sender = nameMatch ? nameMatch[2] : from;

    res.json({ id: msg.data.id, name, sender, subject, to, body, snippet: msg.data.snippet });
  } catch (err) {
    console.error("Fetch email error:", err.message);
    res.status(500).json({ error: "Failed to fetch email" });
  }
});

app.listen(PORT, () => {
  console.log(`Server running on http://localhost:${PORT}`);
});
