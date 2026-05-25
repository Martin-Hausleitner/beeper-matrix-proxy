# Portable Matrix Avatar Sync

This document describes the public-safe deployment shape for using
`beeper-source` as a portable Matrix-native room/avatar synchronizer on any
homeserver where the bridge user can upload media and set room state.

The sync writes ordinary Matrix state and media:

- room names: `m.room.name`
- room topics: `m.room.topic`
- room avatars: `m.room.avatar`
- service spaces: normal Matrix Space rooms with native app icons

Because the messenger badge is composed into the avatar image before upload,
Cinny, Element, and other Matrix clients can render the service marker without
client patches.

## What Gets Published

The public repository contains only reusable code, static brand-icon assets,
example configs, and local test tools. It must not contain:

- real access tokens
- real Beeper chat IDs
- real Matrix room IDs from a private deployment
- local SQLite databases
- logs
- screenshots with private room names or contacts
- contact-photo override files

Keep those in a deployment-local directory such as:

```bash
mkdir -p "$HOME/Library/Application Support/matrix-avatar-sync"
chmod 700 "$HOME/Library/Application Support/matrix-avatar-sync"
```

## Requirements

- A Matrix homeserver with standard Client-Server API support.
- A Matrix user/access token that can create rooms, upload media, and set room
  avatar/name/topic state in the rooms it owns.
- A Beeper Desktop API endpoint, usually `http://localhost:23373`.
- Go 1.25 or newer for building this repository.
- Optional: `node` plus `sharp` for regenerating local icon galleries.

This has mostly been exercised against Synapse. The implementation uses normal
Matrix APIs, so it should also work on other servers if their media upload,
room creation, and state APIs are compatible.

## Build

```bash
git clone https://github.com/Martin-Hausleitner/beeper-matrix-proxy.git
cd beeper-matrix-proxy
go build -o beeper-matrix-proxy
go build -o beeper-source ./cmd/beeper-source
```

## Minimal Read-Only Avatar Refresh

Start with a read-only run. This creates or refreshes Matrix-side portal rooms
from Beeper data, but prevents Matrix events from being sent back to real
contacts.

```bash
export BEEPER_ACCESS_TOKEN="..." # keep in a secret manager in production
export MATRIX_ACCESS_TOKEN="..." # keep in a secret manager in production

export BEEPER_MATRIX_PROXY_BEEPER_BASE_URL="http://localhost:23373"
export BEEPER_MATRIX_PROXY_MATRIX_HOMESERVER_URL="https://matrix.example.com"
export BEEPER_MATRIX_PROXY_MATRIX_USER_ID="@beeper_source:example.com"

export BEEPER_MATRIX_PROXY_SYNC_MODE=read_only
export BEEPER_MATRIX_PROXY_DISABLE_MATRIX_TO_BEEPER=true
export BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGES=true
export BEEPER_MATRIX_PROXY_MATRIX_DM_PARTICIPANT_AVATARS=true
export BEEPER_MATRIX_PROXY_MATRIX_FORCE_AVATAR_SYNC=true

./beeper-source \
  -db "$HOME/Library/Application Support/matrix-avatar-sync/beeper-source.db" \
  -once \
  -backfill-history=false
```

For a rooms/avatar-only repair run on an existing deployment:

```bash
export BEEPER_MATRIX_PROXY_PORTAL_CHECK_ACCESS=false
export BEEPER_MATRIX_PROXY_MATRIX_SPACES=false
export BEEPER_MATRIX_PROXY_MATRIX_FORCE_AVATAR_SYNC=true

./beeper-source \
  -db "$HOME/Library/Application Support/matrix-avatar-sync/beeper-source.db" \
  -once
```

## Avatar Badge Config

Use a deployment-local YAML file when you want the same visual system across
multiple homeservers:

```bash
export BEEPER_MATRIX_PROXY_AVATAR_BADGE_CONFIG="$HOME/Library/Application Support/matrix-avatar-sync/avatar-badge.yaml"
```

Public-safe example:

```yaml
avatar_badge:
  position: bottom-right
  layout: circle-safe
  shape: rounded
  size_percent: 34
  inset_percent: 0
  shadow: true

group_avatar:
  style: auto
  max_participants: 10
  overlap_percent: 34
  exclude_self: true
  self_ids:
    - "@your_matrix_user:example.com"
    - "Me"
```

