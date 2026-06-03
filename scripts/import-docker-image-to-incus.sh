#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
  echo "usage: $0 IMAGE_REF ALIAS [REMOTE]" >&2
  exit 2
fi

image_ref="$1"
alias_name="$2"
remote="${3:-${SANDCASTLE_REMOTE:-local}}"

for tool in docker incus tar zstd; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "$tool is required" >&2
    exit 127
  fi
done

arch="$(docker image inspect "$image_ref" --format '{{.Architecture}}')"
case "$arch" in
  amd64) incus_arch="x86_64" ;;
  arm64) incus_arch="aarch64" ;;
  arm) incus_arch="armv7l" ;;
  *)
    echo "unsupported Docker image architecture: $arch" >&2
    exit 1
    ;;
esac

tmpdir="$(mktemp -d)"
container_id=""
cleanup() {
  if [ -n "$container_id" ]; then
    docker rm "$container_id" >/dev/null 2>&1 || true
  fi
  rm -rf "$tmpdir"
}
trap cleanup EXIT

container_id="$(docker create --platform "linux/$arch" "$image_ref" /bin/true)"
echo "Exporting and compressing rootfs..."
docker export "$container_id" | zstd -T0 -c >"$tmpdir/rootfs.tar.zst"
echo "Rootfs size: $(du -sh "$tmpdir/rootfs.tar.zst" | cut -f1)"

serial="$(date -u +%Y%m%d%H%M%S)"
temp_alias="${alias_name}-import-${serial}"
cat >"$tmpdir/metadata.yaml" <<EOF
architecture: "$incus_arch"
creation_date: $(date +%s)
properties:
  architecture: "$incus_arch"
  description: "Sandcastle base image imported from $image_ref"
  os: "debian"
  release: "13"
  serial: "$serial"
  type: "container"
templates: {}
EOF

tar -C "$tmpdir" --zstd -cf "$tmpdir/metadata.tar.zst" metadata.yaml

image_fingerprint() {
  incus image info "$1" 2>/dev/null | awk '/^Fingerprint:/ { print $2; exit }'
}

activate_remote_alias() {
  local old_fingerprint=""
  old_fingerprint="$(image_fingerprint "$remote:$alias_name" || true)"
  if [ -n "$old_fingerprint" ]; then
    incus image alias delete "$remote:$alias_name"
  fi
  if ! incus image alias rename "$remote:$temp_alias" "$alias_name"; then
    if [ -n "$old_fingerprint" ]; then
      incus image alias create "$remote:$alias_name" "$old_fingerprint" || true
    fi
    return 1
  fi
}

remote_host_from_incus() {
  if [ -n "${SANDCASTLE_IMAGE_UPLOAD_SSH_HOST:-}" ]; then
    printf '%s\n' "$SANDCASTLE_IMAGE_UPLOAD_SSH_HOST"
    return 0
  fi
  if ! command -v python3 >/dev/null 2>&1; then
    return 1
  fi
  incus remote list --format json | REMOTE_NAME="$remote" python3 -c '
import json, os, sys, urllib.parse
data = json.load(sys.stdin)
addr = data.get(os.environ["REMOTE_NAME"], {}).get("Addr", "")
print(urllib.parse.urlparse(addr).hostname or "")
'
}

import_via_incus_api() {
  echo "Importing into Incus remote $remote over Incus API..."
  incus image import "$tmpdir/metadata.tar.zst" "$tmpdir/rootfs.tar.zst" "$remote:" \
    --alias "$temp_alias" --public
  activate_remote_alias
}

import_via_ssh() {
  local ssh_target="$1"
  local remote_tmp=""
  echo "Importing into Incus remote $remote via SSH relay $ssh_target..."
  remote_tmp="$(ssh "$ssh_target" 'mktemp -d')"
  scp "$tmpdir/metadata.tar.zst" "$tmpdir/rootfs.tar.zst" "$ssh_target:$remote_tmp/"
  ssh "$ssh_target" \
    "alias_name=$(printf '%q' "$alias_name") temp_alias=$(printf '%q' "$temp_alias") remote_tmp=$(printf '%q' "$remote_tmp") bash -s" <<'REMOTE'
set -euo pipefail
cleanup() {
  rm -rf "$remote_tmp"
}
trap cleanup EXIT

old_fingerprint="$(incus image info "local:$alias_name" 2>/dev/null | awk '/^Fingerprint:/ { print $2; exit }' || true)"
incus image import "$remote_tmp/metadata.tar.zst" "$remote_tmp/rootfs.tar.zst" --alias "$temp_alias" --public
if [ -n "$old_fingerprint" ]; then
  incus image alias delete "local:$alias_name"
fi
if ! incus image alias rename "local:$temp_alias" "$alias_name"; then
  if [ -n "$old_fingerprint" ]; then
    incus image alias create "local:$alias_name" "$old_fingerprint" || true
  fi
  exit 1
fi
REMOTE
}

ssh_host="$(remote_host_from_incus || true)"
ssh_user="${SANDCASTLE_IMAGE_UPLOAD_SSH_USER:-root}"
ssh_target=""
if [ -n "$ssh_host" ] && [ "$remote" != "local" ]; then
  candidate="${ssh_user}@${ssh_host}"
  if ssh -o BatchMode=yes -o ConnectTimeout=5 "$candidate" true >/dev/null 2>&1; then
    ssh_target="$candidate"
  fi
fi

if [ -n "$ssh_target" ]; then
  import_via_ssh "$ssh_target"
else
  import_via_incus_api
fi
echo "Done."
