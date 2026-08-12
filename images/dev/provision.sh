#!/bin/sh
# The dev Machine Template's recipe, split into one stage per Dockerfile layer
# so the SAME steps run under two engines:
#
#   - images/dev/Dockerfile (docker/podman) — the OCI path, for GHCR publishing.
#   - scripts/build-image-in-project.sh — provisions a live Sandcastle machine
#     and publishes it with `sc image save` (ADR-0019). That yields a
#     SYSTEM-CONTAINER image (systemd as PID 1), which is what Sandcastle
#     machines actually need; the OCI path yields an application-container
#     image that never boots systemd.
#
# One recipe, two engines — the same split images/ai and images/dev already use
# for install-ai-cli-tools.sh. A change to the dev image must land here, not in
# a Dockerfile RUN body, or the two paths drift.
#
# Usage:
#   provision.sh packages
#   provision.sh ai     <codex-version> <claude-version>
#   provision.sh skills [<skills-version>]
#   provision.sh shell
#   provision.sh stamp  <template> <tag> <version> <commit-date>
#   provision.sh all    <codex-version> <claude-version> [<skills-version>]
#   provision.sh clean
#
# `clean` is for the live-machine path only: it strips what belongs to the build
# machine rather than to the template (its login user, the per-instance /.sc
# shims, cloud-init state). The Dockerfile never calls it — a docker build has
# no login user and no cloud-init to begin with.
set -eu

# The context directory holds this image's config files (zshrc, starship.toml,
# …). The Dockerfile installs this script to /usr/local/sbin and the files to
# /usr/local/share/sandcastle-dev; the live-machine path runs both out of the
# pushed context directory. $SANDCASTLE_DEV_CTX overrides both.
resolve_ctx() {
  if [ -n "${SANDCASTLE_DEV_CTX:-}" ]; then
    printf '%s\n' "$SANDCASTLE_DEV_CTX"
    return
  fi
  script_dir="$(cd "$(dirname "$0")" && pwd)"
  if [ -f "$script_dir/zshrc" ]; then
    printf '%s\n' "$script_dir"
    return
  fi
  printf '%s\n' "/usr/local/share/sandcastle-dev"
}

CTX="$(resolve_ctx)"
SKEL=/etc/skel

log() { echo "[provision] $*"; }

require_ctx_file() {
  if [ ! -f "$CTX/$1" ]; then
    echo "provision.sh: missing $CTX/$1 (context dir wrong?)" >&2
    exit 1
  fi
}

# --- packages -----------------------------------------------------------------
# Mirrors the Dockerfile's apt layer. Package list rationale:
#   - minimum tenant plumbing (mirrors images/base): systemd systemd-sysv
#     openssh-server. Already present on a live machine; apt is a no-op there.
#   - wish §2: git gh ripgrep fd-find make zsh
#   - jq: hard dependency of the Claude Code statusline script (§7)
#   - ca-certificates curl gnupg: needed by the curl-based installers below
#   - build-essential nodejs npm: required to npm install -g the AI CLIs
stage_packages() {
  log "apt packages"
  export DEBIAN_FRONTEND=noninteractive
  # Only present in docker images; harmless elsewhere. Removed so the
  # Dockerfile's apt cache mount keeps its downloaded archives.
  rm -f /etc/apt/apt.conf.d/docker-clean
  apt-get update
  apt-get install -y --no-install-recommends \
    build-essential \
    ca-certificates \
    curl \
    fd-find \
    gh \
    git \
    gnupg \
    jq \
    make \
    nodejs \
    npm \
    openssh-server \
    ripgrep \
    systemd \
    systemd-sysv \
    zsh

  # fd-find installs its binary as `fdfind` (Debian/Ubuntu packaging quirk),
  # the same pattern images/base uses for its hand-installed tools.
  ln -sf /usr/bin/fdfind /usr/local/bin/fd

  # zsh as the image's system default shell — belt-and-braces, since
  # V2DefaultProfileUserData already forces the tenant login user's shell to
  # zsh independently of the image (internal/tenant/create_plan_v2.go).
  sed -i 's|^SHELL=.*|SHELL=/usr/bin/zsh|' /etc/default/useradd
  chsh -s /usr/bin/zsh root

  install -d /run/sshd /workspace

  # Make `ping` work for a normal user in an unprivileged container.
  # Ubuntu ships no cap_net_raw on /usr/bin/ping and relies on unprivileged
  # ICMP datagram sockets instead, gated by net.ipv4.ping_group_range. systemd's
  # own /usr/lib/sysctl.d/50-default.conf tries to open that up with
  # `-net.ipv4.ping_group_range = 0 2147483647`, but gid 2147483647 is not
  # mapped into the container's user namespace, so the kernel rejects the write
  # with EINVAL — and the leading `-` makes systemd-sysctl ignore the failure.
  # The value stays at its restrictive 65534 65534 and every unprivileged ping
  # fails with "missing cap_net_raw+p capability or setuid?". A range inside the
  # container's gid map takes effect; 99- sorts after 50-default.conf.
  install -d /etc/sysctl.d
  cat >/etc/sysctl.d/99-sandcastle-ping.conf <<'SYSCTL'
# Unprivileged ICMP for every user; the upper bound must stay inside the
# container's mapped gid range or the kernel rejects the write (EINVAL).
net.ipv4.ping_group_range = 0 65534
SYSCTL
}

