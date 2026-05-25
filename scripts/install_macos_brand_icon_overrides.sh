#!/usr/bin/env bash
set -euo pipefail

out_dir="${BEEPER_MATRIX_PROXY_BRAND_ICON_DIR:-$HOME/Library/Application Support/matrix-archive-sync/brand-icons}"
mkdir -p "$out_dir"

convert_icon() {
  local key="$1"
  local app="$2"
  local icon="$3"
  local src="$app/Contents/Resources/$icon"
  local dst="$out_dir/$key.png"

  if [[ ! -f "$src" ]]; then
    echo "skip $key: missing $src" >&2
    return 0
  fi

  sips -Z 256 -s format png "$src" --out "$dst" >/dev/null
  chmod 0600 "$dst"
  echo "$key -> $dst"
}

convert_icon "whatsapp" "/Applications/WhatsApp.app" "AppIcon.icns"
convert_icon "signal" "/Applications/Signal.app" "icon.icns"
convert_icon "telegram" "/Applications/Telegram.app" "AppIcon.icns"
convert_icon "beeper" "/Applications/Beeper Desktop.app" "icon.icns"
convert_icon "discord" "/Applications/Discord.app" "electron.icns"
convert_icon "email" "/System/Applications/Mail.app" "ApplicationIcon.icns"
