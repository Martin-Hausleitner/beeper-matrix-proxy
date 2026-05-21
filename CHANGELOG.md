# Changelog

All notable changes to `beeper-matrix-proxy` are documented here.

The project does not have tagged releases yet. Commit hashes below refer to the
public `main` branch.

## Unreleased

### Added

- `matrix-archive-sync`, a standalone Rust CLI that archives standard Matrix
  rooms into SQLite with zstd-compressed raw events, FTS5 search fields, a
  SHA-256 content-addressed media store, static HTML/JSONL exports, and
  restic-friendly snapshot manifests.
- `cmd/beeper-source -backfill-history`, a safe read-only Beeper Desktop API
  history crawler that paginates older messages with Beeper's `oldestCursor`
  and mirrors them into existing Matrix portal rooms before archive sync.
- `cmd/beeper-source`, an executable reconcile loop that reads Beeper Desktop
  API chats and mirrors them into Matrix rooms.
- Matrix client sink for `beeper-source` with room creation, deterministic
  transaction IDs, optional invite target, sender-prefix fallback, and Beeper
  per-message profile metadata.
- Beeper chat avatar mirroring into Matrix portal room icons, including local
  Beeper media paths, `file://` paths, remote asset downloads, and refreshes for
  already-created rooms.
- `cmd/beeper-source -rooms-only` to create/update Matrix rooms for all Beeper
  chats without importing message history or enabling Matrix -> Beeper sends.
- Paginated Beeper chat discovery via `/v1/chats`, so all chats are considered
  instead of only the first API page.
- Safe all-chat defaults for rooms-only imports: archived chats are skipped
  unless `BEEPER_MATRIX_PROXY_INCLUDE_ARCHIVED=true` is set.
- Configurable parallel portal creation via `BEEPER_MATRIX_PROXY_PORTAL_WORKERS`
  so large Beeper accounts can be mirrored into Cinny without a fully
  sequential room-creation bottleneck.
- Best-effort rooms-only imports: one slow or failing Beeper chat no longer
  aborts the whole all-chat import, and
  `BEEPER_MATRIX_PROXY_PORTAL_TIMEOUT_SECONDS` controls the per-room retry
  window.
- Adaptive Matrix room-creation backpressure for rooms-only imports: `429
  M_LIMIT_EXCEEDED` responses now honor `retry_after_ms`/`Retry-After`, retry
  the individual portal, and reduce concurrent workers instead of aborting the
  entire import.
- Stale portal recovery for resumable rooms-only imports: existing portal rows
  are checked for Matrix room accessibility and recreated when the Matrix room
  is no longer reachable by the bridge account.
- `cmd/beeper-source -rooms-only` now forcibly applies the safety mode for
  all-chat imports by setting sync mode to read-only and disabling
  Matrix -> Beeper sends regardless of the surrounding environment. It also
  enables Matrix Spaces for the all-chat Cinny import while keeping real
  Beeper/BIPA chat avatars preferred over platform-logo room avatars.
- `BEEPER_MATRIX_PROXY_MATRIX_ROOM_INCLUDE_PLATFORM` controls whether portal
  room names include bracketed service names such as `[Telegram]`; rooms-only
  mode disables the bracket and clears the default `Beeper: ` prefix because
  Matrix Spaces group rooms by service. Explicit `BEEPER_MATRIX_PROXY_MATRIX_ROOM_PREFIX`
  values are still preserved.
- Relative Beeper/BIPA avatar asset URLs are resolved against
  `BEEPER_MATRIX_PROXY_BEEPER_BASE_URL` before download, so rooms can use the
  full chat/person profile picture when Beeper exposes one.
- Rooms-only imports now refresh existing portal room names, topics, and
  avatars instead of only creating missing portals; this keeps idempotent
  re-runs useful after config changes.
- `BEEPER_MATRIX_PROXY_PORTAL_CHECK_ACCESS=false` can skip stale-room access
  probes for fast read-only profile/avatar refresh runs, while the default keeps
  stale portal recovery enabled.
- Absolute local Beeper avatar paths, including escaped `Application%20Support`
  paths, are kept local and uploaded directly instead of being incorrectly
  rewritten as Desktop API URLs.
- Matrix Space organization for Beeper-source rooms: rooms-only import now
  creates a Beeper root space, creates one service space per messenger network,
  and links each portal room under its service space.
- Top-level service-space layout for Cinny: WhatsApp, Signal, Telegram,
  bridgev2, and Beeper(Matrix) spaces are left visible as their own workspaces
  while still being linked from the Beeper root space.
- Platform avatar uploads are cached in SQLite `media_cache`, so hundreds of
  WhatsApp/Signal/Telegram rooms can reuse the same Matrix `mxc://` icon
  instead of reuploading identical PNGs.
- Generated PNG platform logo avatars under `platform-logo-v3:*`, replacing
  SVG initials for known services so Cinny can render service icons more
  reliably.