# --- ai -----------------------------------------------------------------------
# Claude Code + Codex only. No Gemini: the dev template's deliberate
# non-parity with images/ai (ADR-0024 decision 2).
stage_ai() {
  codex_version="${1:?codex version required}"
  claude_version="${2:?claude version required}"
  log "AI CLIs: codex ${codex_version}, claude-code ${claude_version}"
  require_ctx_file install-ai-cli-tools.sh
  sh "$CTX/install-ai-cli-tools.sh" cli "$codex_version" "$claude_version"
}

# --- skills -------------------------------------------------------------------
# Installed into /etc/skel so every tenant user created by cloud-init's
# `useradd --create-home` inherits them under ~/.claude/skills. HOME drives the
# `-g` (global) install target.
stage_skills() {
  skills_version="${1:-latest}"
  log "community skills: ${skills_version}"
  require_ctx_file install-ai-cli-tools.sh
  install -d "$SKEL"
  HOME="$SKEL" sh "$CTX/install-ai-cli-tools.sh" skills "$skills_version"
}

# --- shell --------------------------------------------------------------------
# starship, mise, and the per-user dotfiles — all baked under /etc/skel, NOT
# /etc/zsh/zshrc, which is reserved for the /.sc shim's append:true cloud-init
# step (ADR-0022) and must not be perturbed by this image.
stage_shell() {
  log "starship"
  curl -sS https://starship.rs/install.sh | sh -s -- --yes --bin-dir /usr/local/bin

  log "mise + toolchain"
  install -d "$SKEL"
  # Run the installer under HOME=/etc/skel so mise lands at
  # /etc/skel/.local/bin and /etc/skel/.local/share/mise for every future
  # tenant user to inherit. `mise install` runs here too, so tool versions are
  # pre-resolved — no first-boot network fetch.
  HOME="$SKEL" sh -c "curl -fsSL https://mise.run | sh"
  require_ctx_file mise-config.toml
  install -D -m 0644 "$CTX/mise-config.toml" "$SKEL/.config/mise/config.toml"
  HOME="$SKEL" MISE_YES=1 "$SKEL/.local/bin/mise" install

  log "dotfiles"
  # Global git defaults and aliases; per-tenant identity is populated on first
  # login by the hook below (a shared image cannot hold per-tenant identity).
  install -D -m 0644 "$CTX/gitconfig" /etc/gitconfig
  install -D -m 0644 "$CTX/zshrc" "$SKEL/.zshrc"
  install -D -m 0755 "$CTX/git-identity-hook.sh" "$SKEL/.config/dev/git-identity-hook.sh"
  install -D -m 0644 "$CTX/starship.toml" "$SKEL/.config/starship.toml"
  install -D -m 0755 "$CTX/statusline-command.sh" "$SKEL/.claude/statusline-command.sh"
  install -D -m 0644 "$CTX/claude-settings.json" "$SKEL/.claude/settings.json"
  install -D -m 0644 "$CTX/codex-config.toml" "$SKEL/.codex/config.toml"
}

