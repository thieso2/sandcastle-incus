#!/usr/bin/env bash
set -euo pipefail

INSTANCE="${1:-workvm}"
WORKVM_USER="${WORKVM_USER:-thies}"
SSH_PUBLIC_KEY_FILE="${SSH_PUBLIC_KEY_FILE:-$HOME/.ssh/id_ed25519.pub}"
TAILSCALE_HOSTNAME="${TAILSCALE_HOSTNAME:-$INSTANCE}"
SKILLS_SOURCE_DIR="${SKILLS_SOURCE_DIR:-}"

if ! command -v incus >/dev/null 2>&1; then
  echo "incus is required on the host" >&2
  exit 127
fi

if [[ ! -r "$SSH_PUBLIC_KEY_FILE" ]]; then
  echo "SSH public key not readable: $SSH_PUBLIC_KEY_FILE" >&2
  exit 1
fi

if [[ -z "$SKILLS_SOURCE_DIR" ]]; then
  if [[ -d "$HOME/.agents/skills" ]]; then
    SKILLS_SOURCE_DIR="$HOME/.agents/skills"
  elif [[ -d "$HOME/.agent/skills" ]]; then
    SKILLS_SOURCE_DIR="$HOME/.agent/skills"
  fi
fi

if [[ -z "$SKILLS_SOURCE_DIR" || ! -d "$SKILLS_SOURCE_DIR" ]]; then
  echo "Skills source directory not found. Set SKILLS_SOURCE_DIR or create ~/.agents/skills." >&2
  exit 1
fi

SSH_PUBLIC_KEY="$(<"$SSH_PUBLIC_KEY_FILE")"
REMOTE_DIR="/root/workvm-setup"
REMOTE_SCRIPT="$REMOTE_DIR/bootstrap.sh"
REMOTE_ENV="$REMOTE_DIR/bootstrap.env"
REMOTE_SKILLS_ARCHIVE="$REMOTE_DIR/skills.tgz"
TMP_ENV="$(mktemp)"
TMP_SCRIPT="$(mktemp)"
TMP_SKILLS_ARCHIVE="$(mktemp)"

cleanup() {
  rm -f "$TMP_ENV" "$TMP_SCRIPT" "$TMP_SKILLS_ARCHIVE"
  incus exec "$INSTANCE" -- rm -f "$REMOTE_ENV" "$REMOTE_SKILLS_ARCHIVE" >/dev/null 2>&1 || true
}
trap cleanup EXIT

cat >"$TMP_ENV" <<EOF_ENV
WORKVM_USER=$(printf '%q' "$WORKVM_USER")
SSH_PUBLIC_KEY=$(printf '%q' "$SSH_PUBLIC_KEY")
TAILSCALE_HOSTNAME=$(printf '%q' "$TAILSCALE_HOSTNAME")
TAILSCALE_AUTHKEY=$(printf '%q' "${TAILSCALE_AUTHKEY:-}")
EOF_ENV
chmod 600 "$TMP_ENV"

COPYFILE_DISABLE=1 tar --no-xattrs -C "$SKILLS_SOURCE_DIR" -chzf "$TMP_SKILLS_ARCHIVE" .

cat >"$TMP_SCRIPT" <<'EOF_SCRIPT'
#!/usr/bin/env bash
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive

log() {
  printf '\n==> %s\n' "$*"
}

require_supported_os() {
  . /etc/os-release
  case "${ID}:${VERSION_CODENAME}" in
    ubuntu:jammy|ubuntu:noble|ubuntu:resolute|debian:bookworm|debian:trixie)
      ;;
    *)
      echo "Unsupported OS for Zabbly Incus packages: ${PRETTY_NAME:-$ID $VERSION_CODENAME}" >&2
      exit 1
      ;;
  esac
}

install_base_packages() {
  log "Installing base packages"
  apt-get update
  apt-get install -y \
    bash-completion \
    ca-certificates \
    curl \
    git \
    gnupg \
    jq \
    nodejs \
    npm \
    openssh-server \
    ripgrep \
    sudo \
    wget \
    zsh
}

