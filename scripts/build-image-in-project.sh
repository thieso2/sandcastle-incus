#!/usr/bin/env bash
# Build a Sandcastle Machine Template inside a Sandcastle project, on the Incus
# host, and publish it from there.
#
# Why this exists (three problems it solves at once):
#
#   1. ARCHITECTURE. The operator's Mac is arm64; Sandcastle hosts are amd64.
#      `mise run image:dev:build-upload` builds under emulation with
#      DOCKER_DEFAULT_PLATFORM=linux/amd64 — slow, and it needs docker locally.
#      Here the build runs in a machine ON the host, so it is natively the
#      host's architecture and no docker is needed on the Mac at all.
#
#   2. IMAGE TYPE. `image build-remote` publishes an OCI image via GHCR, and
#      Incus runs OCI images as *application* containers: PID 1 is the
#      entrypoint and systemd never boots. Sandcastle machines need
#      system-container images. `sc image save` publishes a running machine
#      (ADR-0019), which is a system-container image by construction.
#
#   3. DATA PATH. Nothing round-trips through the operator's laptop: the
#      snapshot-and-publish happens entirely inside the Incus host, and needs
#      no GHCR token, no registry, and no admin credentials — only `sc`.
#
# The recipe itself is images/<template>/provision.sh — the same stages the
# Dockerfile runs, so the two paths cannot drift.
#
# Usage:
#   scripts/build-image-in-project.sh [options]
#
#   --project P     Sandcastle project to build in (default: $SANDCASTLE_BUILD_PROJECT,
#                   else the project `sc` is currently on)
#   --machine M     build machine name (default dev-build)
#   --template T    machine template: dev (default dev)
#   --alias A       published image name (default the template name)
#   --base IMG      image to build from (default per template)
#   --copy-to P1,P2 also copy the published image into these projects
#   --keep          leave the build machine running when done
#   --recreate      delete an existing build machine first
#   --skip-provision  reuse an already-provisioned build machine; save only
set -euo pipefail

SC="${SC:-sc}"
PROJECT="${SANDCASTLE_BUILD_PROJECT:-}"
MACHINE="dev-build"
TEMPLATE="dev"
ALIAS=""
BASE=""
COPY_TO=""
KEEP=0
RECREATE=0
SKIP_PROVISION=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    --project) PROJECT="$2"; shift 2 ;;
    --machine) MACHINE="$2"; shift 2 ;;
    --template) TEMPLATE="$2"; shift 2 ;;
    --alias) ALIAS="$2"; shift 2 ;;
    --base) BASE="$2"; shift 2 ;;
    --copy-to) COPY_TO="$2"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    --recreate) RECREATE=1; shift ;;
    --skip-provision) SKIP_PROVISION=1; shift ;;
    -h|--help) sed -n '2,37p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

ALIAS="${ALIAS:-$TEMPLATE}"

if [ -z "$PROJECT" ]; then
  # Default to the project `sc` is currently on (`sc project switch`), so a bare
  # run builds where the operator is already working. `sc project list` marks it
  # with a leading `*`. Resolved BEFORE SANDCASTLE_PROJECT is exported below,
  # which would otherwise answer this question with its own answer.
  PROJECT="$("$SC" project list 2>/dev/null | awk '/^\*/ { print $2; exit }')"
fi

if [ -z "$PROJECT" ]; then
  echo "could not determine a project: pass --project <name>, set" >&2
  echo "SANDCASTLE_BUILD_PROJECT, or select one with \`sc project switch\`" >&2
  exit 2
fi

case "$TEMPLATE" in
  dev) BASE="${BASE:-images:ubuntu/26.04/cloud}" ;;
  *)
    # base/ai are FROM Debian and are published through the OCI pipeline; only
    # templates carrying a provision.sh can be built natively in a machine.
    echo "template '$TEMPLATE' has no provision.sh recipe; only 'dev' is supported here" >&2
    exit 2
    ;;
esac