# --- stamp --------------------------------------------------------------------
stage_stamp() {
  template="${1:-dev}"
  tag="${2:-sandcastle/dev:latest}"
  version="${3:-unknown}"
  commit_date="${4:-unknown}"
  log "stamp /etc/issue (${version})"
  printf 'Sandcastle %s image\nVersion: %s\nImage: %s\nLast commit: %s\n\n' \
    "$template" "$version" "$tag" "$commit_date" >/etc/issue
  cp /etc/issue /etc/issue.net
}

# --- clean --------------------------------------------------------------------
# Live-machine path only. Everything here exists because `sc image save`
# publishes a machine's whole rootfs, so anything the BUILD machine acquired at
# boot would otherwise ship inside the template.
SC_SHIM_MARKER='Sandcastle /.sc shim'

stage_clean() {
  # 1. The build machine's login user. A child machine's cloud-init finds an
  #    already-present account, skips creation, and so never populates a home
  #    from /etc/skel — the whole interactive environment would be missing.
  for u in $(awk -F: '$3 >= 1000 && $3 < 65534 { print $1 }' /etc/passwd); do
    log "clean: removing build-machine login user ${u}"
    pkill -KILL -u "$u" 2>/dev/null || true
    userdel -r "$u" 2>/dev/null || userdel "$u" 2>/dev/null || true
  done

  # 2. The /.sc shims (ADR-0022). Cloud-init re-bakes them per instance — the
  #    rc snippets with `append: true` — so a copy baked into the template
  #    would be duplicated on every child. The shim block is appended last,
  #    hence "print until the marker".
  for rc in /etc/zsh/zshrc /etc/bash.bashrc; do
    [ -f "$rc" ] || continue
    if grep -q "$SC_SHIM_MARKER" "$rc" 2>/dev/null; then
      log "clean: stripping the /.sc shim from ${rc}"
      awk -v marker="$SC_SHIM_MARKER" 'index($0, marker) { exit } { print }' "$rc" >"$rc.provision" \
        && mv "$rc.provision" "$rc"
    fi
  done
  rm -f /etc/ssh/sshrc

  # 3. cloud-init state, so the child runs its own per-instance modules from a
  #    clean slate rather than inheriting this machine's instance directory.
  if command -v cloud-init >/dev/null 2>&1; then
    log "clean: cloud-init clean --logs"
    cloud-init clean --logs >/dev/null 2>&1 || true
  fi

  # 4. Build residue. SSH host keys and /etc/machine-id are deliberately NOT
  #    touched: sandcastle-generalize regenerates both on the child's first
  #    boot (ADR-0019 decision 4), which is the one place that logic lives.
  rm -rf /var/lib/apt/lists/* /root/.npm /tmp/* /var/tmp/*
  find /var/log -type f -exec truncate -s 0 {} + 2>/dev/null || true
  log "clean: done"
}

action="${1:?usage: provision.sh packages|ai|skills|shell|stamp|all|clean ...}"
shift

case "$action" in
  packages) stage_packages ;;
  ai)       stage_ai "$@" ;;
  skills)   stage_skills "$@" ;;
  shell)    stage_shell ;;
  stamp)    stage_stamp "$@" ;;
  clean)    stage_clean ;;
  all)
    codex_version="${1:?codex version required}"
    claude_version="${2:?claude version required}"
    skills_version="${3:-latest}"
    stage_packages
    stage_ai "$codex_version" "$claude_version"
    stage_skills "$skills_version"
    stage_shell
    stage_stamp "${SANDCASTLE_IMAGE_TEMPLATE:-dev}" \
      "${SANDCASTLE_IMAGE_TAG:-sandcastle/dev:latest}" \
      "${SANDCASTLE_IMAGE_VERSION:-unknown}" \
      "${SANDCASTLE_IMAGE_COMMIT_DATE:-unknown}"
    ;;
  *)
    echo "provision.sh: unknown stage '${action}'" >&2
    exit 1
    ;;
esac