install_zabbly_incus_repo() {
  log "Configuring Zabbly Incus stable repository"
  . /etc/os-release
  install -d -m 0755 /etc/apt/keyrings /etc/apt/sources.list.d
  curl -fsSL https://pkgs.zabbly.com/key.asc -o /etc/apt/keyrings/zabbly.asc
  chmod 0644 /etc/apt/keyrings/zabbly.asc

  local fprs
  fprs="$(gpg --show-keys --with-colons /etc/apt/keyrings/zabbly.asc | awk -F: '$1 == "fpr" { print $10 }')"
  if ! grep -qx '4EFC590696CB15B87C73A3AD82CC8797C838DCFD' <<<"$fprs"; then
    echo "Unexpected Zabbly key fingerprint" >&2
    exit 1
  fi

  cat >/etc/apt/sources.list.d/zabbly-incus-stable.sources <<EOF_ZABBLY
Enabled: yes
Types: deb
URIs: https://pkgs.zabbly.com/incus/stable
Suites: ${VERSION_CODENAME}
Components: main
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/zabbly.asc

EOF_ZABBLY
}

install_github_cli_repo() {
  log "Configuring GitHub CLI repository"
  install -d -m 0755 /etc/apt/keyrings /etc/apt/sources.list.d
  curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
    -o /etc/apt/keyrings/githubcli-archive-keyring.gpg
  chmod 0644 /etc/apt/keyrings/githubcli-archive-keyring.gpg

  local fprs
  fprs="$(gpg --show-keys --with-colons /etc/apt/keyrings/githubcli-archive-keyring.gpg | awk -F: '$1 == "fpr" { print $10 }')"
  if ! grep -qx '2C6106201985B60E6C7AC87323F3D4EA75716059' <<<"$fprs"; then
    echo "Unexpected GitHub CLI key fingerprint" >&2
    exit 1
  fi
  if ! grep -qx '7F38BBB59D064DBCB3D84D725612B36462313325' <<<"$fprs"; then
    echo "Unexpected GitHub CLI key fingerprint" >&2
    exit 1
  fi

  cat >/etc/apt/sources.list.d/github-cli.list <<EOF_GH
deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main
EOF_GH
}

install_tailscale_repo() {
  log "Configuring Tailscale stable repository"
  . /etc/os-release
  local os_name="$ID"
  local os_codename="${UBUNTU_CODENAME:-$VERSION_CODENAME}"
  local base="https://pkgs.tailscale.com/stable/${os_name}/${os_codename}"

  install -d -m 0755 /usr/share/keyrings /etc/apt/sources.list.d
  curl -fsSL "${base}.noarmor.gpg" -o /usr/share/keyrings/tailscale-archive-keyring.gpg
  chmod 0644 /usr/share/keyrings/tailscale-archive-keyring.gpg
  curl -fsSL "${base}.tailscale-keyring.list" -o /etc/apt/sources.list.d/tailscale.list
}

install_tools() {
  log "Installing Incus, Tailscale, GitHub CLI, and Codex"
  apt-get update
  apt-get install -y gh incus tailscale
  npm install -g @openai/codex@latest
}

configure_user() {
  log "Configuring user ${WORKVM_USER}"
  if ! id "$WORKVM_USER" >/dev/null 2>&1; then
    useradd -m -s /usr/bin/zsh "$WORKVM_USER"
  else
    usermod -s /usr/bin/zsh "$WORKVM_USER"
  fi

  usermod -aG sudo "$WORKVM_USER"
  if getent group incus-admin >/dev/null; then
    usermod -aG incus-admin "$WORKVM_USER"
  fi
  if getent group incus >/dev/null; then
    usermod -aG incus "$WORKVM_USER"
  fi

  cat >/etc/sudoers.d/90-workvm-"$WORKVM_USER" <<EOF_SUDO
${WORKVM_USER} ALL=(ALL) NOPASSWD:ALL
EOF_SUDO
  chmod 0440 /etc/sudoers.d/90-workvm-"$WORKVM_USER"
  visudo -cf /etc/sudoers.d/90-workvm-"$WORKVM_USER" >/dev/null

  local home
  home="$(getent passwd "$WORKVM_USER" | cut -d: -f6)"
  install -d -m 0700 -o "$WORKVM_USER" -g "$WORKVM_USER" "$home/.ssh"
  touch "$home/.ssh/authorized_keys"
  chmod 0600 "$home/.ssh/authorized_keys"
  chown "$WORKVM_USER:$WORKVM_USER" "$home/.ssh/authorized_keys"
  if ! grep -Fxq "$SSH_PUBLIC_KEY" "$home/.ssh/authorized_keys"; then
    printf '%s\n' "$SSH_PUBLIC_KEY" >>"$home/.ssh/authorized_keys"
  fi

  local alias_line='alias ycodex="codex --dangerously-bypass-approvals-and-sandbox"'
  for rc in "$home/.bashrc" "$home/.zshrc"; do
    touch "$rc"
    chown "$WORKVM_USER:$WORKVM_USER" "$rc"
    if ! grep -Fxq "$alias_line" "$rc"; then
      printf '\n%s\n' "$alias_line" >>"$rc"
    fi
  done

  local bash_prompt_marker='# workvm bash prompt: hostname cwd git-branch'
  if ! grep -Fxq "$bash_prompt_marker" "$home/.bashrc"; then
    cat >>"$home/.bashrc" <<'EOF_BASH_PROMPT'

# workvm bash prompt: hostname cwd git-branch
__workvm_git_branch() {
  local branch
  branch="$(git symbolic-ref --quiet --short HEAD 2>/dev/null || git rev-parse --short HEAD 2>/dev/null)" || return 0
  printf ' (%s)' "$branch"
}
PS1='\h:\w$(__workvm_git_branch)\$ '
EOF_BASH_PROMPT
    chown "$WORKVM_USER:$WORKVM_USER" "$home/.bashrc"
  fi
}