CTX_DIR="images/${TEMPLATE}"
if [ ! -f "${CTX_DIR}/provision.sh" ]; then
  echo "missing ${CTX_DIR}/provision.sh — run this from the repo root" >&2
  exit 2
fi

for tool in python3 curl; do
  command -v "$tool" >/dev/null 2>&1 || { echo "$tool is required" >&2; exit 127; }
done

# Every sc call below is scoped to the build project; nothing depends on (or
# disturbs) the operator's currently selected project.
export SANDCASTLE_PROJECT="$PROJECT"

log() { echo "==> $*"; }

npm_latest() {
  curl -sf "https://registry.npmjs.org/$1/latest" |
    python3 -c "import sys,json; print(json.load(sys.stdin)['version'])"
}

json_field() {
  python3 -c "import sys,json; print(json.load(sys.stdin).get('$1',''))"
}

CODEX_VERSION="${SANDCASTLE_CODEX_VERSION:-$(npm_latest @openai/codex)}"
CLAUDE_VERSION="${SANDCASTLE_CLAUDE_VERSION:-$(npm_latest @anthropic-ai/claude-code)}"
SKILLS_VERSION="${SANDCASTLE_SKILLS_VERSION:-latest}"
IMAGE_VERSION="$(git describe --always --dirty 2>/dev/null || echo unknown)"
IMAGE_COMMIT_DATE="$(git log -1 --format=%cI 2>/dev/null || echo unknown)"

log "template ${TEMPLATE} -> project ${PROJECT}, machine ${MACHINE}, alias ${ALIAS}"
log "codex ${CODEX_VERSION}  claude ${CLAUDE_VERSION}  skills ${SKILLS_VERSION}  version ${IMAGE_VERSION}"

# --- the project --------------------------------------------------------------
if "$SC" project list 2>/dev/null | sed 's/^[* ]*//' | grep -qx "$PROJECT"; then
  log "project ${PROJECT} exists"
else
  log "creating project ${PROJECT}"
  "$SC" project create "$PROJECT"
fi

# --- the build machine --------------------------------------------------------
# The link to the Incus host is frequently a relayed tailnet path, so short
# control calls get a couple of retries rather than failing the whole build on
# one dropped connection. The long-running work is detached (see below) and
# never depends on a connection staying up.
sc_retry() {
  attempt=1
  until "$SC" "$@"; do
    if [ "$attempt" -ge 3 ]; then
      echo "sc $* failed after ${attempt} attempts" >&2
      return 1
    fi
    attempt=$((attempt + 1))
    sleep 5
  done
}

in_machine() {
  sc_retry incus exec "$MACHINE" -- "$@"
}

machine_exists() {
  "$SC" incus info "$MACHINE" >/dev/null 2>&1
}

if [ "$RECREATE" = "1" ] && machine_exists; then
  log "deleting existing build machine ${MACHINE}"
  "$SC" delete "$MACHINE" --yes
fi

if machine_exists; then
  log "reusing build machine ${MACHINE}"
  "$SC" incus start "$MACHINE" >/dev/null 2>&1 || true
else
  log "creating build machine ${MACHINE} from ${BASE}"
  # Pointing the configured Dev Image alias at the base makes `sc create` take
  # the Dev Image path (internal/cli/create_v2.go): instance-level cloud-init
  # that keeps the login user, SSH key, sshd and the /.sc shims but skips Caddy
  # and the TLS leaf. The build machine wants no public ingress, and Caddy must
  # not end up baked into the published template.
  SANDCASTLE_DEV_IMAGE="$BASE" "$SC" create "$MACHINE" --image "$BASE"
fi

log "waiting for the machine to accept commands"
deadline=$((SECONDS + 300))
until "$SC" incus exec "$MACHINE" -- true >/dev/null 2>&1; do
  if [ "$SECONDS" -ge "$deadline" ]; then
    echo "build machine ${MACHINE} never became reachable" >&2
    exit 1
  fi
  sleep 3
