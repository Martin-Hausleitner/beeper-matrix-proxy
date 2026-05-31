# Avatar, Contact Merge, And Deep Research Status

Date: 2026-05-23

## Current Summary

The Matrix-native avatar path now has the polished v2 pipeline:

- Real Beeper/BIPA chat or participant avatars are uploaded as normal Matrix `m.room.avatar` media.
- A larger app-like messenger badge is composed into the avatar image, so Cinny and Element render it without client patches.
- The badge uses locally embedded brand PNG assets from `beepersource/assets/brand-icons/manifest.json`; runtime never downloads icons from the internet.
- Rooms without a real photo now get a generated initials avatar plus the messenger badge instead of a pure service-logo circle.
- Service spaces still use service icons.
- A private override file can supply manually approved contact photos before Beeper/BIPA avatars are considered.
- The hourly LaunchAgent enables this by default with `BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGES=true`.

Still intentionally unfinished:

- Automatic Apple Contacts image lookup is not enabled yet.
- Apple Photos can be searched read-only, but automatic face/person matching remains out of scope without a reviewed mapping step.
- The current manifest uses official/first-party sources where practical and Simple Icons as fallback, with per-icon source/license notes and hashes.
- No private contact photos, room lists, screenshots, or tokens belong in the public repo.

## Git And Code State

Branch:

```text
codex/matrix-archive-sync
```

Latest pushed avatar-badge commit:

```text
cda12db Add messenger badges to portal avatars
```

GitHub branch:

```text
https://github.com/Martin-Hausleitner/beeper-matrix-proxy/tree/codex/matrix-archive-sync
```

Important code files:

- `beepersource/avatar_badge.go`
- `beepersource/avatar_badge_test.go`
- `beepersource/brand_icon.go`
- `beepersource/brand_icon_test.go`
- `beepersource/contact_avatar.go`
- `beepersource/contact_avatar_test.go`
- `beepersource/assets/brand-icons/manifest.json`
- `beepersource/service.go`
- `beepersource/config.go`
- `README.md`
- `CHANGELOG.md`

Dependency decision:

```text
No runtime SVG renderer is used. Icon PNGs are checked in and embedded with Go embed.
```

## Evidence Already Produced

Avatar state proof was produced locally in the private evidence folder:

- `e2e-evidence/avatar-badge-mixed/avatar-state-proof.json`
- `e2e-evidence/avatar-badge-mixed/avatar-badge-pixel-proof.json`
- `e2e-evidence/avatar-badge-mixed/avatar-badge-gallery.png`

E2E proof was produced locally in the private evidence folder:

- `e2e-evidence/matrix-bridge-e2e-2026-05-22.log`
- `e2e-evidence/matrix-bridge-e2e-2026-05-22.png`
- `e2e-evidence/benchmarks-2026-05-22.txt`

Measured proof is kept in the private evidence folder and should not be copied
into public commits. The public repo should only describe the verification
method, not exact room counts, account mix, screenshots, or private archive
sizes.

Avatar v2 proof is stored locally under `e2e-evidence/avatar-v2`; publish only
sanitized summaries unless a release artifact has been reviewed for private
room names, account identifiers, screenshots, and contact data.

## Current Avatar Behavior

Priority today:

1. Private manual override file via `BEEPER_MATRIX_PROXY_CONTACT_AVATAR_OVERRIDES`.
2. Participant avatar for direct chats when enabled.
3. Chat-level Beeper/BIPA avatar if Beeper exposes one.
4. Generated initials avatar when no real chat/person image exists.
5. Messenger badge is applied last.

The default private override path used by the hourly local sync, when present, is:

```text
~/Library/Application Support/matrix-archive-sync/contact-avatar-overrides.yaml
```

Suggested private mapping file:

```yaml
contacts:
  - display_name: "Example Person"
    aliases:
      - "Example WhatsApp Name"
      - "+43123456789"
    beeper_chat_ids:
      - "!example:beeper.local"
    matrix_room_ids:
      - "!example:100.120.120.120"
    sender_ids:
      - "@example:whatsapp"
    avatar_file: "~/Pictures/Contact Avatars/example-person.jpg"
    confidence: "manual"
```

## OpenClaw Skills Found

Relevant local skills:

- `apple-photos-local-search`
  Read-only Apple Photos search using local Photos indexes, OCR, metadata, and derivative paths. Useful for finding candidate photos, not for automatic identity matching.

- `google-deep-researcher`
  Useful for running Gemini/Google Deep Research in a real browser profile.

- `hidden-brave-comet-deep-research`
  Another browser-based Deep Research route.

- `browser-profile-routing`
  Useful for choosing the right local browser/profile safely.

- `popular-web-designs`
  Useful for visual polish references.

## Best Next Implementation Plan

### 1. Real Icon Asset Pipeline

Goal: replace generated approximations with high-quality brand icons.

Recommended source strategy:

1. Prefer official brand asset pages where licensing permits local use.
2. Use Simple Icons as a fallback for SVG brand marks.
3. Store a local icon manifest with source URL, license, brand color, and update date.
4. Render icons into PNG during asset preparation, not during every sync.
5. Use stable cache keys like `platform-logo-v5:<platform>` and `avatar-badge-v2:<sourceHash>:<platformIconHash>`.

Design direction:

- Badge is larger than now but still secondary.
- Rounded app-like badge container.
- Soft shadow and subtle off-white border instead of a harsh white ring.
- Adaptive treatment for light/dark avatars so the badge reads clearly.

### 2. Better Fallback Avatar

For chats without a real photo:

1. Do not use only the messenger logo as the room avatar.
2. Generate a neutral person silhouette or initials/avatar base.
3. Add the messenger badge bottom-right.
4. Keep service spaces themselves using the service icon.

This makes the room list feel like people/chats first, service second.

### 3. Contact And Photo Merge

Do not blindly guess identities from Apple Photos.

Recommended merge priority:

1. Manual/private override file.
2. Apple Contacts image, when contact identity is matched.
3. Beeper/BIPA participant avatar.
4. Beeper/BIPA chat avatar.
5. Generated person/initials fallback.
6. Messenger badge is applied last.

Why manual first:

- Photos face clusters and contact names are private and can be wrong.
- A wrong profile photo in chat UI is worse than a generic avatar.
- The sync should be deterministic and auditable.

### 4. Matrix Native Output

Everything should still end as normal Matrix state:

- Room avatar: `m.room.avatar`
- Sender/per-message profile where supported: `com.beeper.per_message_profile`
- Archive metadata: store source avatar URL, merge source, badge source, content hash, and generated MXC.

Cinny and Element should not need any patch.

## Deep Research System Prompt

Use this as the system prompt for Deep Research:

```text
You are a senior product engineer, design-systems researcher, and privacy-minded local-first data architect. Research the best way to build a polished Matrix/Beeper avatar and contact-photo synchronization system for a personal Matrix/Synapse setup viewed in Cinny and Element.

Context:
- The local project is a Beeper/BIPA-to-Matrix sync/archive system.
- Rooms are normal Matrix rooms on a private Synapse homeserver.
- Avatars must be written as native Matrix `m.room.avatar` media so Cinny and Element render them without client patches.
- Current implementation already composes a messenger badge into real Beeper/BIPA chat or participant avatars.
- Needed improvement: use real/high-quality native app icons for WhatsApp, Signal, Telegram, Beeper, Matrix, email, iMessage, Messenger, Instagram, Discord, Slack, X, LinkedIn, and future services.
- Needed improvement: if no real photo exists, use a tasteful generated initials avatar, not only the messenger logo.
- Needed improvement: merge profile photos across Beeper/BIPA, Matrix, Apple Contacts, optional Apple Photos candidates, and private manual override files.
- Safety: do not send messages to real contacts, do not upload private photos to third-party services, and do not rely on cloud face recognition. Prefer local-only, auditable workflows.

Research goals:
1. Identify the best sources for official/native app icons:
   - official brand guidelines and asset downloads where available;
   - licensing constraints for local/private use;
   - whether Simple Icons is acceptable as a fallback;
   - how to handle iOS/Android app icons versus flat brand glyphs.
2. Propose a badge visual system:
   - badge size relative to 256px and 512px avatars;
   - border/ring/shadow treatments that look like a high-quality app;
   - light/dark avatar contrast handling;
   - circular, squircle, or app-icon badge shape;
   - how to avoid ugly white bars/rings.
3. Propose a fallback-avatar system:
   - neutral person silhouettes;
   - initials;
   - color generation;
   - service badge overlay;
   - accessibility and recognizability in Cinny/Element sidebars.
4. Propose an identity merge architecture:
   - deterministic identity graph across Beeper chat IDs, Matrix room IDs, sender IDs, phone numbers, emails, display names, and Apple Contacts identifiers;
   - manual override file format;
   - conflict handling;
   - confidence scoring;
   - no automatic Apple Photos face matching unless manually approved.
5. Propose a local asset/cache architecture:
   - content-addressed avatar store;
   - content hashes;
   - cache invalidation when Beeper/BIPA returns changed bytes for the same URL;
   - generated output sizes and formats;
   - Matrix media upload deduplication.
6. Propose E2E verification:
   - unit tests for icon rendering, badge composition, cache invalidation, fallback avatars, and merge precedence;
   - local Synapse tests proving `m.room.avatar` updates;
   - Cinny and Element browser screenshots;
   - pixel-level checks proving the badge exists;
   - privacy checks proving no private photo or contact data is committed to GitHub.

Deliverables:
- A ranked recommendation with 2-3 alternative stacks.
- A concrete data model for contacts, identities, avatar sources, generated avatars, and Matrix MXC outputs.
- A small visual spec for badge placement, sizing, shadows, borders, and fallbacks.
- A privacy/security checklist.
- A migration plan from the current generated-icon implementation to official/native icon assets.
- Links to all sources, especially icon licensing and brand guideline pages.

Important constraints:
- Everything must work locally and offline after assets are cached.
- No client patching for Cinny or Element.
- No GitHub Actions required for verification.
- No private screenshots, room names, phone numbers, contact photos, or tokens should be committed publicly.
```

## Immediate To-Do Checklist

- [x] Remove unused exploratory SVG renderer dependencies from production.
- [x] Create `assets/brand-icons/manifest.json` with source, license note, color, local PNG path, and hash.
- [x] Replace generated badge shapes with embedded brand icon assets.
- [x] Improve badge visual style: larger, app-like, soft border/shadow, no harsh white bar.
- [x] Add person/default fallback avatar for rooms without real Beeper/BIPA photos.
- [x] Add private contact override config outside the public repo.
- [ ] Add Apple Contacts image lookup as an optional local source.
- [x] Use Apple Photos only as a candidate search tool, never as an automatic identity resolver.
- [x] Re-run rooms-only avatar refresh in read-only safety mode.
- [x] Re-run proof generation: Matrix state, downloaded MXC samples, and private avatar gallery.
- [ ] Re-run Cinny/Element screenshots after the next visible client refresh.
