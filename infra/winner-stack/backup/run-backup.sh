#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: run-backup.sh [backup.env]

Creates an encrypted restic backup for the winner-stack deployment:
- PostgreSQL custom-format dumps
- Matrix/MDAD config, signing keys, bridge registrations
- Nextcloud compose/config
- beeper-matrix-proxy config and matrix-archive snapshot, when present
- S3 inventory manifests for primary Matrix/Nextcloud buckets, when configured

No primary S3 media bucket is copied by default. Use provider-side versioning or
replication for matrix-media and nextcloud-primary, then back up DB/config here.
USAGE
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

ENV_FILE="${1:-./backup.env}"
if [[ -f "$ENV_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$ENV_FILE"
fi

require() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "Missing required env: $name" >&2
    exit 64
  fi
}

require RESTIC_REPOSITORY
if [[ -z "${RESTIC_PASSWORD:-}" && -z "${RESTIC_PASSWORD_FILE:-}" && -z "${RESTIC_PASSWORD_COMMAND:-}" ]]; then
  echo "Set RESTIC_PASSWORD, RESTIC_PASSWORD_FILE, or RESTIC_PASSWORD_COMMAND" >&2
  exit 64
fi

BACKUP_STAGING_ROOT="${BACKUP_STAGING_ROOT:-/var/tmp/matrix-stack-backup}"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
STAGING="$BACKUP_STAGING_ROOT/$RUN_ID"
MANIFEST="$STAGING/manifest.json"
mkdir -p "$STAGING"/{postgres,configs,s3,checksums}

cleanup() {
  if [[ "${BACKUP_KEEP_STAGING:-false}" != "true" ]]; then
    rm -rf "$STAGING"
  else
    echo "Keeping staging directory: $STAGING"
  fi
}
trap cleanup EXIT

copy_if_exists() {
  local src="$1"
  local dest="$2"
  if [[ -e "$src" ]]; then
    mkdir -p "$(dirname "$dest")"
    rsync -a --delete "$src" "$dest"
  fi
}

dump_database() {
  local db="$1"
  local out="$STAGING/postgres/${db}.dump"
  echo "Dumping PostgreSQL database: $db"
  if [[ -n "${POSTGRES_CONTAINER:-}" ]]; then
    docker exec -e "PGPASSWORD=${POSTGRES_PASSWORD:-}" "$POSTGRES_CONTAINER" \
      pg_dump -Fc -U "${POSTGRES_USER:-postgres}" "$db" >"$out"
  else
    require POSTGRES_HOST
    PGPASSWORD="${POSTGRES_PASSWORD:-}" pg_dump -Fc \
      -h "$POSTGRES_HOST" \
      -p "${POSTGRES_PORT:-5432}" \
      -U "${POSTGRES_USER:-postgres}" \
      -d "$db" >"$out"
  fi
}

for db in ${POSTGRES_DATABASES:-synapse nextcloud}; do
  dump_database "$db"
done

copy_if_exists "${MDAD_BASE_PATH:-/matrix}/inventory" "$STAGING/configs/matrix-docker-ansible-deploy/inventory"
copy_if_exists "${MDAD_BASE_PATH:-/matrix}/matrix" "$STAGING/configs/matrix"
copy_if_exists "${NEXTCLOUD_COMPOSE_DIR:-/opt/nextcloud}" "$STAGING/configs/nextcloud"
copy_if_exists "${BEEPER_PROXY_DIR:-/opt/beeper-matrix-proxy}/config.yaml" "$STAGING/configs/beeper-matrix-proxy/config.yaml"
copy_if_exists "${BEEPER_PROXY_DIR:-/opt/beeper-matrix-proxy}/registration.yaml" "$STAGING/configs/beeper-matrix-proxy/registration.yaml"
copy_if_exists "${MATRIX_ARCHIVE_DIR:-/opt/matrix-archive}/snapshot" "$STAGING/configs/matrix-archive/snapshot"

if command -v aws >/dev/null 2>&1; then
  aws_args=()
  if [[ -n "${AWS_PROFILE:-}" ]]; then
    aws_args=(--profile "$AWS_PROFILE")
  fi
  if [[ -n "${MATRIX_MEDIA_BUCKET:-}" ]]; then
    aws "${aws_args[@]}" s3 ls "s3://$MATRIX_MEDIA_BUCKET" --recursive --summarize \
      >"$STAGING/s3/matrix-media.inventory.txt" || true
  fi
  if [[ -n "${NEXTCLOUD_PRIMARY_BUCKET:-}" ]]; then
    aws "${aws_args[@]}" s3 ls "s3://$NEXTCLOUD_PRIMARY_BUCKET" --recursive --summarize \
      >"$STAGING/s3/nextcloud-primary.inventory.txt" || true
  fi
fi

(cd "$STAGING" && find . -type f -print0 | sort -z | xargs -0 shasum -a 256 >checksums/SHA256SUMS)

python3 - <<PY >"$MANIFEST"
import json, os, pathlib, time
root = pathlib.Path("$STAGING")
files = []
for path in sorted(p for p in root.rglob("*") if p.is_file()):
    rel = path.relative_to(root).as_posix()
    files.append({"path": rel, "size": path.stat().st_size})
summary = {
    "run_id": "$RUN_ID",
    "created_at_unix": int(time.time()),
    "hostname": os.uname().nodename,
    "postgres_databases": "${POSTGRES_DATABASES:-synapse nextcloud}".split(),
    "files": files,
}
print(json.dumps(summary, indent=2, sort_keys=True))
PY

restic backup "$STAGING" --tag matrix-stack --tag "$RUN_ID"

if [[ "${RESTIC_FORGET:-true}" == "true" ]]; then
  restic forget --prune \
    --keep-daily "${RESTIC_KEEP_DAILY:-14}" \
    --keep-weekly "${RESTIC_KEEP_WEEKLY:-8}" \
    --keep-monthly "${RESTIC_KEEP_MONTHLY:-12}" \
    --tag matrix-stack
fi

restic check --read-data-subset="${RESTIC_CHECK_READ_DATA_SUBSET:-1/50}"

echo "Backup complete: $RUN_ID"
