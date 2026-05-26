#!/usr/bin/env sh
set -eu

container="${1:-openclaw-cinny-e2e}"
script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repo_dir="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
script_name="openclaw-room-list-avatars.js"
script_version="9"
target="/app/assets/${script_name}"
icon_target="/app/assets/openclaw-brand-icons"

docker cp "${script_dir}/${script_name}" "${container}:${target}"
docker exec "${container}" sh -lc "mkdir -p '${icon_target}'"
docker cp "${repo_dir}/beepersource/assets/brand-icons/icons/." "${container}:${icon_target}/"

docker exec "${container}" sh -lc "
  set -eu
  tag='<script src=\"/assets/${script_name}?v=${script_version}\" defer></script>'
  sed -i '/<script src=\"\\/${script_name}\" defer><\\/script>/d' /app/index.html
  sed -i '/<script src=\"\\/assets\\/${script_name}/d' /app/index.html
  if ! grep -Fq '/assets/${script_name}' /app/index.html; then
    sed -i \"s#</body>#  \${tag}\\n  </body>#\" /app/index.html
  fi
  grep -F '/assets/${script_name}' /app/index.html >/dev/null
"

echo "Installed ${script_name} into ${container}"
