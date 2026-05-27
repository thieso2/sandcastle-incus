#!/usr/bin/env bash
set -euo pipefail

remote="${1:-${SANDCASTLE_REMOTE:-big}}"
if [ "$#" -gt 0 ]; then
  shift
fi

base_image="${SANDCASTLE_BASE_IMAGE:-sandcastle/base:latest}"
ai_image="${SANDCASTLE_AI_IMAGE:-sandcastle/ai:latest}"
project_prefix="${SANDCASTLE_INCUS_PROJECT_PREFIX:-sc}"

for tool in incus python3; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "$tool is required" >&2
    exit 127
  fi
done

if [ "$#" -gt 0 ]; then
  projects=("$@")
else
  mapfile -t projects < <(
    incus project list "$remote:" --format json |
      PROJECT_PREFIX="$project_prefix" python3 -c '
import json, os, sys
prefix = os.environ["PROJECT_PREFIX"] + "-"
for project in json.load(sys.stdin):
    name = project.get("name", "")
    config = project.get("config") or {}
    if name.startswith(prefix) and config.get("features.images") == "true":
        print(name)
'
  )
fi

if [ "${#projects[@]}" -eq 0 ]; then
  echo "No Sandcastle image-enabled projects found on $remote."
  exit 0
fi

sync_alias() {
  local project="$1" alias="$2"
  echo "Syncing $alias into $remote project $project..."
  incus image copy "$remote:$alias" "$remote:" \
    --project default \
    --target-project "$project" \
    --copy-aliases \
    --reuse \
    --mode relay
}

for project in "${projects[@]}"; do
  sync_alias "$project" "$base_image"
  sync_alias "$project" "$ai_image"
done

echo "Done."