done

log "waiting for cloud-init to finish"
# A non-zero status means cloud-init finished with errors (a warning here, not
# a failure: the provisioning below is what actually matters).
"$SC" incus exec "$MACHINE" -- cloud-init status --wait ||
  log "warning: cloud-init reported a non-clean status; continuing"

PROVISION_UNIT="sandcastle-provision"

# Runs a provision.sh stage as a detached systemd transient unit and polls for
# completion. The build takes many minutes over a link that may drop; a plain
# `incus exec` would take the whole run down with it, whereas a transient unit
# keeps going and the poll simply reconnects.
run_stage_detached() {
  stage_label="$1"
  shift
  # Not sc_retry: a missing unit makes this fail, and retrying a no-op costs
  # ten seconds per stage.
  "$SC" incus exec "$MACHINE" -- systemctl reset-failed "$PROVISION_UNIT" >/dev/null 2>&1 || true
  sc_retry incus exec "$MACHINE" -- systemd-run \
    --unit "$PROVISION_UNIT" \
    --setenv "SANDCASTLE_IMAGE_TEMPLATE=${TEMPLATE}" \
    --setenv "SANDCASTLE_IMAGE_TAG=${ALIAS}" \
    --setenv "SANDCASTLE_IMAGE_VERSION=${IMAGE_VERSION}" \
    --setenv "SANDCASTLE_IMAGE_COMMIT_DATE=${IMAGE_COMMIT_DATE}" \
    -- "$@" >/dev/null

  log "${stage_label} running in the machine; polling"
  stage_deadline=$((SECONDS + 5400))
  while :; do
    if [ "$SECONDS" -ge "$stage_deadline" ]; then
      echo "${stage_label} did not finish within 90 minutes" >&2
      exit 1
    fi
    state="$("$SC" incus exec "$MACHINE" -- \
      systemctl show -p ActiveState --value "$PROVISION_UNIT" 2>/dev/null || echo unknown)"
    state="$(echo "$state" | tr -d '\r')"
    case "$state" in
      activating|active|unknown) ;;
      *) break ;;
    esac
    # incus exec pads its stdout stream with NULs when the remote command is
    # quiet, so strip them rather than flooding the operator's terminal.
    "$SC" incus exec "$MACHINE" -- \
      journalctl -u "$PROVISION_UNIT" -n 1 --no-pager -o cat 2>/dev/null |
      tr -d '\000' | grep -v '^[[:space:]]*$' | sed 's/^/    /' || true
    sleep 20
  done

  status="$("$SC" incus exec "$MACHINE" -- \
    systemctl show -p ExecMainStatus --value "$PROVISION_UNIT" 2>/dev/null | tr -d '\r')"
  if [ "${status:-1}" != "0" ]; then
    echo "${stage_label} failed (exit ${status:-unknown}); last log lines:" >&2
    "$SC" incus exec "$MACHINE" -- journalctl -u "$PROVISION_UNIT" -n 40 --no-pager -o cat >&2 || true
    exit 1
  fi
  "$SC" incus exec "$MACHINE" -- systemctl reset-failed "$PROVISION_UNIT" >/dev/null 2>&1 || true
  log "${stage_label} finished"
}

if [ "$SKIP_PROVISION" = "1" ]; then
  log "skipping provisioning (--skip-provision)"
