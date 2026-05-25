# UI Avatars Offline Vendor

This directory vendors the upstream MIT-licensed UI Avatars sources for local,
offline reference. Runtime avatar rendering in this repository does not call
`ui-avatars.com`.

Vendored sources:

- `service/` from `https://github.com/LasseRafn/ui-avatars`
- `php-initial-avatar-generator/` from `https://github.com/LasseRafn/php-initial-avatar-generator`

The Go renderer ports the stable core behavior used by the upstream package
where it matches the Beeper-style design:

- initials are generated locally from the display name
- automatic color identity uses `crc32(name) % 360`
- font size follows the UI Avatars default `0.4375` base factor

Local Beeper-style overrides:

- background colors are rendered as softer pastel gradients
- initials are always white, never black
- group bubbles are scaled to keep visible padding between bubbles

License files are kept next to the vendored source trees.
