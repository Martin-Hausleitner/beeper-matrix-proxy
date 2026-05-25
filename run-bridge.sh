#!/bin/zsh
set -euo pipefail

export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export CGO_CFLAGS="-I/opt/homebrew/opt/libolm/include"
export CGO_LDFLAGS="-L/opt/homebrew/opt/libolm/lib -lolm"
if [[ -f ".env" ]]; then
  set -a
  source ".env"
  set +a
fi
export LOCAL_MATRIX_INITIAL_BACKFILL_LIMIT="${LOCAL_MATRIX_INITIAL_BACKFILL_LIMIT:-0}"
export BEEPER_MATRIX_PROXY_DIR="${BEEPER_MATRIX_PROXY_DIR:-$PWD}"
if [[ "$BEEPER_MATRIX_PROXY_DIR" != "$PWD" && -f "$BEEPER_MATRIX_PROXY_DIR/.env" ]]; then
  set -a
  source "$BEEPER_MATRIX_PROXY_DIR/.env"
  set +a
fi
export BEEPER_MATRIX_PROXY_BINARY="${BEEPER_MATRIX_PROXY_BINARY:-$BEEPER_MATRIX_PROXY_DIR/beeper-matrix-proxy}"
export BEEPER_BRIDGE_NAME="${BEEPER_BRIDGE_NAME:-sh-vcvm-matrix}"
export BEEPER_MATRIX_PROXY_AUTOBUILD="${BEEPER_MATRIX_PROXY_AUTOBUILD:-1}"
export BEEPER_BBCTL="${BEEPER_BBCTL:-$(command -v bbctl || true)}"
if [[ -z "$BEEPER_BBCTL" && -x "$HOME/.local/bin/bbctl" ]]; then
  export BEEPER_BBCTL="$HOME/.local/bin/bbctl"
fi
if [[ -z "$BEEPER_BBCTL" ]]; then
  echo "bbctl not found. Set BEEPER_BBCTL to the bbctl binary path." >&2
  exit 127
fi
# Synapse advertises a very high media limit, but the HTTPS/proxy path currently
# returns HTTP 413 for larger uploads. Keep Beeper's room_features conservative.
export LOCAL_MATRIX_MAX_UPLOAD_SIZE="${LOCAL_MATRIX_MAX_UPLOAD_SIZE:-4194304}"

cd "$BEEPER_MATRIX_PROXY_DIR"
if [[ "$BEEPER_MATRIX_PROXY_AUTOBUILD" != "0" && ! -x "$BEEPER_MATRIX_PROXY_BINARY" ]]; then
  go build -o "$BEEPER_MATRIX_PROXY_BINARY" .
fi
exec "$BEEPER_BBCTL" run \
  --type bridgev2 \
  --local-dev \
  --no-override-config \
  --custom-startup-command "$BEEPER_MATRIX_PROXY_BINARY" \
  "$BEEPER_BRIDGE_NAME"
