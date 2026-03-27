# Baremail

A minimal, distraction-free Gmail client. Read your inbox without the noise.

## What it does

Baremail connects to your Gmail account via Google OAuth and presents your inbox as a clean, simple list. Click an email to read it. That's it.

- Sign in with your Google account
- View your 20 most recent emails
- Read full email content in a distraction-free view
- Read-only access — Baremail never modifies or sends emails

## Tech stack

- **Frontend**: React + Vite + Tailwind CSS
- **Backend**: Node.js + Express
- **Auth**: Google OAuth2
- **API**: Gmail API (read-only)

## Running locally

**Prerequisites**: Node.js, a Google Cloud project with Gmail API enabled, and OAuth2 credentials.

1. Clone the repo
2. Create a `.env` file in `/server` with your credentials:
   ```
   CLIENT_ID=your_google_client_id
   CLIENT_SECRET=your_google_client_secret
   ```
3. Install dependencies and start both servers:
   ```bash
   # Backend
   cd server && npm install && node index.js

   # Frontend (new terminal)
   cd baremail-app && npm install && npm run dev
   ```
4. Open `http://localhost:5173`

## Google OAuth warning

Baremail is currently **not verified by Google**. When you sign in, you will see a warning screen that says *"Google hasn't verified this app"*.

This is expected. To proceed:

1. On the warning screen, click **"Advanced"**
2. Click **"Go to Baremail (unsafe)"**
3. Review the permissions and click **"Continue"**

Baremail only requests **read-only** access to your Gmail. It cannot send, delete, or modify any emails or account data. The unverified warning is a Google requirement for apps that haven't completed their OAuth verification process — it does not mean the app is malicious.

See [PRIVACY/PRIVACY_POLICY.md](PRIVACY/PRIVACY_POLICY.md) for full details on how your data is handled.

## Privacy

Baremail does not store, log, or transmit your email data anywhere. All data stays between your browser and Google's servers. See the [Privacy Policy](PRIVACY/PRIVACY_POLICY.md) for details.

## License

MIT
