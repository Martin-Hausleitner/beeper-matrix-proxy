#!/bin/zsh
set -euo pipefail

repo_dir="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_dir"

export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export CGO_CFLAGS="${CGO_CFLAGS:-"-I/opt/homebrew/opt/libolm/include"}"
export CGO_LDFLAGS="${CGO_LDFLAGS:-"-L/opt/homebrew/opt/libolm/lib -lolm"}"

if [[ -f ".env" ]]; then
  set -a
  source ".env"
  set +a
fi

beeper_env="${BEEPER_ENV_FILE:-$HOME/.openclaw/workspace/.env.beeper}"
if [[ -f "$beeper_env" ]]; then
  set -a
  source "$beeper_env"
  set +a
fi

if [[ -n "${BEEPER_TOKEN:-}" && -z "${BEEPER_ACCESS_TOKEN:-}" ]]; then
  export BEEPER_ACCESS_TOKEN="$BEEPER_TOKEN"
fi
if [[ -n "${BEEPER_BASE_URL:-}" && -z "${BEEPER_MATRIX_PROXY_BEEPER_BASE_URL:-}" ]]; then
  export BEEPER_MATRIX_PROXY_BEEPER_BASE_URL="$BEEPER_BASE_URL"
fi

export MATRIX_ACCESS_TOKEN="${MATRIX_ACCESS_TOKEN:-$(security find-generic-password -s matrix-archive-sync -a "$USER" -w)}"
export BEEPER_MATRIX_PROXY_MATRIX_TOKEN_ENV="${BEEPER_MATRIX_PROXY_MATRIX_TOKEN_ENV:-MATRIX_ACCESS_TOKEN}"
export BEEPER_MATRIX_PROXY_MATRIX_USER_ID="${BEEPER_MATRIX_PROXY_MATRIX_USER_ID:-@openclaw:100.120.120.120}"
export BEEPER_MATRIX_PROXY_MATRIX_HOMESERVER_URL="${BEEPER_MATRIX_PROXY_MATRIX_HOMESERVER_URL:-${LOCAL_MATRIX_HS:-https://vcvm.tail6a40cd.ts.net:3443}}"
export BEEPER_MATRIX_PROXY_MATRIX_INSECURE_TLS="${BEEPER_MATRIX_PROXY_MATRIX_INSECURE_TLS:-${LOCAL_MATRIX_INSECURE_TLS:-true}}"

export BEEPER_MATRIX_PROXY_DISABLE_MATRIX_TO_BEEPER="${BEEPER_MATRIX_PROXY_DISABLE_MATRIX_TO_BEEPER:-false}"
export BEEPER_MATRIX_PROXY_SYNC_MODE="${BEEPER_MATRIX_PROXY_SYNC_MODE:-bidirectional}"
export BEEPER_MATRIX_PROXY_MATRIX_NATIVE_GHOST_SENDERS="${BEEPER_MATRIX_PROXY_MATRIX_NATIVE_GHOST_SENDERS:-true}"
export BEEPER_MATRIX_PROXY_MATRIX_PREFIX_SENDER="${BEEPER_MATRIX_PROXY_MATRIX_PREFIX_SENDER:-false}"
export BEEPER_MATRIX_PROXY_PORTAL_WORKERS="${BEEPER_MATRIX_PROXY_PORTAL_WORKERS:-1}"
export BEEPER_MATRIX_PROXY_MAX_SEND_RPS="${BEEPER_MATRIX_PROXY_MAX_SEND_RPS:-0.2}"
export BEEPER_MATRIX_PROXY_PORTAL_TIMEOUT_SECONDS="${BEEPER_MATRIX_PROXY_PORTAL_TIMEOUT_SECONDS:-180}"
export BEEPER_MATRIX_PROXY_BEEPER_CHAT_IDS="${BEEPER_MATRIX_PROXY_BEEPER_CHAT_IDS:-!obm0gpZQvhU619bdkYDQ:beeper.local,!uEut5CxNpL9yrnzOl1pl:beeper.local}"

binary="${BEEPER_SOURCE_BINARY:-./beeper-source}"
if [[ ! -x "$binary" || "${BEEPER_SOURCE_AUTOBUILD:-1}" != "0" ]]; then
  go build -o "$binary" ./cmd/beeper-source
fi

exec "$binary" \
  -db "${BEEPER_SOURCE_DB:-beeper-source-all-chats.db}" \
  -interval "${BEEPER_SOURCE_INTERVAL:-15s}" \
  "$@"
