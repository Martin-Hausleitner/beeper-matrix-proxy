#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

EXPECTED_WHATSAPP='!obm0gpZQvhU619bdkYDQ:beeper.local'
EXPECTED_SIGNAL='!uEut5CxNpL9yrnzOl1pl:beeper.local'
STALE_IDS=(
  '!v3p7CvBdPbzyQGKXswFv:beeper.local'
  '!p6AhHep02QYdQfCtQIdQ:beeper.local'
  '!VjUPGUS7o7KZz3nXOB4L:beeper.local'
)

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

ok() {
  printf 'OK: %s\n' "$*"
}

require_file() {
  [[ -f "$1" ]] || fail "missing $1"
}

require_file voice-ai.env
require_file beeper-source-all-chats.db

set -a
source voice-ai.env
set +a

[[ "${BEEPER_MATRIX_PROXY_DISABLE_MATRIX_TO_BEEPER:-}" == "false" ]] ||
  fail "Matrix-to-Beeper is disabled in voice-ai.env"

chat_ids="${BEEPER_MATRIX_PROXY_BEEPER_CHAT_IDS:-}"
[[ "$chat_ids" == *"$EXPECTED_WHATSAPP"* ]] || fail "WhatsApp test chat ID missing"
[[ "$chat_ids" == *"$EXPECTED_SIGNAL"* ]] || fail "Signal test chat ID missing"
for stale in "${STALE_IDS[@]}"; do
  [[ "$chat_ids" != *"$stale"* ]] || fail "stale chat ID still configured: $stale"
done
ok "live env is bidirectional with current WhatsApp/Signal test chat IDs"

stale_sql="$(printf "'%s'," "${STALE_IDS[@]}")"
stale_sql="${stale_sql%,}"
stale_count="$(sqlite3 beeper-source-all-chats.db "select count(*) from portal where beeper_chat_id in ($stale_sql);")"
[[ "$stale_count" == "0" ]] || fail "stale portal rows remain: $stale_count"
allowed_count="$(sqlite3 beeper-source-all-chats.db "select count(*) from portal where beeper_chat_id in ('$EXPECTED_WHATSAPP','$EXPECTED_SIGNAL');")"
[[ "$allowed_count" == "2" ]] || fail "expected 2 active test portals, got $allowed_count"
mapping_orphans="$(sqlite3 beeper-source-all-chats.db "select count(*) from message_mapping m left join portal p on p.beeper_chat_id=m.chat_id where p.beeper_chat_id is null;")"
raw_orphans="$(sqlite3 beeper-source-all-chats.db "select count(*) from beeper_message_raw r left join portal p on p.beeper_chat_id=r.chat_id where p.beeper_chat_id is null;")"
reaction_orphans="$(sqlite3 beeper-source-all-chats.db "select count(*) from reaction_mapping r left join message_mapping m on m.beeper_message_id=r.beeper_message_id where m.beeper_message_id is null;")"
pending_mutations="$(sqlite3 beeper-source-all-chats.db "select count(*) from pending_mutation;")"
queue_rows="$(sqlite3 beeper-source-all-chats.db "select count(*) from queue;")"
kv_stale_count="$(sqlite3 beeper-source-all-chats.db "select count(*) from kv where key like '%!v3p7CvBdPbzyQGKXswFv%' or value like '%!v3p7CvBdPbzyQGKXswFv%' or key like '%!p6AhHep02QYdQfCtQIdQ%' or value like '%!p6AhHep02QYdQfCtQIdQ%' or key like '%!VjUPGUS7o7KZz3nXOB4L%' or value like '%!VjUPGUS7o7KZz3nXOB4L%';")"
[[ "$mapping_orphans" == "0" ]] || fail "message_mapping orphan rows remain: $mapping_orphans"
[[ "$raw_orphans" == "0" ]] || fail "beeper_message_raw orphan rows remain: $raw_orphans"
[[ "$reaction_orphans" == "0" ]] || fail "reaction_mapping orphan rows remain: $reaction_orphans"
[[ "$pending_mutations" == "0" ]] || fail "pending mutation rows remain: $pending_mutations"
[[ "$queue_rows" == "0" ]] || fail "queue rows remain: $queue_rows"
[[ "$kv_stale_count" == "0" ]] || fail "stale chat IDs remain in kv cache rows: $kv_stale_count"
ok "portal DB has 0 stale rows, 2 active test portals, no orphan rows, no pending mutations, no queue rows, and no stale kv cache rows"

launchctl print "gui/$(id -u)/ai.openclaw.beeper-source-voice-ai" 2>/dev/null |
  grep -q 'state = running' || fail "beeper-source LaunchAgent is not running"
ok "beeper-source LaunchAgent is running"

