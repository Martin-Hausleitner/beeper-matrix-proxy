# Beeper Desktop Bridgev2 Custom Icon

This repository includes a local-only helper for customizing the native Beeper
Desktop sidebar icon used by self-hosted `bridgev2` accounts such as
`sh-vcvm-matrix`.

By default, Beeper Desktop does not ship a specific icon mapping for generic
self-hosted `bridgev2` accounts. It falls back to `bridge-generic`. The helper
patches the local Beeper Desktop renderer so `bridgev2` uses the generic bridge
slot, then replaces that generic bridge background with any local image you
choose.

## Quick Start

Run a dry self-test:

```bash
npm run beeper:bridgev2-icon:self-test
```

Patch Beeper Desktop with any local image:

```bash
node scripts/patch_beeper_desktop_bridgev2_icon.cjs --image /absolute/path/to/icon.png
```

Check that both Beeper renderer bundles are patched:

```bash
npm run beeper:bridgev2-icon:check
```

Restart Beeper Desktop:

```bash
osascript -e 'tell application "Beeper Desktop" to quit'
open -a "Beeper Desktop"
```

Restore the latest backup:

```bash
npm run beeper:bridgev2-icon:restore
```

## Supported Images

The helper embeds the image into a rounded 28x28 SVG background. Supported
extensions:

- `.png`
- `.jpg` / `.jpeg`
- `.svg`
- `.webp`
- `.gif`
- `.avif`

The image is cropped with `preserveAspectRatio="xMidYMid slice"`, so square
icons work best. Large photos work, but you should preview the crop first.

## Offline Testbench

Open the offline preview page:

```bash
open infra/beeper-desktop-icon-testbench/index.html
```

Drop or choose any image. The page renders a Beeper-style sidebar preview and
prints the exact patch command for the selected local file.

## Update Safety

This is a local Beeper Desktop renderer patch. Beeper updates can overwrite the
patched files. Re-run the script after an update.

Every patch run creates backups next to the patched Beeper files using this
suffix:

```text
.bak-openclaw-bridgev2-icon-YYYYMMDDHHMMSS
```

No private tokens, chats, or account credentials are touched.

## Icon Layers

There are two different icon systems that look similar in the UI:

| Layer | Where it appears | How it is generated | Can it be customized per chat? |
|---|---|---|---|
| Native Beeper account/platform icon | Beeper Desktop left account rail | Local Beeper renderer platform metadata. This helper maps `bridgev2` to the generic bridge platform and replaces that platform's background with your image. | No, this patch is global for the self-hosted `bridgev2` platform slot. |
| Chat/room avatar badge | Small messenger icon on a chat avatar, usually bottom-right | Matrix avatar sync composes a real profile/group avatar plus a messenger badge, then writes it as standard Matrix `m.room.avatar`. | Yes, on the Matrix/Cinny/Element side via avatar sync config and per-room avatar refresh. |

The screenshot-style badge on a chat row is therefore not produced by this
Beeper Desktop helper. It is produced by either native Beeper UI metadata or by
the Matrix avatar-sync pipeline when viewing mirrored rooms in Cinny/Element.

## Matrix/Cinny Per-Chat Badge Controls

For Matrix clients, use the avatar sync config instead of patching Beeper
Desktop:

```bash
export BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGES=true
export BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_POSITION=bottom-right
export BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_LAYOUT=edge
export BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_SHAPE=rounded
export BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_SIZE_PERCENT=28
export BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_INSET_PERCENT=0
export BEEPER_MATRIX_PROXY_MATRIX_AVATAR_CLIENT_PROFILE=cinny
```

Useful profiles:

| Profile | Purpose |
|---|---|
| `cinny` | Rounded-square badge sits close to Cinny's lower-right room-list edge. |
| `element` | Circle-safe badge sits farther inward for Element's round avatar crop. |
| `generic` | Conservative Matrix-client fallback. |
| `beeper-native` / `bipa-native` | Leaves native Beeper profile pictures mostly alone; do not badge Beeper-native clients unnecessarily. |

The messenger service is inferred from Beeper chat/account metadata:

1. `chat.Network`, for example `WhatsApp`, `Signal`, `Telegram`.
2. `chat.AccountID`, for example `whatsapp`, `signal`, `telegram`, `matrix`.
3. Local brand icons from `beepersource/assets/brand-icons/manifest.json`.
4. Optional local overrides from `BEEPER_MATRIX_PROXY_BRAND_ICON_DIR`.

That means the Matrix image can be reused as the badge source for Matrix/proxy
rooms, while WhatsApp/Signal/Telegram rooms keep their own service badge.

## iPhone / Mobile Beeper Caveat

This helper patches only the local macOS Beeper Desktop renderer. It does not
sync the left-rail icon to Beeper on iPhone.

For mobile visibility, prefer Matrix-native room avatars (`m.room.avatar`) via
the avatar sync. Those avatars are server-side Matrix state, so Matrix clients
on desktop or mobile can render the same generated profile/group image plus
messenger badge without a local Electron patch.
