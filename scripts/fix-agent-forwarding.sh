#!/usr/bin/env bash
# Bootstrap machines into the /.sc shared-scripts model (ADR-0022) over
# `incus exec` — the admin recovery floor that works even when SSH/shells on a
# machine are broken.
#
# Machines built before /.sc shipped carry the old inline agent-forwarding
# scripts (or nothing). This installs the STABLE SHIMS only — the same ones the
# v2 default-profile cloud-init bakes on fresh machines:
#   - /etc/ssh/sshrc            sources /.sc/platform/ssh/sshrc then /.sc/local/ssh/sshrc
#   - /etc/zsh/zshrc  (append)  sources /.sc/platform/shell/rc.sh then /.sc/local/shell/rc.sh
#   - /etc/bash.bashrc (append) same, for bash panes
# Each line is `[ -r … ] &&`-guarded: a missing payload or unmounted /.sc is a
# clean no-op, never a lockout. Script BODIES are no longer pushed per machine —
# they live in the shared /.sc platform payload, converged centrally with
#   sc-adm tenant payload-sync <tenant>      (admin)  or
#   sc fix <machine>                          (tenant)
# Run one of those once per tenant; after this bootstrap, later script changes
# reach every machine with no further per-machine touch.
#
# NOTE: machines whose default profile predates /.sc also need the volume
# devices — an idempotent re-provision (tenant re-login, or sc-adm project
# create path) re-renders the profile; a container picks the new mounts up
# live, a VM on its next restart.
#
# It also DETECTS — and does not silently rewrite — a broken hand-rolled
# ~/.zshrc block that republishes through the *consumed* path
# (ssh_auth_sock_known), which self-links and breaks the agent.
#
# Usage:
#   scripts/fix-agent-forwarding.sh --remote big: --project sc2-thieso2-scraper [instance ...]
#   scripts/fix-agent-forwarding.sh --project local-proj              # default remote
#   scripts/fix-agent-forwarding.sh --remote big: --project P --dry-run
#
# With no explicit instance names, every instance in the project except the
# sidecar is targeted. Requires admin incus access (global ~/.config/incus).
set -euo pipefail

REMOTE=""
PROJECT=""
DRY_RUN=0
SIDECAR="sidecar" # naming.V2SidecarInstanceName
INSTANCES=()

usage() { sed -n '2,37p' "$0"; exit "${1:-0}"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --remote) REMOTE="$2"; shift 2 ;;
    --project) PROJECT="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage 0 ;;
    -*) echo "unknown flag: $1" >&2; usage 1 ;;
    *) INSTANCES+=("$1"); shift ;;
  esac
done

[ -n "$PROJECT" ] || { echo "error: --project is required" >&2; usage 1; }

# Normalize remote to a "<name>:" prefix (empty = default remote).
case "$REMOTE" in "" ) : ;; *: ) : ;; * ) REMOTE="${REMOTE}:" ;; esac

incus_target() { printf '%s%s' "$REMOTE" "$1"; }

if [ "${#INSTANCES[@]}" -eq 0 ]; then
  # All instances in the project except the sidecar.
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    [ "$name" = "$SIDECAR" ] && continue
    INSTANCES+=("$name")
  done < <({ [ -n "$REMOTE" ] && incus list "$REMOTE" --project "$PROJECT" -c n --format csv 2>/dev/null; } || \
           incus list --project "$PROJECT" -c n --format csv)
fi

[ "${#INSTANCES[@]}" -gt 0 ] || { echo "no target instances in project $PROJECT" >&2; exit 1; }

# The bootstrap runs as root inside each machine. Idempotent (marker-guarded
# appends); it installs the stable shims only — mirror of
# tenant.SCSSHRCShim / tenant.SCShellRCShim (internal/tenant/platform_payload.go).
read -r -d '' REMOTE_FIXUP <<'FIX' || true
set -eu

if grep -q 'Sandcastle /.sc shim' /etc/ssh/sshrc 2>/dev/null; then
  echo "  = /etc/ssh/sshrc is already the /.sc shim"
else
  cat > /etc/ssh/sshrc <<'EOF'
#!/bin/sh
# Sandcastle /.sc shim (stable) — the logic lives on the /.sc volume (ADR-0022).
[ -r /.sc/platform/ssh/sshrc ] && . /.sc/platform/ssh/sshrc
[ -r /.sc/local/ssh/sshrc ] && . /.sc/local/ssh/sshrc
true
EOF
  chmod 0755 /etc/ssh/sshrc
  echo "  + installed the /etc/ssh/sshrc /.sc shim"
fi

SNIPPET='# Sandcastle /.sc shim (stable) — shell setup lives on the /.sc volume (ADR-0022).
[ -r /.sc/platform/shell/rc.sh ] && . /.sc/platform/shell/rc.sh
[ -r /.sc/local/shell/rc.sh ] && . /.sc/local/shell/rc.sh
true'

for RC in /etc/zsh/zshrc /etc/bash.bashrc; do
  # A missing rc means the shell isn't installed (legacy machines predate the
  # zsh-default profile) — skip rather than pre-create the package's conffile.
  [ -e "$RC" ] || { echo "  - $RC absent (shell not installed), skipped"; continue; }
  if grep -q 'Sandcastle /.sc shim' "$RC" 2>/dev/null; then
    echo "  = $RC already has the /.sc shim"
  else
    printf '\n%s\n' "$SNIPPET" >> "$RC"
    echo "  + appended the /.sc shim to $RC"
  fi
done

# Detect (do not rewrite) a broken hand-rolled ~/.zshrc that republishes via the
# consumed path — the ssh_auth_sock_known self-link trap.
for home in /root /home/*; do
  [ -d "$home" ] || continue
  z="$home/.zshrc"
  if [ -f "$z" ] && grep -q 'ssh_auth_sock_known' "$z" 2>/dev/null; then
    echo "  ! WARNING: $z has a broken hand-rolled agent block (ssh_auth_sock_known);"
    echo "    replace it with the read-only consume snippet by hand (see the script header)."
  fi
  # Clear a stale/self-looped legacy link so the healthy indirection can take over.
  rm -f "$home/.ssh/ssh_auth_sock_known" 2>/dev/null || true
done

if [ -r /.sc/platform/VERSION ]; then
  echo "  ok  /.sc/platform payload $(cat /.sc/platform/VERSION)"
else
  echo "  ! /.sc/platform payload not visible — run: sc-adm tenant payload-sync <tenant>"
  echo "    (and re-provision the profile if this machine predates the /.sc volumes)"
fi

echo "  done"
FIX

echo "Bootstrapping /.sc shims on ${#INSTANCES[@]} instance(s) in project $PROJECT (remote='${REMOTE:-default}')"
rc=0
for inst in "${INSTANCES[@]}"; do
  target="$(incus_target "$inst")"
  echo "== $target =="
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "  (dry-run) would: incus exec $target --project $PROJECT -- sh -s"
    continue
  fi
  if ! printf '%s' "$REMOTE_FIXUP" | incus exec "$target" --project "$PROJECT" -- sh -s; then
    echo "  FAILED on $target" >&2
    rc=1
  fi
done
exit "$rc"
