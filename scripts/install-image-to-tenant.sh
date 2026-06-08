#!/usr/bin/env bash
# Install a freshly built Sandcastle image into a tenant's Incus project as a
# bootable system-container image.
#
# Why this exists: `image:all:build-remote` publishes OCI images and imports
# them into the `default` project alias, but Incus runs OCI images as
# application containers (PID 1 = entrypoint, no systemd) — unusable as
# Sandcastle machines. And each tenant project keeps its OWN copy of the image,
# which nothing refreshes. This script bridges both gaps: it takes the image the
# build appliance already built (podman local storage), exports its rootfs
# natively (no ghcr re-pull, no emulation), re-imports it as a system-container
# image (metadata type: container, boots systemd), and points the tenant
# project's alias at it. Existing machines are untouched until recreated.
#
# Usage:
#   scripts/install-image-to-tenant.sh <base|ai> <tenant> [remote-ssh-host]
#
# Env:
#   SANDCASTLE_IMAGE_SSH_HOST  SSH host for the Incus server (default big.thieso2.dev)
#   SANDCASTLE_REMOTE          incus remote name for the server (default big)
#   GHCR_OWNER                 GHCR owner for the podman image (default thieso2)
#   UPDATE_DEFAULT_ALIAS       also repoint the default-project alias (default 1)
set -euo pipefail

template="${1:-}"
tenant="${2:-}"
ssh_host="${3:-${SANDCASTLE_IMAGE_SSH_HOST:-big.thieso2.dev}}"
remote="${SANDCASTLE_REMOTE:-big}"
ghcr_owner="${GHCR_OWNER:-thieso2}"
update_default="${UPDATE_DEFAULT_ALIAS:-1}"

case "${template}" in base|ai) ;; *) echo "usage: $0 <base|ai> <tenant> [ssh-host]" >&2; exit 2 ;; esac
[ -n "${tenant}" ] || { echo "usage: $0 <base|ai> <tenant> [ssh-host]" >&2; exit 2; }

alias_name="sandcastle/${template}:latest"
tenant_project="sc-${tenant}"
podman_ref="ghcr.io/${ghcr_owner}/sandcastle-${template}:latest"
build_project="sc-build"
build_instance="sc-builder"
build_user="build"
build_env="HOME=/home/${build_user} XDG_RUNTIME_DIR=/run/user/1000"

echo ">> installing ${podman_ref} into ${remote}:${tenant_project} as ${alias_name}"

# Everything below runs ON the Incus host so the multi-GB rootfs never crosses
# the network: export from the appliance, pull locally, import, repoint aliases.
ssh "root@${ssh_host}" TEMPLATE="${template}" ALIAS="${alias_name}" \
  TENANT_PROJECT="${tenant_project}" PODMAN_REF="${podman_ref}" \
  BUILD_PROJECT="${build_project}" BUILD_INSTANCE="${build_instance}" \
  BUILD_USER="${build_user}" BUILD_ENV="${build_env}" \
  UPDATE_DEFAULT="${update_default}" 'bash -s' <<'REMOTE'
set -euo pipefail
work="$(mktemp -d /var/tmp/sc-image.XXXXXX)"
rootfs="${work}/rootfs.tar"
# NOTE: this script is fed to `bash -s` over ssh, so its commands share stdin
# with the heredoc. `incus exec` forwards stdin to the instance and would eat the
# rest of the script, so every incus invocation below reads from /dev/null.
trap 'rm -rf "${work}"; incus exec "${BUILD_INSTANCE}" --project "${BUILD_PROJECT}" -- sh -c "rm -f /home/'"${BUILD_USER}"'/sc-export.tar" </dev/null 2>/dev/null || true' EXIT

echo ">> [appliance] podman export ${PODMAN_REF}"
incus exec "${BUILD_INSTANCE}" --project "${BUILD_PROJECT}" -- sh -c \
  "cd /home/${BUILD_USER} && runuser -u ${BUILD_USER} -- env ${BUILD_ENV} sh -c '
     cid=\$(podman create ${PODMAN_REF} /bin/true) &&
     podman export \$cid -o /home/${BUILD_USER}/sc-export.tar &&
     podman rm \$cid >/dev/null'" </dev/null

echo ">> pull rootfs onto host"
incus file pull "${BUILD_INSTANCE}/home/${BUILD_USER}/sc-export.tar" "${rootfs}" --project "${BUILD_PROJECT}" </dev/null

echo ">> build system-container metadata"
serial="$(date -u +%Y%m%d%H%M%S)"
cat >"${work}/metadata.yaml" <<EOF
architecture: "x86_64"
creation_date: $(date +%s)
properties:
  architecture: "x86_64"
  description: "Sandcastle ${TEMPLATE} image (system container) ${serial}"
  os: "debian"
  release: "13"
  serial: "${serial}"
  type: "container"
templates: {}
EOF
tar -C "${work}" -cf "${work}/metadata.tar" metadata.yaml

tmp_alias="${ALIAS}-install-${serial}"
echo ">> import as system-container image"
incus image import "${work}/metadata.tar" "${rootfs}" --project default --alias "${tmp_alias}" </dev/null
fp="$(incus image info "local:${tmp_alias}" --project default </dev/null | awk '/^Fingerprint/{print $2; exit}')"
incus image alias delete "local:${tmp_alias}" --project default
echo ">> new image fingerprint: ${fp}"

repoint() { # project
  local proj="$1"
  incus image alias delete "local:${ALIAS}" --project "${proj}" </dev/null 2>/dev/null || true
}

if [ "${UPDATE_DEFAULT}" = "1" ]; then
  echo ">> repoint default:${ALIAS} -> ${fp}"
  repoint default
  incus image alias create "local:${ALIAS}" "${fp}" --project default </dev/null
fi

echo ">> copy into ${TENANT_PROJECT} and repoint ${ALIAS}"
repoint "${TENANT_PROJECT}"
incus image copy "local:${fp}" local: --project default --target-project "${TENANT_PROJECT}" --alias "${ALIAS}" </dev/null

echo ">> done. ${TENANT_PROJECT}:${ALIAS}:"
incus image list local: --project "${TENANT_PROJECT}" </dev/null 2>/dev/null | grep -F "${fp:0:12}" || true
REMOTE

echo ">> installed ${alias_name} into ${tenant_project}. Recreate machines (sc delete / sc create) to pick it up."
