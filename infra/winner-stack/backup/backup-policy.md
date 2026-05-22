# Backup Policy

## Goal

Make the Matrix/Nextcloud winner stack restorable without depending on the live
host, local disk, or the primary media/file buckets.

## What Gets Backed Up By `run-backup.sh`

| Data | Method | Why |
|---|---|---|
| PostgreSQL databases | `pg_dump -Fc` into encrypted restic | Synapse events, Nextcloud metadata, bridge state, bot state. |
| MDAD inventory/config | rsync into staging, then restic | Homeserver config, Traefik config, Synapse settings. |
| Synapse signing/appservice material | included from MDAD paths | Required for federation identity and bridge registration continuity. |
| Nextcloud compose/config | rsync into staging, then restic | Required to reconstruct the app container and object-store wiring. |
| Beeper/bridge config | rsync into staging, then restic | Required for bridge continuity. |
| Matrix archive snapshot | rsync into staging, then restic | Readable chat archive plus media manifest. |
| S3 inventories | `aws s3 ls --summarize` if configured | Proves primary buckets existed and gives restore drill counts. |
| Checksums/manifest | SHA-256 + JSON manifest | Non-destructive restore verification. |

## What Must Be Protected Separately

- Matrix client E2EE recovery keys and secure backup passphrases.
- Provider-side versioning/replication for `matrix-media` and
  `nextcloud-primary`.
- DNS records and registrar access.
- S3 account credentials and IAM policy backups.

## Restore Order

1. Provision a clean host.
2. Restore MDAD and Nextcloud config from restic.
3. Create empty PostgreSQL cluster and users.
4. Restore PostgreSQL dumps with `pg_restore`.
5. Reconnect Synapse to `matrix-media`.
6. Reconnect Nextcloud to `nextcloud-primary`.
7. Reinstall bridge/appservice registrations.
8. Run Element and Cinny login smoke tests.
9. Run `restore-check.sh` and one real room/media retrieval test.

## Scheduling

Recommended cadence:

- PostgreSQL/config restic backup: hourly or every 4 hours.
- Restic prune: daily.
- Restic restore check: daily lightweight checksum check.
- Full restore drill: monthly.
- Primary S3 bucket inventory: daily.
- Provider-side bucket versioning/replication: always on where available.

## Hard Rules

- Do not back up primary buckets into the same account that hosts primary data.
- Do not store plaintext restic passwords in the repo.
- Do not treat S3 as backup. S3 is primary storage for media/files here.
- Do not skip restore tests; a backup without restore proof is only a wish.
