ARG SANDCASTLE_BASE_IMAGE=sandcastle/base:latest
FROM ${SANDCASTLE_BASE_IMAGE}

ENV DEBIAN_FRONTEND=noninteractive

# Toolchain layer: independent of the pinned tool versions, so it stays cached
# across version bumps.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        git \
        gh \
        nodejs \
        npm \
        python3 \
        python3-pip \
    && rm -rf /var/lib/apt/lists/*

# Tool-version args are declared here (after the toolchain layer) so a version
# bump rebuilds only the npm layer below, not the apt layer above.
ARG CODEX_CLI_VERSION
ARG CLAUDE_CODE_VERSION
ARG GEMINI_CLI_VERSION
RUN test -n "${CODEX_CLI_VERSION}" \
    && test -n "${CLAUDE_CODE_VERSION}" \
    && test -n "${GEMINI_CLI_VERSION}"

RUN npm install -g \
        "@openai/codex@${CODEX_CLI_VERSION}" \
        "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
        "@google/gemini-cli@${GEMINI_CLI_VERSION}"

# Install Matt Pocock's agent skills into /etc/skel so every tenant user created
# by `useradd --create-home` (see sandcastle-bootstrap) inherits them under
# ~/.claude/skills. HOME drives the `-g` (global) install target.
RUN install -d /etc/skel \
    && HOME=/etc/skel npx -y skills@latest add mattpocock/skills -a claude-code -g -y

ARG SANDCASTLE_IMAGE_TEMPLATE=ai
ARG SANDCASTLE_IMAGE_TAG=sandcastle/ai:latest
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