lsof -nP -iTCP:23373 -sTCP:LISTEN >/dev/null 2>&1 ||
  fail "Beeper Desktop API port 23373 is not listening"
lsof -nP -iTCP:18790 -sTCP:LISTEN >/dev/null 2>&1 ||
  fail "OpenClaw gateway port 18790 is not listening"
lsof -nP -iTCP:18792 -sTCP:LISTEN >/dev/null 2>&1 &&
  fail "OpenClaw browser sidecar port 18792 is still listening"
lsof -nP -iTCP:4000 -sTCP:LISTEN >/dev/null 2>&1 &&
  fail "legacy appservice port 4000 is still listening"
ok "ports 23373 and 18790 are listening, and sidecar/legacy ports are free"

curl -fsS http://127.0.0.1:18790/health >/dev/null ||
  fail "OpenClaw gateway health endpoint failed"
ok "OpenClaw gateway health is live"

beeper_env="${BEEPER_ENV_FILE:-$HOME/.openclaw/workspace/.env.beeper}"
require_file "$beeper_env"
set -a
source "$beeper_env"
set +a
: "${BEEPER_BASE_URL:?BEEPER_BASE_URL missing}"
: "${BEEPER_TOKEN:?BEEPER_TOKEN missing}"
curl -fsS -H "Authorization: Bearer $BEEPER_TOKEN" "$BEEPER_BASE_URL/v1/accounts" >/dev/null ||
  fail "Beeper /v1/accounts failed"
ok "Beeper Desktop API responds on /v1/accounts"

disabled_state="$(launchctl print-disabled "gui/$(id -u)" 2>/dev/null || true)"
for label in \
  ai.openclaw.beeper-watchdog \
  ai.openclaw.beeper-chatwoot-forwarder \
  ai.openclaw.matrix-archive-sync \
  ai.hermes.gateway \
  org.openclaw.ensure-vencord-discord \
  org.aaron.autovencordpatch \
  ai.openclaw.skills-sync \
  ai.openclaw.findmysync.receiver \
  com.openclaw.obsidian-notion-sync \
  ai.openclaw.mail-backup-onedrive \
  ai.openclaw.findmy.traccar-bridge \
  ai.openclaw.findmy.geopulse-stack \
  ai.openclaw.findmysync.app \
  ai.openclaw.findmy.owntracks-stack \
  ai.openclaw.findmy.status-publisher \
  ai.openclaw.findmy.export \
  ai.openclaw.findmy.geopulse-bridge \
  ai.openclaw.project-activity \
  ai.openclaw.findmy.traccar-stack \
  ai.openclaw.hermes-backup \
  ai.openclaw.findmy.onedrive-backup \
  ai.openclaw.findmy.owntracks-bridge \
  ai.openclaw.mail-backup \
  ai.servas.aw-whoop-sync \
  com.sessionmanager.cmuxbridge \
  com.voicetypetracker.keycount \
  com.voicetypetracker.typedwatcher \
  com.voicetypetracker.refresh \
  com.mh.door-bridge \
  com.mh.nuki-poll \
  com.mh.apple-health-live-ingest \
  ai.servas.health-data-onedrive-sync \
  com.martin.auto-cleanup \
  com.mh.disk-watchdog \
  com.mh.apple-health-onedrive-sync \
  com.mh.cognitor-tray-watchdog
do
  if ! grep -q "\"$label\" => disabled" <<<"$disabled_state"; then
    fail "$label is not disabled"
  fi
  if launchctl print "gui/$(id -u)/$label" 2>/dev/null | grep -q 'state = running'; then
    fail "$label is still running"
  fi
  if [[ -e "$HOME/Library/LaunchAgents/$label.plist" ]]; then
    fail "$label plist is still in LaunchAgents autostart folder"
  fi
  if compgen -G "$HOME/Library/LaunchAgents/$label.plist.bak*" >/dev/null; then
    fail "$label backup plist is still in LaunchAgents autostart folder"
  fi
done
ok "Beeper watchdog, Chatwoot forwarder, Matrix archive sync, social patchers, skills/findmysync receivers, and legacy Hermes gateway are disabled and removed from LaunchAgents"

allowed_openclaw_launchagents="$(
  find "$HOME/Library/LaunchAgents" -maxdepth 1 -type f \
    \( -name '*openclaw*.plist' -o -name 'ai.openclaw.*.plist' -o -name 'com.openclaw.*.plist' \) \
    -print | sed "s#^$HOME/Library/LaunchAgents/##" | sort
)"
expected_openclaw_launchagents=$'ai.openclaw.beeper-source-voice-ai.plist\nai.openclaw.gateway.plist'
[[ "$allowed_openclaw_launchagents" == "$expected_openclaw_launchagents" ]] ||
  fail "unexpected OpenClaw LaunchAgents remain: $allowed_openclaw_launchagents"
