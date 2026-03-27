# Privacy Policy

**Last updated: March 27, 2026**

## Overview

Baremail is a minimal Gmail client that lets you read your inbox in a distraction-free interface. This policy explains what data is accessed, how it is used, and what is never done with it.

## What data Baremail accesses

When you sign in with Google, Baremail requests the following OAuth scope:

- `gmail.readonly` — Read-only access to your Gmail inbox

This allows Baremail to fetch and display your emails. It cannot send, delete, draft, or modify emails or any other account data.

## What Baremail does NOT do

- Does not store your emails in any database
- Does not log email content to any file or external service
- Does not share your data with any third party
- Does not track your reading behavior or usage patterns
- Does not retain your OAuth tokens after your session ends (tokens are held in memory only for the duration of your session)

## Data handling

All email data is fetched directly from Google's servers and displayed in your browser. Nothing is persisted beyond your active session. When you close the app or your session ends, no email data remains on the server.

## Google OAuth & the "unverified app" warning

Baremail uses Google OAuth2 for authentication. Because this app has not yet completed Google's OAuth verification process, Google will display a warning when you sign in:

> *"Google hasn't verified this app"*

This warning means Google has not yet reviewed the app's OAuth usage — it does not indicate that the app is harmful. You can safely proceed by clicking **"Advanced"** → **"Go to Baremail (unsafe)"**.

Baremail is working toward completing the Google verification process. Once verified, this warning will no longer appear.

## Third-party services

Baremail communicates only with:

- **Google OAuth2 API** — for authentication
- **Gmail API** — for fetching your emails

No other third-party services are involved.

## Changes to this policy

If this policy changes, the updated version will be committed to this repository with an updated date at the top of this file.

## Contact

If you have questions about this privacy policy, open an issue in the project repository.
