# Ubuntu, deliberately not FROM sandcastle/base:latest (Debian) — the Dev
# Image is a distinct OS lineage from base/ai, per the wish.
FROM ubuntu:26.04

ENV DEBIAN_FRONTEND=noninteractive

# apt cache mounts keep downloaded .debs and package lists on the builder
# cache volume (not in the image layer), so a rebuild of this heavy layer
# reuses the local archive instead of re-fetching. docker-clean is removed
# once here so apt keeps its archives; its absence persists into all later
# layers. Package list:
#   - minimum tenant plumbing (mirrors images/base/Dockerfile): systemd
#     systemd-sysv openssh-server
#   - wish §2: git gh ripgrep fd-find make zsh
#   - jq: hard dependency of the Claude Code statusline script (§7); base
#     already has it, dev needs it added explicitly
#   - ca-certificates curl gnupg: needed by the curl-based installers used
#     below (mise, starship)
#   - build-essential nodejs npm: not in the wish's package list, but
#     required to install the AI CLIs the same way images/ai does (npm
#     install -g, see install-ai-cli-tools.sh below) — see
#     implementation-notes.md
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
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
# same pattern base uses for its zellij/coredns hand-installs.
RUN ln -sf /usr/bin/fdfind /usr/local/bin/fd

# zsh as the image's system default shell — belt-and-braces, since
# V2DefaultProfileUserData already forces the tenant login user's shell to
# zsh independently of the image (internal/tenant/create_plan_v2.go:43).
RUN sed -i 's|^SHELL=.*|SHELL=/usr/bin/zsh|' /etc/default/useradd \
    && chsh -s /usr/bin/zsh root

RUN install -d /run/sshd /workspace

# Tool-version args are declared here (after the toolchain layer) so a
# version bump rebuilds only the npm layer below, not the apt layer above.
# Shared with images/ai/Dockerfile (images/ai/install-ai-cli-tools.sh is a
# byte-identical copy of this script — see its header comment) so the two
# templates never drift on how the AI CLI toolchain gets installed. Unlike
# ai, dev does not install @google/gemini-cli (wish §6 names only Claude
# Code + Codex).
ARG CODEX_CLI_VERSION
ARG CLAUDE_CODE_VERSION
RUN test -n "${CODEX_CLI_VERSION}" && test -n "${CLAUDE_CODE_VERSION}"

COPY install-ai-cli-tools.sh /usr/local/sbin/install-ai-cli-tools.sh
RUN chmod 0755 /usr/local/sbin/install-ai-cli-tools.sh

RUN --mount=type=cache,target=/root/.npm,sharing=locked \
    /usr/local/sbin/install-ai-cli-tools.sh cli "${CODEX_CLI_VERSION}" "${CLAUDE_CODE_VERSION}"

# Install Matt Pocock's agent skills into /etc/skel so every tenant user
# created by cloud-init's `useradd --create-home` inherits them under
# ~/.claude/skills. HOME drives the `-g` (global) install target.
ARG SKILLS_VERSION=latest
RUN --mount=type=cache,target=/root/.npm,sharing=locked \
    install -d /etc/skel \
    && HOME=/etc/skel /usr/local/sbin/install-ai-cli-tools.sh skills "${SKILLS_VERSION}"

# starship (wish §4), via the official installer, non-interactive.
RUN curl -sS https://starship.rs/install.sh | sh -s -- --yes --bin-dir /usr/local/bin

# mise (wish §5), via the official installer, run under HOME=/etc/skel so it
# lands at /etc/skel/.local/bin and /etc/skel/.local/share/mise — the same
# HOME=/etc/skel trick images/ai/Dockerfile uses for its skills install —
# for every future tenant user (created via `useradd --create-home`) to
# inherit. `mise install` runs at build time under the same HOME so tool
# versions are pre-resolved; no first-boot network fetch.
RUN install -d /etc/skel \
    && HOME=/etc/skel sh -c "curl -fsSL https://mise.run | sh"

COPY mise-config.toml /etc/skel/.config/mise/config.toml
RUN HOME=/etc/skel MISE_YES=1 /etc/skel/.local/bin/mise install

# Global git defaults and aliases; per-tenant identity is populated on first
# login by the hook below (a shared image cannot hold per-tenant identity).
COPY gitconfig /etc/gitconfig
RUN chmod 0644 /etc/gitconfig

# Per-user zsh rc (history config §3, mise/starship activation §5, git
# identity hook §8) baked under /etc/skel — NOT /etc/zsh/zshrc, which is
# reserved for the /.sc shim's append:true cloud-init step (ADR-0022) and
# must not be perturbed by this image.
COPY zshrc /etc/skel/.zshrc
COPY git-identity-hook.sh /etc/skel/.config/dev/git-identity-hook.sh

# Claude Code statusline (§7) and its Codex counterpart, a static config
# stanza rather than a script — see codex-config.toml's header comment for
# why Codex has no equivalent script hook.
COPY starship.toml /etc/skel/.config/starship.toml
COPY statusline-command.sh /etc/skel/.claude/statusline-command.sh
RUN chmod 0755 /etc/skel/.claude/statusline-command.sh
COPY claude-settings.json /etc/skel/.claude/settings.json
COPY codex-config.toml /etc/skel/.codex/config.toml

ARG SANDCASTLE_IMAGE_TEMPLATE=dev
ARG SANDCASTLE_IMAGE_TAG=sandcastle/dev:latest
ARG SANDCASTLE_IMAGE_VERSION=unknown
ARG SANDCASTLE_IMAGE_COMMIT_DATE=unknown
RUN printf 'Sandcastle %s image\nVersion: %s\nImage: %s\nLast commit: %s\n\n' \
        "$SANDCASTLE_IMAGE_TEMPLATE" \
        "$SANDCASTLE_IMAGE_VERSION" \
        "$SANDCASTLE_IMAGE_TAG" \
        "$SANDCASTLE_IMAGE_COMMIT_DATE" \
        >/etc/issue \
    && cp /etc/issue /etc/issue.net

WORKDIR /workspace
CMD ["sleep", "infinity"]