- Beeper `network` names on Matrix rooms plus generated platform PNG
  avatars (`BEEPER_MATRIX_PROXY_MATRIX_PLATFORM_AVATARS=true`) for WhatsApp,
  Signal, Telegram, and other services.
- Matrix `/sync` source for bidirectional `beeper-source` text messages from
  Cinny/Matrix portal rooms back to Beeper.
- Matrix `/sync` source support for Matrix -> Beeper media, edits, redactions,
  and reactions.
- Bidirectional reply remapping for `beeper-source`: Beeper `linkedMessageID`
  becomes Matrix `m.in_reply_to`, and Matrix `m.in_reply_to` becomes Beeper
  `replyToMessageID`.
- Post-sync reconcile in `cmd/beeper-source` when Matrix events were handled,
  so Matrix-originated Beeper echo mappings stabilize in the same CLI run
  without extra idle API calls.
- Beeper Desktop API adapter support for multipart asset uploads, message
  updates, message deletes, and reaction add/remove calls.
- Persistent outbound echo suppression so Matrix-originated Beeper sends are
  mapped back to the original Matrix event instead of being mirrored twice.
- Echo-version preservation so an edited Beeper echo refreshes the existing
  mapping without overwriting the original Matrix event ID.
- Matrix-side configuration for homeserver URL, token env, user ID, invite
  user, room prefix, sender fallback, and opt-in local self-signed TLS.
- Tests that verify Matrix room creation and message send payloads against an
  HTTP test homeserver.
- `beeper-source` subsystem for the reverse direction: Beeper Desktop API as
  source, your own Matrix/Synapse appservice as destination.
- Official Beeper Desktop Go SDK pinned at `v5.0.1` with a healthcheck/send
  adapter scaffold.
- SQLite WAL state store for Beeper portal, puppet, message, reaction, pending
  mutation, media cache, and queue tables.
- Deterministic Matrix transaction IDs, bounded echo suppression, media fallback
  decisions, and Deeper-style platform detection for Beeper account IDs.
- Unit tests for the new Beeper-source config, SDK adapter, store, mapping,
  pipeline, WebSocket subscription command, media policy, and safety behavior.
- README performance snapshot with measured hot-path improvements.
- README live VCVM all-chat evidence: 700 discovered Beeper chats, 694 active,
  6 archived, 701 Matrix portal rows, 0 missing active chats, 701/701 portal
  rooms joined by `@cinny_beeper_test:100.120.120.120`, and a saved Cinny
  screenshot at `/tmp/beeper-source-cinny-all-chats-final.png`.
- README live Matrix Spaces evidence: 5 service spaces under the Beeper root,
  6/6 spaces joined by the Cinny test user, and PNG avatar state verified for
  all service spaces.
- README link to this changelog.
- Mixed-modality local Synapse E2E coverage for text, image, file, audio, video,
  location, emote, notice, edits, stickers, reactions, redactions, polls, room
  state, and call invites.
- Optional performance profiling artifacts via `PERF_PROFILE=1 ./scripts/perf.sh`
  (`cpu.pprof`, `mem.pprof`, and top reports).
- `metadata.json` in every performance result directory with commit, Go version,
  platform, benchmark regex, and E2E/profile settings.
- `benchmark-summary.json` and `synapse-summary.json` for quick regression
  comparison without parsing human-readable logs.
- Tested `scripts/perf_metrics.py` helper for metadata, summary generation, and
  performance gate checks.
- Optional `PERF_ENFORCE_GATES=1` mode for local performance smoke benchmarks.
- More regression tests for Beeper media normalization, capability contracts,
  poll fallbacks, edit cleanup, metadata isolation, and performance gate parsing.
- Performance gates now fail explicitly when requested Synapse E2E summaries are
  missing burst or mixed-modality measurements, and custom gate files deep-merge
  with default gates instead of replacing them wholesale.
- Multi-Synapse E2E runner support via `LOCAL_SYNAPSE_E2E_SERVER_COUNT`, with
  primary and peer users registered on every disposable homeserver.
- Expanded real Synapse E2E coverage for multi-room bursts, dual-user rooms,
  authenticated media upload/download, poll start/response/end, typing, and read
  receipt sync.
- `synapse-summary.json` now parses multi-room, dual-user, media, poll, and
  ephemeral E2E results in addition to burst and mixed-modality timings.
- Real Synapse E2E coverage for HTTP 413 upload-limit enforcement, room
  profile state (`m.room.name`, `m.room.topic`, `m.room.avatar`), and
  reply/thread relation payloads.
- Optional parallel multi-homeserver test execution with
  `LOCAL_SYNAPSE_E2E_PARALLEL=1`.
- `synapse-summary.json` now also parses upload-limit, room-state profile, and
  relation E2E probes.
- A real-server 30-point Synapse E2E matrix covering setup, burst-relevant sync
  basics, text/formatting, media modalities, voice/GIF-shaped payloads, polls,
  room profile state, relations, dual-user delivery, upload/download, 413
  handling, typing, and receipts.
