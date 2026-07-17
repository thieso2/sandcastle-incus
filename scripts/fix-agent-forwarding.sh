#!/usr/bin/env bash
# Backfill the forwarded-SSH-agent indirection onto machines that were built
# before it shipped in the v2 default-profile cloud-init (see
# internal/tenant/create_plan_v2.go, sshAgentForwardWriteFiles).
#
# cloud-init only runs at first boot, so already-running machines never get the
# new write_files. This pushes the same three system files in over `incus exec`
# (root, no SSH/agent needed), idempotently:
#   - /etc/ssh/sshrc         republish each session's forwarded agent at the
#                            stable path ~/.ssh/ssh_auth_sock (sole writer)
#   - /etc/zsh/zshrc  (append)  export SSH_AUTH_SOCK from that link (-h guard)
#   - /etc/bash.bashrc (append) same, for bash panes
# The fix is durable: it takes effect on the next `ssh -A` login into the
# machine (which is when sshd runs sshrc and refreshes the link).
#
# It also DETECTS — and does not silently rewrite — a broken hand-rolled
# ~/.zshrc block that republishes through the *consumed* path
# (ssh_auth_sock_known), which self-links and breaks the agent; those are
# reported for manual review.
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

usage() { sed -n '2,30p' "$0"; exit "${1:-0}"; }

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
  done < <(incus list "${REMOTE%:}:" --project "$PROJECT" -c n --format csv 2>/dev/null || \
           incus list --project "$PROJECT" -c n --format csv)
fi

[ "${#INSTANCES[@]}" -gt 0 ] || { echo "no target instances in project $PROJECT" >&2; exit 1; }

# The fixup runs as root inside each machine. It is idempotent and never
# rewrites a user's personal ~/.zshrc — it only reports a broken one.
read -r -d '' REMOTE_FIXUP <<'FIX' || true
set -eu

cat > /etc/ssh/sshrc <<'EOF'
#!/bin/sh
# Sandcastle: republish this session's forwarded SSH agent at a stable path so
# multiplexer panes (herdr/tmux) that outlive the session keep a live agent.
# Re-point on every session so a new login heals a dangling link.
if [ -n "$SSH_AUTH_SOCK" ] && [ "$SSH_AUTH_SOCK" != "$HOME/.ssh/ssh_auth_sock" ]; then
  mkdir -p "$HOME/.ssh" && chmod 700 "$HOME/.ssh"
  ln -sf "$SSH_AUTH_SOCK" "$HOME/.ssh/ssh_auth_sock"
fi
EOF
chmod 0755 /etc/ssh/sshrc

SNIPPET='# Sandcastle: follow the forwarded agent republished by /etc/ssh/sshrc.
# Guard on -h (symlink present), NOT -S (live socket): a pane opened while the
# link dangles must still point AT the link so the next session heals it.
if [ -h "$HOME/.ssh/ssh_auth_sock" ]; then
  export SSH_AUTH_SOCK="$HOME/.ssh/ssh_auth_sock"
fi'

for RC in /etc/zsh/zshrc /etc/bash.bashrc; do
  [ -e "$RC" ] || continue
  if grep -q 'Sandcastle: follow the forwarded agent' "$RC" 2>/dev/null; then
    echo "  = $RC already has consume snippet"
  else
    printf '\n%s\n' "$SNIPPET" >> "$RC"
    echo "  + appended consume snippet to $RC"
  fi
done

# Detect (do not rewrite) a broken hand-rolled ~/.zshrc that republishes via the
# consumed path — the ssh_auth_sock_known self-link trap.
for home in /root /home/*; do
  [ -d "$home" ] || continue
  z="$home/.zshrc"
  [ -f "$z" ] || continue
  if grep -q 'ssh_auth_sock_known' "$z" 2>/dev/null; then
    echo "  ! WARNING: $z has a broken hand-rolled agent block (ssh_auth_sock_known);"
    echo "    replace it with the read-only consume snippet by hand (see the script header)."
  fi
  # Clear a stale/self-looped legacy link so the healthy indirection can take over.
  rm -f "$home/.ssh/ssh_auth_sock_known" 2>/dev/null || true
done

echo "  done"
FIX

echo "Backfilling agent forwarding on ${#INSTANCES[@]} instance(s) in project $PROJECT (remote='${REMOTE:-default}')"
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
