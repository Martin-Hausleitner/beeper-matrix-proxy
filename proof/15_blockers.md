# Features with Blockers — Cannot E2E-verify without user action

## BLOCKER: vcvm offline (SSH port 1337 timeout)
**Timestamp:** 2026-05-31T00:51Z  
**Tailscale:** vcvm visible (100.120.120.120, relay "nue", tx/rx active)  
**Port 1337:** Operation timed out (nc confirmed)  

Affected features that require vcvm to be reachable:
- **Paperclip AI orchestrator** (`http://100.120.120.120:3100/api/health`) — systemd service `paperclip.service` was running 2026-05-31 per memory. Needs `systemctl --user status paperclip` on vcvm.
- **LRN vcvm services** (`limit-reset-dashboard` :8799, `limit-reset-tunnel` cloudflared) — need `systemctl --user status` on vcvm.
- **LRN selfcheck** (`scripts/selfcheck.mjs`) — tests public URL from cloudflared tunnel, hangs because vcvm unreachable.
- **AI agent monitor on vcvm** (`~/agent-mon/` on 0.0.0.0:9111) — cannot verify remotely.

**Action needed:** User must bring vcvm back online (reboot/restart SSH daemon on port 1337). Once reachable: `ssh vcvm "systemctl --user status paperclip limit-reset-dashboard limit-reset-tunnel"`.

## BLOCKER: IG Automation — First Login Phone Challenge
Instagram's challenge-response login requires physical phone confirmation.  
`~/Code/ig-control/` ready (Regram IPA + PlayCover + Frida), Bloks UI clicks don't propagate from outside.  
**Action needed:** User must complete first login manually on phone.

## BLOCKER: Door Presence Bridge — Nuki App-Only Pairing
Nuki lock HTTP API doesn't support pairing — must be done via Nuki iOS app.  
`door_bridge.py nuki-pair` ready to auto-save once pairing is done.  
**Action needed:** User must pair Nuki in app, then run `door_bridge.py nuki-pair`.
