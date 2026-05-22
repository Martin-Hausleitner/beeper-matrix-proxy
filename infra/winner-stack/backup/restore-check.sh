#!/usr/bin/env bash
set -euo pipefail

ENV_FILE="${1:-./backup.env}"
if [[ -f "$ENV_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$ENV_FILE"
fi

if [[ -z "${RESTIC_REPOSITORY:-}" ]]; then
  echo "Missing RESTIC_REPOSITORY" >&2
  exit 64
fi
if [[ -z "${RESTIC_PASSWORD:-}" && -z "${RESTIC_PASSWORD_FILE:-}" && -z "${RESTIC_PASSWORD_COMMAND:-}" ]]; then
  echo "Set RESTIC_PASSWORD, RESTIC_PASSWORD_FILE, or RESTIC_PASSWORD_COMMAND" >&2
  exit 64
fi

RESTORE_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/matrix-stack-restore-check.XXXXXX")"
cleanup() {
  rm -rf "$RESTORE_ROOT"
}
trap cleanup EXIT

restic snapshots --tag matrix-stack
restic restore latest --tag matrix-stack --target "$RESTORE_ROOT"

latest_dir="$(find "$RESTORE_ROOT" -maxdepth 2 -type f -name manifest.json -print -quit | xargs dirname)"
if [[ -z "$latest_dir" || ! -f "$latest_dir/manifest.json" ]]; then
  echo "Restore check failed: manifest.json not found" >&2
  exit 70
fi

if [[ ! -s "$latest_dir/checksums/SHA256SUMS" ]]; then
  echo "Restore check failed: SHA256SUMS missing" >&2
  exit 70
fi

(cd "$latest_dir" && shasum -a 256 -c checksums/SHA256SUMS >/dev/null)

dump_count="$(find "$latest_dir/postgres" -type f -name '*.dump' -size +0c | wc -l | tr -d ' ')"
if [[ "$dump_count" == "0" ]]; then
  echo "Restore check failed: no PostgreSQL dumps found" >&2
  exit 70
fi

echo "Restore check ok: $dump_count PostgreSQL dump(s), checksums valid."