- Real-server edge probes for media-config upload limits, `/messages` history
  pagination, and Synapse restart continuity with an existing `/sync` token.
- `LOCAL_SYNAPSE_E2E_RUN_REGEX` for faster focused local E2E loops.
- Performance gates now require a complete 30/30 Synapse matrix whenever a
  Synapse summary artifact is present, plus the real edge probes above.

### Performance

- Replace JSON round-tripping in `cloneRawMap` with targeted recursive cloning
  for poll/raw-event payloads, reducing the measured test case from roughly 60
  allocations to 12 allocations per clone.
- Cache the complete generated fallback avatar object, reducing repeated
  fallback avatar calls from 3 allocations to 0 allocations in the benchmark.
- Latest `beeper-source` 500-text-message reconcile benchmark on Apple M4 Pro:
  `26159423 ns/op`, `1440863 B/op`, `31662 allocs/op`.
- Live one-run Matrix -> Beeper echo mapping check completed in roughly 2.32s
  including Go process startup and Beeper/Matrix API roundtrips.

### Changed

- Removed the GitHub Actions workflow and README CI badge so validation stays
  local-only in the VCVM, as requested.

### Verified

- Live Matrix/Cinny -> Beeper Signal test group E2E for text, image, edit,
  reaction, and delete.
- Live Matrix/Cinny -> Beeper WhatsApp test group E2E for text and image.
- Browser-verified Cinny v4.11.1 room list with the Beeper BotE2E Signal,
  WhatsApp, and sh-vcvm Matrix rooms visible.
- Live WhatsApp test group avatar E2E: Beeper local avatar media was uploaded to
  Matrix as `m.room.avatar` and Cinny showed the room avatar state change.
- Live Signal reply E2E in both directions: Matrix reply mapped to Beeper
  `linkedMessageID`, and Beeper reply mapped to Matrix `m.in_reply_to`.
- Live Signal and WhatsApp media E2E in both directions for file, GIF, and
  audio; Cinny rendered the Matrix rooms with file controls, GIF previews, and
  audio playback controls.
- Live VCVM all-chat rooms-only import measured 700 Beeper chats via paginated
  `/v1/chats` (694 active, 6 archived). The safe import runs with Matrix ->
  Beeper disabled, Matrix Spaces, PNG platform avatars, and resumable portal
  creation because Synapse room creation rate-limits still apply.

## 2026-05-20

### Performance

- `a3c00d9` - Cache generated fallback avatars for repeated stale Matrix media
  requests.
- `a3c00d9` - Raise the default remote Matrix `/sync` timeline window from 50 to
  100 events.
- `a3c00d9` - Add `LOCAL_MATRIX_SYNC_TIMELINE_LIMIT` so larger deployments can
  tune burst tolerance without code changes.
- `a3c00d9` - Teach the local Synapse E2E runner to raise the test sync window
  to the largest configured burst automatically.
- `80fb7ce` - Replace JSON round-tripping in the message clone hot path with
  targeted deep copies.
- `80fb7ce` - Bound the sent-event echo suppression cache to avoid unbounded
  growth during remote outages.
- `80fb7ce` - Reduce Matrix sync payload size with lazy-loaded members and a
  bridge-focused state filter.

### Testing

- `98d2c47` - Write benchmark output to `perf-results/<timestamp>/bench.txt`.
- `98d2c47` - Write machine-readable benchmark output to
  `perf-results/<timestamp>/bench.jsonl`.
- `98d2c47` - Record local Synapse E2E output in
  `perf-results/<timestamp>/synapse-e2e.txt`.
- `98d2c47` - Add multi-burst local Synapse E2E testing via
  `LOCAL_SYNAPSE_E2E_BURSTS=10,25,40`.
- `80fb7ce` - Add a disposable Docker Synapse E2E harness that registers a test
  user, uploads the bridge sync filter, sends message bursts, and verifies that
  all burst messages arrive through `/sync`.

### Compatibility

- `9f04820` - Harden direct media handling and reaction redactions.
- `71a971c` - Persist Matrix sync filter and next-batch state safely.
- `1dc602d` - Harden Matrix sync and media proxying.
- `f03ba39` - Bridge Matrix typing notifications, read receipts, and call
  notices.

## Current Benchmark Reference

Latest measured local run on Apple M4 Pro:

| Benchmark | Result |
|---|---:|
| `BenchmarkCloneMessageContent` | ~150 ns/op, 576 B/op, 5 allocs/op |
| `BenchmarkGeneratedFallbackAvatarFromMXC` | ~5.9 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkCloneRawMap` | ~664 ns/op, 1736 B/op, 12 allocs/op |
| `BenchmarkReconcileFiveHundredTextMessages` | ~23.5 ms/op, 1.44 MB/op, 31661 allocs/op |
| Local Synapse burst E2E | 10/10, 40/40, and 100/100 messages delivered |
| Local Synapse mixed-modality E2E | text, edit, sticker, reaction, redaction, poll, room state, and call invite delivered |
