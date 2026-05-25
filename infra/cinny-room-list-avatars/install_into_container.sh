#!/usr/bin/env sh
set -eu

container="${1:-openclaw-cinny-e2e}"
script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
script_name="openclaw-room-list-avatars.js"
target="/app/${script_name}"

docker cp "${script_dir}/${script_name}" "${container}:${target}"

docker exec "${container}" sh -lc "
  set -eu
  tag='<script src=\"/${script_name}\" defer></script>'
  if ! grep -Fq '/${script_name}' /app/index.html; then
    sed -i \"s#</body>#  \${tag}\\n  </body>#\" /app/index.html
  fi
  grep -F '/${script_name}' /app/index.html >/dev/null
"

echo "Installed ${script_name} into ${container}"
