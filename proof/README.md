# E2E Proof — Session 2026-05-29 / 2026-05-31

Beeper infrastructure fixes implemented and verified end-to-end.

## Features

### 1. sh-vcvm-matrix Bridge: Permanently Deleted
**File:** `01_bridge_deleted_whoami.txt`  
`bbctl whoami` output confirms `sh-vcvm-matrix` is no longer listed under Bridges.  
Deleted via `DELETE https://api.beeper.com/bridge/sh-vcvm-matrix` → HTTP 204.

### 2. WhatsApp Send (chatID 91745) — E2E Verified
**File:** `02_wa_signal_send_e2e.json`, `03_messages_verified.json`  
MCP `send_message` → Beeper Desktop API → WhatsApp bridge → message confirmed in chat.  
Proof message: `[E2E-PROOF] WhatsApp send verified 2026-05-31T01:34:16Z` (msgID 357049)

### 3. Signal Send (chatID 120600) — E2E Verified
**File:** `02_wa_signal_send_e2e.json`, `03_messages_verified.json`  
MCP `send_message` → Beeper Desktop API → Signal bridge → message confirmed in chat.  
Proof message: `[E2E-PROOF] Signal send verified 2026-05-31T01:34:16Z` (msgID 357048)

### 4. matrix-archive-sync Rescheduled to 03:00
**File:** `04_matrix_archive_sync_plist.xml`, `05_matrix_sync_aw_check.sh`  
- Was: `StartInterval 3600` (every hour, RunAtLoad=true)  
- Now: `StartCalendarInterval Hour=3 Minute=0` (once daily at 03:00, RunAtLoad=false)  
- Added: ActivityWatch AFK check — aborts if user was active on Mac in last 5 min  
- Root cause: hourly sync was flooding Beeper homeserver → Signal/WhatsApp instability  
- WHOOP sleep data confirms 02:00–09:00 = zero active Mac usage → 03:00 is safe

### 5. Beeper Desktop Watchdog
**File:** `06_beeper_watchdog_plist.xml`, `07_beeper_watchdog_script.sh`, `08_beeper_watchdog_log.txt`, `09_watchdog_liverun.txt`  
- LaunchAgent `ai.openclaw.beeper-watchdog` runs every 5 minutes  
- Checks: process alive → port 23373 open → API responds in <3s  
- If slow 2× in a row: auto-restarts Beeper Desktop  
- Live log shows consistent <20ms response after Beeper Desktop restart  
- Root cause fixed: internal Beeper Desktop state buildup caused 20-30s send latency

## Instability Root Cause (Diagnosed)

The `sh-vcvm-matrix` bridgev2 had `KeepAlive=true` and was started/stopped multiple
times during the session. Each restart triggered a full `ChatResync` (3000+ concurrent
PUT requests to `matrix.beeper.com/_hungryserv/martinhltr`), overloading the shared
account homeserver and causing Signal/WhatsApp to drop or time out.

Fix: Bridge permanently deleted server-side. Cannot respawn.

### 6. Direct Beeper API Sender (beeper-send.sh)
**File:** `10_direct_api_sender_e2e.json`, `12_beeper_send_direct_api.sh`  
Root cause of variable 5–30s latency: `npx @beeper/mcp-remote` waits for cloud ACK.  
Fix: `POST http://127.0.0.1:23373/v1/chats/{chatID}/messages` directly — ~0.5s total for WA+Signal.  
E2E: WA pendingMessageID `~beeper-mautrix-go_..._24`, Signal `~beeper-mautrix-go_..._25` confirmed.

### 7. Docker Disk Full → chatwoot-forwarder Fixed
**File:** `11_chatwoot_forwarder_docker_fix.json`  
- `openclaw-chatwoot-local-rails-1` container overlay disk was 100% full (36.7G/36.7G)  
- Fix: `docker volume prune -f` (537MB) + `docker rmi postgres:latest alpine:latest` (~500MB)  
- Result: 359MB free, forwarder returns `{"ok":true}` again  
- Runs every 2 min via `ai.openclaw.beeper-chatwoot-forwarder` LaunchAgent

### 8. Limit Reset Notifier — Local Stack E2E
**File:** `13_lrn_local_stack_e2e.json`, `16_lrn_kpi_fresh.json`  
- local-routine: 10/10 ✅ (00:49Z)  
- Grafana :3300 `ok`, 1437 AI series (:9109), 19 agent series (:9111)  
- `collect.mjs` ran: 1320 KPI-Serien, 31 Tage, Claude+Codex  
- **BLOCKER vcvm:** `limit-reset-dashboard`, `limit-reset-tunnel`, `selfcheck` require SSH to vcvm (port 1337 timeout). See `15_blockers.md`.

### 9. Session Manager (csm) — E2E Verified
**File:** `14_csm_session_list.txt`  
`csm list` returns 18 local sessions with project, title, agent, recency. ✅

### 10. Blockers Documented
**File:** `15_blockers.md`  
- vcvm offline → Paperclip, LRN vcvm services, selfcheck blocked  
- IG Automation → first phone login required  
- Door Presence Bridge → Nuki app pairing required
