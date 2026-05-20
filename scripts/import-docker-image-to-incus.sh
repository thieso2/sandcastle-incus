#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
  echo "usage: $0 IMAGE_REF ALIAS [REMOTE]" >&2
  exit 2
fi

image_ref="$1"
alias_name="$2"
remote="${3:-${SANDCASTLE_REMOTE:-local}}"

for tool in docker incus tar xz; do
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
docker export "$container_id" | xz -T0 -c >"$tmpdir/rootfs.tar.xz"

serial="$(date -u +%Y%m%d%H%M%S)"
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

tar -C "$tmpdir" -cJf "$tmpdir/metadata.tar.xz" metadata.yaml

incus image import "$tmpdir/metadata.tar.xz" "$tmpdir/rootfs.tar.xz" "$remote:" \
  --alias "$alias_name" \
  --reuse
