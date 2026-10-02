# Local Chatwoot

Local Docker Chatwoot for OpenClaw/Hermes integration testing.

## Services

- Chatwoot: <http://127.0.0.1:3080>
- Mailpit UI: <http://127.0.0.1:8025>
- Mailpit SMTP: `127.0.0.1:1025`

Secrets live outside the repo:

`~/Library/Application Support/openclaw-chatwoot-local/.env`

## Commands

```bash
cd infra/chatwoot-local
./start.sh
```

The admin login is stored in the private env file, not in this repository:

`~/Library/Application Support/openclaw-chatwoot-local/.env`

## Local E2E Proof

Verified locally with Safari/Computer Use:

- Chatwoot dashboard loads at <http://127.0.0.1:3080>
- Mailpit loads at <http://127.0.0.1:8025>
- Local Mailpit SMTP receives a test message addressed to the Chatwoot admin.
- Chatwoot has two local test channels:
  - `OpenClaw Beeper Matrix Bridge`
  - `OpenClaw Local Email Bridge`
- Chatwoot can send a reply through local SMTP to Mailpit when the email channel
  uses Mailpit without SMTP authentication.

Evidence screenshots are kept outside git under:

`<private-evidence-folder>/chatwoot-local`

## Scope

This local Chatwoot is the operator UI. It is not the canonical archive. Matrix,
Beeper, email, GitHub, and media remain archived by the local sync/archive
stack first, then mirrored into Chatwoot when a connector is enabled.

Do not use this stack to message real contacts during tests. WhatsApp and Signal
tests must use explicit local/test rooms or already-approved test groups only.
