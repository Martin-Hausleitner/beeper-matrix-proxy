# Winner Stack Blueprint

This folder contains the reproducible blueprint for the chosen small-host
Matrix/Nextcloud setup:

- Synapse via `matrix-docker-ansible-deploy` or an Etke-compatible Ansible
  operation model.
- Element Web and Cinny as static Matrix web clients.
- A separate minimal Nextcloud Docker Compose project.
- One PostgreSQL cluster with separate databases/users.
- Redis only for Nextcloud file locks and app cache.
- S3 primary storage for Synapse media through `synapse-s3-storage-provider`.
- S3 primary object storage for Nextcloud files.
- Backups with restic, PostgreSQL dumps, config/signing-key capture, and optional
  S3 replication checks.

The important rule: Nextcloud is not the Matrix media backend. WebDAV/FUSE is not
primary storage. Matrix media, Nextcloud files, and backups use separate buckets.

## Buckets

| Bucket | Purpose | Owner |
|---|---|---|
| `matrix-media` | Synapse uploads, remote media cache, thumbnails | Synapse only |
| `nextcloud-primary` | Nextcloud file contents | Nextcloud only |
| `infra-backup` | DB dumps, configs, signing keys, appservice tokens, manifests | Backup tool only |

Use a different account or provider for `infra-backup` where possible.

## Files

| Path | Purpose |
|---|---|
| `matrix-docker-ansible-deploy/vars.example.yml` | MDAD/Etke-compatible vars for Synapse, S3 media, Element, Cinny, and conservative resource settings. |
| `nextcloud/docker-compose.yml` | Minimal Nextcloud Apache/FPM-style compose with Redis and external PostgreSQL/S3 settings. |
| `nextcloud/.env.example` | Safe environment template with no secrets. |
| `nextcloud/objectstore.config.php.example` | Nextcloud primary S3 object-store config. |
| `backup/backup.env.example` | Backup environment template. |
| `backup/run-backup.sh` | Restic backup job for DB dumps, Matrix/Nextcloud configs, bridge configs, archive snapshots, and manifests. |
| `backup/restore-check.sh` | Non-destructive restore verification. |
| `backup/backup-policy.md` | What is backed up, what is not, and restore requirements. |

## Deployment Order

1. Deploy Synapse with MDAD and enable S3 media storage from the start.
2. Deploy Element Web and Cinny behind the MDAD Traefik network.
3. Deploy the minimal Nextcloud compose project on the same Traefik network.
4. Enable Nextcloud primary object storage before real user files are added.
5. Configure restic backup to a separate bucket/account.
6. Run `backup/run-backup.sh`, then `backup/restore-check.sh`.
7. Only then enable bridges and bots one by one.

## RAM Rules For 8 GB Hosts

- Keep Synapse monolithic initially. Do not enable Synapse worker fan-out until
  there is a measured need.
- Run at most two heavy bridges at once.
- Do not run Nextcloud AIO, Collabora, Talk, Recognize, or local Whisper as
  always-on services on the same host.
- Use external STT APIs or an on-demand STT container with a memory limit.
- Keep Matrix Media Repo out of the initial deployment.

## Backup Boundary

Backups must include:

- PostgreSQL dumps for Synapse, Nextcloud, bridges, bots, and MAS/OIDC if used.
- Synapse signing key, homeserver config, appservice registrations, and bridge
  registration tokens.
- Nextcloud config, compose files, app config, and data model metadata.
- Matrix archive snapshots if `matrix-archive-sync` is used on the same host.
- Backup manifests, checksums, and restic snapshots.

Backups do not replace:

- Client E2EE recovery keys or Matrix secure backup.
- S3 provider-side bucket versioning/replication for primary object storage.
- Regular restore drills.
