---
name: beeper-desktop-bridgev2-icon
description: Use when customizing, checking, restoring, or documenting the native Beeper Desktop sidebar icon for self-hosted bridgev2 accounts with a local image/photo.
---

# Beeper Desktop Bridgev2 Icon

Use the repo script instead of editing Beeper Desktop bundles by hand:

```bash
node scripts/patch_beeper_desktop_bridgev2_icon.cjs --image /absolute/path/to/icon.png
```

## Workflow

1. Run the self-test:

   ```bash
   npm run beeper:bridgev2-icon:self-test
   ```

2. Patch Beeper Desktop with a local image:

   ```bash
   node scripts/patch_beeper_desktop_bridgev2_icon.cjs --image /absolute/path/to/icon.png
   ```

3. Verify the renderer and browser bundles:

   ```bash
   npm run beeper:bridgev2-icon:check
   ```

4. Restart Beeper Desktop:

   ```bash
   osascript -e 'tell application "Beeper Desktop" to quit'
   open -a "Beeper Desktop"
   ```

5. Use Computer Use to inspect the real Beeper window. Confirm the left sidebar
   no longer shows the generic green bridge icon for the `bridgev2` account.

## Notes

- Supported image inputs: PNG, JPG/JPEG, SVG, WebP, GIF, AVIF.
- The image is embedded into a rounded SVG background and cropped with
  `xMidYMid slice`.
- The script writes timestamped backups next to the patched Beeper files.
- Restore the latest backup with:

  ```bash
  npm run beeper:bridgev2-icon:restore
  ```

- Beeper Desktop updates can overwrite the patch. Re-run the script after an
  update.
