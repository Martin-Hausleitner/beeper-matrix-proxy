# UI Avatars Offline Vendor

This directory keeps MIT-licensed UI Avatars PHP code for offline reference.
It is not an installed PHP service or a complete, runnable Composer package.
Runtime avatar rendering in this repository does not call `ui-avatars.com`.

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

## Source provenance and excluded fonts

The seven retained generator files match their Git blobs at
[`LasseRafn/php-initial-avatar-generator@1a63213583a8694e781f7476345b096af6a14d08`](https://github.com/LasseRafn/php-initial-avatar-generator/tree/1a63213583a8694e781f7476345b096af6a14d08).
This is a verified source comparison, not a claim about the original import
date. The service directory retains its MIT license and Composer metadata;
its two PHP files differ from the current upstream reference
[`LasseRafn/ui-avatars@369e739c1c94cf33c8630bbf581ac0c3295b0ff6`](https://github.com/LasseRafn/ui-avatars/tree/369e739c1c94cf33c8630bbf581ac0c3295b0ff6).
The exact import revision of those service files remains unverified.

The previously copied 24 font binaries are excluded from this distribution.
They were not covered by the PHP code's MIT notice: the observed font metadata
identified 19 Noto files as OFL-1.1 and two Open Sans files as Apache-2.0; three
Font Awesome files did not contain a license field. Complete matching font
license/provenance material was absent, so no blanket font license is asserted.

The Go renderer does not embed or reference these vendor font files. Its
`initialsFontPaths` function selects an explicitly configured file or existing
application/system fonts. No such external files or runtime configuration were
changed. The PHP references to bundled fonts remain historical source context;
running this reference package would require separately obtaining and reviewing
its dependencies and corresponding font licenses.
