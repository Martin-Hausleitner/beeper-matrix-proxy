#!/usr/bin/env bash
# Beeper Desktop Watchdog — startet neu wenn API tot oder intern hängend
set -euo pipefail

LOG="$HOME/Library/Logs/beeper-watchdog.log"
STATE_DIR="$HOME/Library/Application Support/beeper-watchdog"
SLOW_FILE="$STATE_DIR/slow_count"
MAX_RESPONSE_MS=3000   # >3s auf /v0/mcp → hängend

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') $*" | tee -a "$LOG"; }

restart_beeper() {
  log "RESTART: $1"
  echo 0 > "$SLOW_FILE"
  pkill -9 -f 'Beeper Desktop' 2>/dev/null || true
  sleep 4
  open -a "Beeper Desktop"
  for i in $(seq 1 25); do
    if lsof -nP -iTCP:23373 -sTCP:LISTEN >/dev/null 2>&1; then
      log "UP: Beeper Desktop wieder offen nach ${i}s"
      return 0
    fi
    sleep 1
  done
  log "WARN: Port 23373 nach 25s immer noch nicht offen"
}

# Token aus mcp-remote OAuth-Cache lesen
ACCESS_TOKEN=$(python3 -c "
import json, glob, sys
files = glob.glob('$HOME/.beeper-mcp-auth/mcp-remote-0.0.2/*_tokens.json')
if not files: sys.exit(1)
d = json.load(open(files[0]))
print(d.get('access_token') or d.get('accessToken') or '')
" 2>/dev/null || echo "")

# 1. Prozess läuft?
if ! pgrep -x "Beeper Desktop" >/dev/null 2>&1; then
  restart_beeper "Prozess nicht gefunden"
  exit 0
fi

# 2. Port offen?
if ! lsof -nP -iTCP:23373 -sTCP:LISTEN >/dev/null 2>&1; then
  restart_beeper "Port 23373 nicht offen"
  exit 0
fi

# 3. API-Antwortzeit messen (GET /v0/mcp mit Auth — antwortet sofort wenn gesund)
RESPONSE_MS=$(curl -s --max-time 5 -X GET \
  -o /dev/null -w '%{time_total}' \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  "http://127.0.0.1:23373/v0/mcp" 2>/dev/null | \
  python3 -c "import sys; print(int(float(sys.stdin.read())*1000))" 2>/dev/null || echo 9999)

if (( RESPONSE_MS >= MAX_RESPONSE_MS )); then
  SLOW=$(cat "$SLOW_FILE" 2>/dev/null || echo 0)
  SLOW=$(( SLOW + 1 ))
  echo "$SLOW" > "$SLOW_FILE"
  log "SLOW: API antwortet in ${RESPONSE_MS}ms (Zähler: ${SLOW}/2)"
  if (( SLOW >= 2 )); then
    restart_beeper "API hängend ${RESPONSE_MS}ms — 2x in Folge"
  fi
else
  echo 0 > "$SLOW_FILE"
  log "OK: API antwortet in ${RESPONSE_MS}ms"
fi