configure_sshd() {
  log "Configuring SSH server"
  install -d -m 0755 /etc/ssh/sshd_config.d
  cat >/etc/ssh/sshd_config.d/90-workvm.conf <<'EOF_SSHD'
PubkeyAuthentication yes
PasswordAuthentication no
PermitRootLogin no
EOF_SSHD
  systemctl enable --now ssh
  systemctl restart ssh
}

install_agent_skills() {
  log "Installing agent skills"
  local home
  home="$(getent passwd "$WORKVM_USER" | cut -d: -f6)"
  for target in "$home/.codex/skills" "$home/.claude/skills"; do
    install -d -m 0755 -o "$WORKVM_USER" -g "$WORKVM_USER" "$target"
    tar -xzf /root/workvm-setup/skills.tgz -C "$target"
    chown -R "$WORKVM_USER:$WORKVM_USER" "$target"
  done
}

configure_tailscale() {
  log "Configuring Tailscale"
  systemctl enable --now tailscaled
  if [[ -n "${TAILSCALE_AUTHKEY:-}" ]]; then
    local state
    state="$(tailscale status --json 2>/dev/null | jq -r '.BackendState // ""' || true)"
    if [[ "$state" != "Running" ]]; then
      tailscale up --auth-key="$TAILSCALE_AUTHKEY" --hostname="$TAILSCALE_HOSTNAME"
    fi
  else
    echo "TAILSCALE_AUTHKEY not set; installed Tailscale but did not join a tailnet" >&2
  fi
}

verify_setup() {
  log "Verifying setup"
  local home
  home="$(getent passwd "$WORKVM_USER" | cut -d: -f6)"
  test -s "$home/.ssh/authorized_keys"
  test -f "$home/.codex/skills/diagnose/SKILL.md"
  test -f "$home/.claude/skills/diagnose/SKILL.md"
  sudo -n -u "$WORKVM_USER" sudo -n true
  sudo -n -iu "$WORKVM_USER" zsh -ic 'alias ycodex >/dev/null && command -v codex >/dev/null && codex --version'
  gh --version | head -1
  tailscale version | head -1
  incus version
  if [[ -n "${TAILSCALE_AUTHKEY:-}" ]]; then
    tailscale status --json | jq -r '.BackendState'
  fi
}

require_supported_os
install_base_packages
install_zabbly_incus_repo
install_github_cli_repo
install_tailscale_repo
install_tools
configure_user
configure_sshd
install_agent_skills
configure_tailscale
verify_setup

log "workvm setup complete"
EOF_SCRIPT
chmod 700 "$TMP_SCRIPT"

incus exec "$INSTANCE" -- mkdir -p "$REMOTE_DIR"
incus file push "$TMP_ENV" "${INSTANCE}${REMOTE_ENV}"
incus file push "$TMP_SCRIPT" "${INSTANCE}${REMOTE_SCRIPT}"
incus file push "$TMP_SKILLS_ARCHIVE" "${INSTANCE}${REMOTE_SKILLS_ARCHIVE}"
incus exec "$INSTANCE" -- chmod 600 "$REMOTE_ENV"
incus exec "$INSTANCE" -- chmod 700 "$REMOTE_SCRIPT"
incus exec "$INSTANCE" -- bash -lc "set -a; . '$REMOTE_ENV'; set +a; bash '$REMOTE_SCRIPT'"
