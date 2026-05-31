#!/usr/bin/env bash
# Schneller Beeper-Sender: direkt über v1 API, kein npx/MCP-Overhead
# Usage: beeper-send.sh <chatID> <text>
set -euo pipefail

CHAT_ID="${1:?Usage: beeper-send.sh <chatID> <text>}"
TEXT="${2:?Usage: beeper-send.sh <chatID> <text>}"

TOKEN=$(python3 -c "
import json, glob, sys
files = glob.glob('$HOME/.beeper-mcp-auth/mcp-remote-0.0.2/*_tokens.json')
if not files: sys.exit(1)
d = json.load(open(files[0]))
print(d.get('access_token') or d.get('accessToken') or '')
" 2>/dev/null) || { echo "ERROR: no token"; exit 1; }

RESULT=$(curl -sf --max-time 10 -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"text\": $(python3 -c "import json,sys; print(json.dumps(sys.argv[1]))" "$TEXT")}" \
  "http://127.0.0.1:23373/v1/chats/${CHAT_ID}/messages" 2>/dev/null) || {
  echo "ERROR: send failed (Beeper Desktop down or chat not found)"
  exit 1
}
echo "$RESULT"
