ARG SANDCASTLE_BASE_IMAGE=sandcastle/base:latest
FROM ${SANDCASTLE_BASE_IMAGE}

ARG CODEX_CLI_VERSION
ARG CLAUDE_CODE_VERSION
ARG GEMINI_CLI_VERSION

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

WORKDIR /workspace
CMD ["/bin/bash", "-l"]
