ARG SANDCASTLE_BASE_IMAGE=sandcastle/base:latest
FROM ${SANDCASTLE_BASE_IMAGE}

ARG CODEX_CLI_VERSION
ARG CLAUDE_CODE_VERSION
ARG GEMINI_CLI_VERSION
ARG SANDCASTLE_IMAGE_TEMPLATE=ai
ARG SANDCASTLE_IMAGE_TAG=sandcastle/ai:latest
ARG SANDCASTLE_IMAGE_VERSION=unknown
ARG SANDCASTLE_IMAGE_COMMIT_DATE=unknown

ENV DEBIAN_FRONTEND=noninteractive

RUN test -n "${CODEX_CLI_VERSION}" \
    && test -n "${CLAUDE_CODE_VERSION}" \
    && test -n "${GEMINI_CLI_VERSION}"

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

RUN npm install -g \
        "@openai/codex@${CODEX_CLI_VERSION}" \
        "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
        "@google/gemini-cli@${GEMINI_CLI_VERSION}"

RUN printf 'Sandcastle %s image\nVersion: %s\nImage: %s\nLast commit: %s\n\n' \
        "$SANDCASTLE_IMAGE_TEMPLATE" \
        "$SANDCASTLE_IMAGE_VERSION" \
        "$SANDCASTLE_IMAGE_TAG" \
        "$SANDCASTLE_IMAGE_COMMIT_DATE" \
        >/etc/issue \
    && cp /etc/issue /etc/issue.net

WORKDIR /workspace
CMD ["sleep", "infinity"]