`style: auto` renders Beeper-like grouped initials bubbles when there are two
or more visible participants. If only one visible participant remains after
self-exclusion, the renderer uses a full-size single initials avatar instead of
a tiny group bubble.

## Contact Photo Overrides

Manual contact-photo merge rules are intentionally private. Keep them outside
the repository:

```bash
export BEEPER_MATRIX_PROXY_CONTACT_AVATAR_OVERRIDES="$HOME/Library/Application Support/matrix-avatar-sync/contact-avatar-overrides.yaml"
```

Example shape:

```yaml
contacts:
  - display_name: "Example Person"
    aliases:
      - "Example WhatsApp Name"
    beeper_chat_ids:
      - "!public-example-only"
    sender_ids:
      - "@example"
    avatar_file: "~/Pictures/Contact Avatars/example-person.jpg"
    confidence: "manual"
```

Do not commit this file. It may contain private contact names, IDs, and local
photo paths.

## Server Integration Patterns

### Synapse / matrix-docker-ansible-deploy

Use a normal Matrix user token for the bridge user and run `beeper-source` as a
sidecar/systemd service near the Beeper Desktop API endpoint. If your Synapse
media store is S3-backed, avatar uploads still go through the homeserver media
API and then land in your configured media backend.

### Etke / Managed Synapse

Use the same token/env layout, but keep the process outside the managed stack
unless the operator explicitly supports custom sidecars. The bridge only needs
HTTPS access to the homeserver and local access to Beeper Desktop API.

### Non-Synapse Homeservers

Verify these API paths before enabling automation:

- `/_matrix/client/v3/upload`
- `/_matrix/client/v3/createRoom`
- `/_matrix/client/v3/rooms/{roomId}/state/m.room.avatar`
- `/_matrix/client/v3/rooms/{roomId}/state/m.room.name`
- `/_matrix/client/v3/rooms/{roomId}/state/m.room.topic`

If any of these differ or are partially implemented, use a read-only dry run and
inspect logs before scheduling recurring sync.

## Verification

Run unit tests locally:

```bash
go test ./beepersource ./cmd/beeper-source ./connector -count=1
```

Open the local configurator for visual checks:

```bash
open infra/avatar-configurator/index.html
```

The configurator is fully local. It uses checked-in icon assets and the
vendored `third_party/ui-avatars` reference, not the hosted `ui-avatars.com`
API.

For deployment verification, check:

- generated Matrix rooms have the expected `m.room.avatar` state
- direct chats prefer real Beeper/BIPA participant photos when available
- rooms without photos get initials plus a messenger badge
- one-visible-person groups render a full initials avatar, not a tiny bubble
- group avatars show 2 to 10 visible participants without bubble or badge overlap
- Cinny/Element render the avatar as expected after cache refresh

## Backup

Back up the local deployment state together:

- `beeper-source.db`
- avatar-badge YAML
- contact override YAML, if used
- service unit or launchd plist
- version/commit of this repository

Do not rely on Matrix media alone as the only backup. Matrix room avatar state
contains MXC references, while the local DB contains the mapping and sync state
needed for deterministic repairs.

## Ten Stability Improvements

1. Two-phase Matrix checkpointing so a crash after `/sync` fetch but before
   processing cannot drop messages.
2. A read-only dry-run planner that prints planned room/avatar changes before
   uploading media.
3. Per-homeserver capability probes for upload size, media auth behavior, and
   state-event permissions.
4. A resumable avatar upload queue with exponential backoff and persistent retry
   state for `429` and transient media failures.
5. Pixel-level golden tests for 1 to 10 participant group avatars across all
   badge positions.
6. A public-safe `doctor` command that validates env vars, token scopes, DB
   permissions, icon manifest integrity, and Matrix API reachability.
7. A restore test that rebuilds Matrix avatar state from only DB + config +
   local icon assets.
8. Optional Prometheus metrics for avatar uploads, cache hits, Matrix errors,
   Beeper API latency, and skipped contacts.
9. A strict public-export command that redacts room IDs, sender IDs, and local
   paths from evidence bundles.
10. A compatibility matrix for Synapse, Dendrite, Conduit, Continuwuity/Tuwunel,
    and managed Etke deployments.
