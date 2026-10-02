#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

env_file="${CHATWOOT_ENV_FILE:-$HOME/Library/Application Support/openclaw-chatwoot-local/.env}"

if [[ ! -f "$env_file" ]]; then
  echo "Missing private env file: $env_file" >&2
  echo "Create it from infra/chatwoot-local/.env.example with private secrets." >&2
  exit 1
fi

docker compose --env-file "$env_file" up -d postgres redis mailpit
docker compose --env-file "$env_file" run --rm rails bundle exec rails db:chatwoot_prepare
docker compose --env-file "$env_file" up -d rails sidekiq

echo "Chatwoot: http://127.0.0.1:${CHATWOOT_PORT:-3080}"
echo "Mailpit:  http://127.0.0.1:${CHATWOOT_MAILPIT_WEB_PORT:-8025}"