if pgrep -f '/Users/mh/\.openclaw/workspace/aw-importer-whoop' >/dev/null; then
  fail "OpenClaw workspace Whoop importer is still running"
fi
if pgrep -f 'cmux-bridge-forward|vt_keycount|typed_watcher|door_bridge|nuki-poll|apple-health-live-ingest|auto-cleanup|disk-watchdog' >/dev/null; then
  fail "strict-core side process is still running"
fi
xbar_plugin="$HOME/Library/Application Support/xbar/plugins/hermes-openclaw.30s.sh"
[[ ! -e "$xbar_plugin" ]] || fail "xbar Hermes/OpenClaw polling plugin is still active"
if crontab -l 2>/dev/null | grep -Ev '^[[:space:]]*#' | grep -Eq 'gbrain dream|mold-cleanup\.sh'; then
  fail "strict-core OpenClaw/skill cron job is still active"
fi
if crontab -l 2>/dev/null | grep -Ev '^[[:space:]]*(#|$)' >/dev/null; then
  fail "active user crontab entries remain"
fi
ok "only Beeper/OpenClaw core autostarts remain; strict-core side jobs, xbar plugin, and skill cron jobs are disabled"

openclaw_config="$HOME/.openclaw/openclaw.json"
require_file "$openclaw_config"
for plugin in active-memory webhooks telegram bonjour browser phone-control talk-voice file-transfer device-pair; do
  enabled="$(jq -r --arg plugin "$plugin" '.plugins.entries[$plugin].enabled // false' "$openclaw_config")"
  [[ "$enabled" == "false" ]] || fail "OpenClaw plugin $plugin is still enabled"
done
telegram_channel_enabled="$(jq -r '.channels.telegram.enabled // false' "$openclaw_config")"
[[ "$telegram_channel_enabled" == "false" ]] || fail "OpenClaw Telegram channel is still enabled"
openclaw_cron_enabled="$(jq -r '.cron.enabled // false' "$openclaw_config")"
[[ "$openclaw_cron_enabled" == "false" ]] || fail "OpenClaw internal cron is still enabled"
ok "OpenClaw social, sidecar, Telegram, and internal cron config are disabled"

if pgrep -f '/Applications/(Signal|Discord|WhatsApp)\.app' >/dev/null; then
  fail "native Signal/Discord/WhatsApp social side processes are still running"
fi
social_skill_paths="$(
  find "$HOME/.agents/skills" "$HOME/.codex/skills" "$HOME/.openclaw/workspace/skills" "$HOME/.openclaw/skills" \
    -maxdepth 1 \
    \( -iname '*telegram*' -o -iname '*discord*' -o -iname '*whatsapp*' -o -iname '*signal*' \) \
    -print 2>/dev/null | sort
)"
[[ -z "$social_skill_paths" ]] || fail "social skill paths remain: $social_skill_paths"
login_items="$(osascript -e 'tell application "System Events" to get the name of every login item' 2>/dev/null || true)"
for login_item in "xbar"; do
  if grep -Eq "(^|, )$login_item(,|$)" <<<"$login_items"; then
    fail "$login_item is still configured as a Login Item"
  fi
done
ok "native Signal/Discord/WhatsApp social side processes are stopped, social skills are removed, and xbar Login Item is removed"

watchdog="$HOME/Library/Application Support/beeper-watchdog/watchdog.sh"
if [[ -f "$watchdog" ]]; then
  if grep -Eq '(^|[[:space:];|&])(pkill|killall)([[:space:];|&]|$)|(^|[[:space:]])-[0-9]+([[:space:]]|$)' "$watchdog"; then
    fail "Beeper watchdog still contains hard-kill logic"
  fi
  ok "Beeper watchdog has no hard-kill logic"
fi

for legacy in beeper-matrix-proxy sh-vcvm-matrix sh-vcvm-matrix.db sh-vcvm-matrix.db-wal sh-vcvm-matrix.db-shm; do
  [[ ! -e "$legacy" ]] || fail "legacy appservice artifact still active at repo root: $legacy"
done
legacy_bridge_processes="$(
  ps -axo pid=,command= |
    rg -i '(^|[ /])(minibridge|beeper-matrix-proxy|sh-vcvm-matrix)([[:space:]]|$)|mautrix.*bridgev2|BEEPER_LEGACY_APPSERVICE_ENABLE=1' |
    rg -v 'rg -i|validate_openclaw_beeper_bridge' || true
)"
[[ -z "$legacy_bridge_processes" ]] || fail "legacy BIPA/bridgev2 process is still running: $legacy_bridge_processes"
for stale_db in beeper-source-vcvm-test.db beeper-source-vcvm-test.db-shm beeper-source-vcvm-test.db-wal beeper-source-vcvm-test.db.failed-* beeper-source-vcvm-test.db.disabled-stale-* sh-vcvm-matrix.db.backup-e2ee-*; do
  if compgen -G "$stale_db" >/dev/null; then
    fail "stale DB artifact still active at repo root: $stale_db"
  fi
