# Ubuntu, deliberately not FROM sandcastle/base:latest (Debian) — the Dev
# Image is a distinct OS lineage from base/ai, per the wish.
#
# Every step below delegates to provision.sh, which is the single recipe for
# this template: the same stages run natively inside a live Sandcastle machine
# via scripts/build-image-in-project.sh, whose `sc image save` publish produces
# a system-container image (systemd as PID 1) rather than the application-
# container image an OCI build yields. One recipe, two engines — see
# provision.sh's header and docs/adr/0024-dev-image-third-machine-template.md.
# Layer boundaries are unchanged: one RUN per stage, same cache mounts.
FROM ubuntu:26.04

ENV DEBIAN_FRONTEND=noninteractive

# provision.sh is copied alone, and before the heavy apt layer, so editing any
# other context file (zshrc, starship.toml, …) does not invalidate it.
COPY provision.sh /usr/local/sbin/sandcastle-dev-provision
RUN chmod 0755 /usr/local/sbin/sandcastle-dev-provision

# apt cache mounts keep downloaded .debs and package lists on the builder
# cache volume (not in the image layer), so a rebuild of this heavy layer
# reuses the local archive instead of re-fetching. The `packages` stage also
# removes docker-clean so apt keeps its archives; its absence persists into
# all later layers. See provision.sh for the package list rationale.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    sandcastle-dev-provision packages

# The rest of the context (config files + install-ai-cli-tools.sh) lands at the
# fixed path provision.sh falls back to when it is not run from a context dir.
COPY . /usr/local/share/sandcastle-dev

# Tool-version args are declared here (after the toolchain layer) so a
# version bump rebuilds only the npm layer below, not the apt layer above.
# install-ai-cli-tools.sh is shared with images/ai/Dockerfile (byte-identical
# copy — see its header comment) so the two templates never drift on how the
# AI CLI toolchain gets installed. Unlike ai, dev does not install
# @google/gemini-cli (wish §6 names only Claude Code + Codex).
ARG CODEX_CLI_VERSION
ARG CLAUDE_CODE_VERSION
RUN test -n "${CODEX_CLI_VERSION}" && test -n "${CLAUDE_CODE_VERSION}"

RUN --mount=type=cache,target=/root/.npm,sharing=locked \
    sandcastle-dev-provision ai "${CODEX_CLI_VERSION}" "${CLAUDE_CODE_VERSION}"

# Matt Pocock's agent skills, into /etc/skel so every tenant user created by
# cloud-init's `useradd --create-home` inherits them under ~/.claude/skills.
ARG SKILLS_VERSION=latest
RUN --mount=type=cache,target=/root/.npm,sharing=locked \
    sandcastle-dev-provision skills "${SKILLS_VERSION}"

# starship (§4), mise + its pre-resolved toolchain (§5), and the per-user
# dotfiles (§3/§7/§8) — all baked under /etc/skel, never /etc/zsh/zshrc, which
# belongs to the /.sc shim's append:true cloud-init step (ADR-0022).
RUN sandcastle-dev-provision shell

ARG SANDCASTLE_IMAGE_TEMPLATE=dev
ARG SANDCASTLE_IMAGE_TAG=sandcastle/dev:latest
ARG SANDCASTLE_IMAGE_VERSION=unknown
ARG SANDCASTLE_IMAGE_COMMIT_DATE=unknown
RUN sandcastle-dev-provision stamp \
        "${SANDCASTLE_IMAGE_TEMPLATE}" \
        "${SANDCASTLE_IMAGE_TAG}" \
        "${SANDCASTLE_IMAGE_VERSION}" \
        "${SANDCASTLE_IMAGE_COMMIT_DATE}"

WORKDIR /workspace
CMD ["sleep", "infinity"]
