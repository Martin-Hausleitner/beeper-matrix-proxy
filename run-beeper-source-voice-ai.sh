#!/bin/zsh
set -euo pipefail

cd "$(dirname "$0")"

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

if [[ -f "voice-ai.env" ]]; then
  set -a
  source "voice-ai.env"
  set +a
fi

if [[ -n "${BEEPER_TOKEN:-}" && -z "${BEEPER_ACCESS_TOKEN:-}" ]]; then
  export BEEPER_ACCESS_TOKEN="$BEEPER_TOKEN"
fi
if [[ -n "${BEEPER_BASE_URL:-}" && -z "${BEEPER_MATRIX_PROXY_BEEPER_BASE_URL:-}" ]]; then
  export BEEPER_MATRIX_PROXY_BEEPER_BASE_URL="$BEEPER_BASE_URL"
fi
if [[ -n "${LOCAL_MATRIX_HS:-}" && -z "${BEEPER_MATRIX_PROXY_MATRIX_HOMESERVER_URL:-}" ]]; then
  export BEEPER_MATRIX_PROXY_MATRIX_HOMESERVER_URL="$LOCAL_MATRIX_HS"
fi
if [[ -n "${LOCAL_MATRIX_INSECURE_TLS:-}" && -z "${BEEPER_MATRIX_PROXY_MATRIX_INSECURE_TLS:-}" ]]; then
  export BEEPER_MATRIX_PROXY_MATRIX_INSECURE_TLS="$LOCAL_MATRIX_INSECURE_TLS"
fi

export BEEPER_MATRIX_PROXY_VOICE_AI_ENABLED="${BEEPER_MATRIX_PROXY_VOICE_AI_ENABLED:-false}"
export BEEPER_MATRIX_PROXY_VOICE_AI_SUMMARY_BASE_URL="${BEEPER_MATRIX_PROXY_VOICE_AI_SUMMARY_BASE_URL:-http://127.0.0.1:1234/v1}"
export BEEPER_MATRIX_PROXY_VOICE_AI_LANGUAGE="${BEEPER_MATRIX_PROXY_VOICE_AI_LANGUAGE:-auto}"
export BEEPER_MATRIX_PROXY_VOICE_AI_MAX_AUDIO_BYTES="${BEEPER_MATRIX_PROXY_VOICE_AI_MAX_AUDIO_BYTES:-26214400}"
export BEEPER_MATRIX_PROXY_VOICE_AI_COMMAND_TIMEOUT_SECONDS="${BEEPER_MATRIX_PROXY_VOICE_AI_COMMAND_TIMEOUT_SECONDS:-300}"
export BEEPER_MATRIX_PROXY_DISABLE_MATRIX_TO_BEEPER="${BEEPER_MATRIX_PROXY_DISABLE_MATRIX_TO_BEEPER:-true}"

binary="${BEEPER_SOURCE_BINARY:-./beeper-source}"
if [[ ! -x "$binary" || "${BEEPER_SOURCE_AUTOBUILD:-1}" != "0" ]]; then
  go build -o "$binary" ./cmd/beeper-source
fi

exec "$binary" \
  -db "${BEEPER_SOURCE_DB:-beeper-source-all-chats.db}" \
  -interval "${BEEPER_SOURCE_INTERVAL:-15s}" \
  "$@"