else
  # --- the recipe -------------------------------------------------------------
  # Streamed as one tar over exec's stdin rather than `incus file push -r`:
  # a recursive push is one API call per file, and any of them timing out on a
  # relayed link fails the build.
  log "shipping ${CTX_DIR} into the machine"
  in_machine rm -rf "/root/${TEMPLATE}" /root/sandcastle-ctx.tar
  attempt=1
  until tar -cf - -C "$(dirname "$CTX_DIR")" --exclude '*_test.go' "$(basename "$CTX_DIR")" |
    "$SC" incus exec "$MACHINE" -- sh -c 'cat > /root/sandcastle-ctx.tar'; do
    if [ "$attempt" -ge 3 ]; then
      echo "shipping the build context failed after ${attempt} attempts" >&2
      exit 1
    fi
    attempt=$((attempt + 1))
    sleep 5
  done
  in_machine tar -xf /root/sandcastle-ctx.tar -C /root
  in_machine rm -f /root/sandcastle-ctx.tar
  in_machine chmod 0755 \
    "/root/${TEMPLATE}/provision.sh" "/root/${TEMPLATE}/install-ai-cli-tools.sh"

  log "provisioning (this is the long part)"
  run_stage_detached "provision" \
    "/root/${TEMPLATE}/provision.sh" all \
    "$CODEX_VERSION" "$CLAUDE_VERSION" "$SKILLS_VERSION"

  # --- de-personalize ---------------------------------------------------------
  # `sc image save` publishes the whole rootfs, so anything this machine picked
  # up at boot (its login user, the per-instance /.sc shims, cloud-init state)
  # would otherwise ship inside the template.
  log "cleaning build-machine state out of the rootfs"
  run_stage_detached "clean" "/root/${TEMPLATE}/provision.sh" clean
  # The pushed context goes last: `clean` truncates /tmp and logs, not /root.
  in_machine rm -rf "/root/${TEMPLATE}"
fi

# --- publish ------------------------------------------------------------------
log "publishing ${MACHINE} as image '${ALIAS}' in project ${PROJECT}"
# Deliberately NOT retried. A publish that fails client-side is usually still
# running on the server; a second `image save` would take a second `sc-save`
# snapshot on top of it and wedge the first ("dataset is busy" on the ZFS
# snapshot). Re-run with --skip-provision instead, once the server is idle.
if ! "$SC" image save "$MACHINE" "$ALIAS"; then
  echo "publish failed. The server may still be finishing it — check with" >&2
  echo "  SANDCASTLE_PROJECT=${PROJECT} sc image list" >&2
  echo "before re-running with --skip-provision." >&2
  exit 1
fi

# --- optional fan-out ---------------------------------------------------------
if [ -n "$COPY_TO" ]; then
  # The Incus project name is read off the live tenant per target, never
  # derived from the Sandcastle project name.
  remote="$("$SC" incus remote get-default)"
  fingerprint="$("$SC" incus image info "$ALIAS" | awk '/^Fingerprint:/ { print $2; exit }')"
  IFS=',' read -r -a targets <<<"$COPY_TO"
  for target in "${targets[@]}"; do
    target="$(echo "$target" | tr -d '[:space:]')"
    [ -n "$target" ] || continue
    target_incus="$(SANDCASTLE_PROJECT="$target" "$SC" project list --json | json_field incusName)"
    if [ -z "$target_incus" ]; then
      echo "warning: could not resolve the Incus project for '${target}'; skipped" >&2
      continue
    fi
    log "copying ${ALIAS} into ${target} (${target_incus})"
    "$SC" incus image copy "$fingerprint" "${remote}:" \
      --target-project "$target_incus" --alias "$ALIAS" --reuse ||
      echo "warning: copy into '${target}' failed; the image is still available in ${PROJECT}" >&2
  done
fi

# --- teardown -----------------------------------------------------------------
if [ "$KEEP" = "1" ]; then
  log "keeping build machine ${MACHINE} (--keep)"
else
  log "deleting build machine ${MACHINE}"
  sc_retry delete "$MACHINE" --yes
fi

cat <<EOF

Done. Launch a machine from it with:

  SANDCASTLE_PROJECT=${PROJECT} sc create mydev --image ${ALIAS}

To get the Dev Image's no-Caddy/SSH-only treatment, point the configured Dev
Image alias at it for that call:

  SANDCASTLE_DEV_IMAGE=${ALIAS} SANDCASTLE_PROJECT=${PROJECT} sc create mydev --image ${ALIAS}
EOF