done
if compgen -G 'beeper-source-all-chats.db.backup*' >/dev/null; then
  fail "stale all-chats backup DB artifact still active at repo root"
fi
if compgen -G 'beepersource/beeper-source-all-chats.db*' >/dev/null; then
  fail "package-local beeper-source DB artifact still active under beepersource/"
fi
if compgen -G 'config.yaml.backup-*' >/dev/null; then
  fail "legacy config.yaml backup still active at repo root"
fi
ok "legacy appservice and stale DB artifacts are not active at repo root and port 4000 is free"

grep -q 'BEEPER_LEGACY_APPSERVICE_ENABLE:-0' run-bridge.sh ||
  fail "legacy run-bridge.sh is not gated by BEEPER_LEGACY_APPSERVICE_ENABLE"
grep -q 'legacyAppserviceEnabled' main.go ||
  fail "legacy bridgev2 main.go is not gated by BEEPER_LEGACY_APPSERVICE_ENABLE"
grep -q 'os.Exit(64)' main.go ||
  fail "legacy bridgev2 main.go does not fail closed before startup"
if [[ -f config.yaml ]]; then
  if grep -q 'file:sh-vcvm-matrix\.db' config.yaml; then
    fail "legacy config.yaml still points at root sh-vcvm-matrix.db"
  fi
  if grep -Eq '^[[:space:]]*hostname:[[:space:]]*0\.0\.0\.0[[:space:]]*$' config.yaml; then
    fail "legacy config.yaml still binds all interfaces"
  fi
  if grep -Eq '^[[:space:]]*port:[[:space:]]*4000[[:space:]]*$' config.yaml; then
    fail "legacy config.yaml still uses port 4000"
  fi
fi
ok "legacy BIPA/bridgev2 runner, direct binary, and config are fail-closed and cannot revive port 4000 by default"

if rg -n 'matrix\.onlyapi\.example|!v3p7CvBdPbzyQGKXswFv|!p6AhHep02QYdQfCtQIdQ|!VjUPGUS7o7KZz3nXOB4L|BEEPER_MATRIX_PROXY_DISABLE_MATRIX_TO_BEEPER="\$\{BEEPER_MATRIX_PROXY_DISABLE_MATRIX_TO_BEEPER:-true\}"' \
  run-beeper-source-voice-ai.sh scripts beepersource docs README.md \
  -g '!scripts/validate_openclaw_beeper_bridge.sh' \
  -g '!**/proof/**' >/tmp/openclaw-beeper-bridge-rg.txt; then
  cat /tmp/openclaw-beeper-bridge-rg.txt >&2
  fail "active repo paths still contain stale bridge configuration"
fi
ok "active repo paths contain no stale IDs, old homeserver, or read-only runner default"

if ! rg -n 'matrixToBeeperChatAllowed' beepersource/matrixsource.go >/dev/null; then
  fail "Matrix-to-Beeper path does not enforce configured chat allowlist"
fi
if ! perl -0ne 'exit(!/func \(m \*MatrixClientSource\) matrixToBeeperChatAllowed\(chatID string\) bool \{\s*if len\(m\.cfg\.Beeper\.ChatIDs\) == 0 \{\s*return false\s*\}/s)' beepersource/matrixsource.go; then
  fail "Matrix-to-Beeper path does not fail closed when chat allowlist is empty"
fi
if ! perl -0ne 'exit(!/func \(s \*Service\) matrixToBeeperChatAllowed\(chatID string\) bool \{\s*if len\(s\.cfg\.Beeper\.ChatIDs\) == 0 \{\s*return false\s*\}/s)' beepersource/service.go; then
  fail "Service Matrix-to-Beeper guard does not fail closed when chat allowlist is empty"
fi
for guard in \
  'matrixToBeeperBlocked\(inbound\.ChatID\)' \
  'matrixToBeeperBlocked\(chatID\)'
do
  if ! rg -n "$guard" beepersource/service.go >/dev/null; then
    fail "Service Matrix-to-Beeper operations do not enforce configured chat allowlist"
  fi
done
ok "Matrix-to-Beeper path and service operations enforce configured chat allowlist and fail closed when empty"

if grep -q 'beeper-source-vcvm-test\.db' scripts/run_beeper_source_vcvm_openclaw.sh; then
  fail "alternate OpenClaw runner still defaults to stale test DB"
fi
ok "alternate OpenClaw runner uses the live all-chats DB"

ok "OpenClaw Beeper bridge validation passed"
