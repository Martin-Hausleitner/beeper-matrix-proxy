# Brand Icon Source Audit - 2026-05-25

## Decision

Use real app or first-party web-app icons before generated/simple fallbacks.

Priority order:

1. Official brand kit or brand resource page.
2. App Store app artwork for actual native chat apps.
3. First-party `apple-touch-icon` or PWA manifest icon from the official platform domain.
4. Simple Icons only when no better public icon endpoint exists.
5. Local generated fallback is not acceptable for production unless explicitly marked as a missing-asset placeholder.

## Applied

- WhatsApp, Signal, Telegram, Messenger, Instagram, Discord, Slack, X, LinkedIn, and Beeper now use App Store artwork fetched via the Apple iTunes Lookup API by exact bundle id.
- Creator/adult platform icons were refreshed from official homepages, `apple-touch-icon`, PWA manifests, or explicit brand pages where available.
- The PNG assets are normalized to 256x256 and transparent rounded app-icon corners.
- `infra/avatar-configurator/icon-manifest.js` was regenerated from the real manifest so the local file-based configurator uses the same icon metadata.

## Important Licensing Notes

- App icons and brand marks remain trademarks/copyrighted assets of their owners.
- The checked-in assets are for nominative/internal UI reference in this Matrix/Beeper badge context.
- For public distribution, review each vendor's brand terms before release, especially Meta/WhatsApp/Instagram/Messenger, Apple/iMessage, X, LinkedIn, Slack, Discord, OnlyFans, and other creator platforms.

## Source Highlights

- Signal publishes downloadable Brand Assets and says to use only the official Signal logo from its brand assets page.
- Telegram's press page links to Telegram logos and permits use for article illustrations, graphs, and Telegram buttons as long as it is clear the user is not representing Telegram officially.
- Matrix publishes official branding/assets and asks for trademark attribution/disclaimer.
- Discord publishes a brand kit and says not to edit, distort, recolor, or reconfigure its logo.
- Slack publishes a media kit and brand center for logos/assets under its guidelines.
- X publishes a brand toolkit and trademark guideline flow.
- LinkedIn publishes downloadable logos subject to its brand/user agreements.

## Known Weak Spots

- OnlyFans: no clean public brand kit was found; the current icon is still based on Simple Icons and should be treated as a fallback.
- Unlockd: official domain/assets were ambiguous in research; current source should be manually verified before public release.
- RevealMe/Reveal: naming/domain ambiguity remains; current source uses `www.reveal.me`.
- Meta assets may require login/accepting terms on Meta's brand resources pages; App Store artwork is visually accurate but does not replace legal review.
