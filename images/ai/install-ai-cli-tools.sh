#!/bin/sh
# Shared by images/ai/Dockerfile and images/dev/Dockerfile so the two image
# templates never drift on how the AI CLI toolchain and the community skills
# bundle get installed. Docker's build context is scoped per template
# (images/ai/, images/dev/), so this file cannot be COPY'd from one shared
# location; it is instead kept byte-identical in both directories, enforced
# by images/dev/install_ai_cli_tools_test.go.
#
# Usage:
#   install-ai-cli-tools.sh cli <codex-version> <claude-version> [<gemini-version>]
#   install-ai-cli-tools.sh skills <skills-version>
set -eu

action="${1:?usage: install-ai-cli-tools.sh cli|skills ...}"
shift

case "$action" in
  cli)
    codex_version="${1:?codex version required}"
    claude_version="${2:?claude version required}"
    gemini_version="${3:-}"
    packages="@openai/codex@${codex_version} @anthropic-ai/claude-code@${claude_version}"
    if [ -n "$gemini_version" ]; then
      packages="$packages @google/gemini-cli@${gemini_version}"
    fi
    # shellcheck disable=SC2086
    npm install -g $packages
    ;;
  skills)
    skills_version="${1:-latest}"
    install -d "$HOME"
    npx -y "skills@${skills_version}" add mattpocock/skills -a claude-code -g -y
    ;;
  *)
    echo "install-ai-cli-tools.sh: unknown action '${action}'" >&2
    exit 1
    ;;
esac
