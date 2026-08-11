# Shape: Add a "dev" base image (Ubuntu 26.04)

## What this wish is

A **third OCI image template**, `images/dev/Dockerfile`, based on `ubuntu:26.04`, built and
published through the same admin tooling as the existing `base` (Debian 13) and `ai` (Debian
13 + AI CLIs) templates — a new `sc-adm image build dev` / `mise run image:dev:*` path — that
provisions a ready-to-use interactive developer shell: zsh + starship + mise-managed toolchain
+ Claude Code/Codex CLI + a context-usage status line for both, git/gh identity wired for first
use. It plugs into `config.Images` (`Images.Dev` alongside `Images.Base`/`Images.AI`) and is
selectable the same way `ai` is today: `sc create <machine> --image dev` (or an admin-configured
alias/env override), going through the **same v2 tenant/cloud-init machinery** as every other
image.

## What this wish is not

- **Not a fix for SSH agent forwarding.** That work is already done — see "Key finding" below.
  This wish's job re: agent forwarding is *verification that it still works on Ubuntu 26.04*,
  not new code.
- **Not a fourth Sandcastle service tier.** Per the already-decided integration scope, `dev`
  carries only enough tenant plumbing to be a valid Sandcastle Machine (systemd + sshd +
  cloud-init) — it does **not** get tailscale, Samba, Caddy ingress, CoreDNS, or Docker-in-Docker.
  A `dev`-image machine is reachable via `sc connect`/`incus exec` (which route over Incus's own
  remote/cert plumbing, not tailscale — confirmed in `internal/cli/connect_v2.go`) and gets a
  login user + SSH key + hostname from cloud-init exactly like `base`/`ai`, but has no HTTPS
  ingress, no SMB home-share, and isn't part of the tailnet.
- **Not a replacement for `base`/`ai`.** Purely additive; `base`/`ai` are untouched.

## Key finding: section 1 (SSH agent forwarding) is already shipped

`internal/tenant/create_plan_v2.go` already implements, byte-for-byte, the exact mechanism the
wish describes in its own words: a stable symlink (`~/.ssh/ssh_auth_sock`) re-pointed at the
live `$SSH_AUTH_SOCK` on every SSH session (`/etc/ssh/sshrc`, "republish"), consumed by shell rc
files via a `-h` (symlink-exists) guard rather than `-S` (socket-live) so a pane opened while the
link is stale still heals once the next session re-points it. This is ADR-0022 ("`/.sc` shared
scripts volume"), merged to `main` **2026-07-21** (`4fb4181`, well before this wish), with:

- `sshAgentRepublishScript` / `sshAgentConsumeSnippet` (`create_plan_v2.go:168-184`) — single
  source of truth, baked into `V2DefaultProfileUserData`'s cloud-init `write_files` for
  **every** v2 tenant machine, regardless of which OCI image it boots (the shim injection is
  image-agnostic — it's rendered once in Go and handed to cloud-init).
- `sc fix <machine>` / `sc fix <machine> --check` (`internal/cli/fix.go`) — backfills/verifies
  the shim on machines built before it shipped.
- A documented PASS criterion already in `docs/e2e-sc2.md:949-969`, describing **exactly** the
  `herdr pane split` → `ssh-add -l` → `herdr pane close` verification protocol the wish asks for.
- History in `implementation-notes.md` (2026-07-17 entries) recording the original incident,
  root cause, and the two guards that make it correct.

**Consequence for this wish:** because the shim is injected centrally in Go (not per-image), the
`dev` image gets working agent forwarding for free the moment it's wired into the standard v2
image-selection path — *provided* it installs `zsh` (section 2 already asks for this) so
`/etc/zsh/zshrc` exists for the cloud-init `append: true` step to land in. The only real work
here is (a) confirming this end-to-end on a fresh Ubuntu 26.04 `dev` machine, and (b) a short
addition to `docs/e2e-sc2.md` noting the existing PASS criterion now also covers the `dev`
template. No new symlink/indirection code, no new ADR for the mechanism itself.

## Decisions taken (by this shaping pass) and why

These were judgment calls with a clearly-better answer given existing repo convention, not
genuine forks — recorded here rather than escalated, per "a question is not worth asking when
you could have found the answer by reading the repository."

- **`fd-find` gets an `fd` symlink.** The Debian/Ubuntu `fd-find` package installs the binary as
  `fdfind` (no `fd` on `$PATH`) — a near-universal developer surprise. The image adds
  `/usr/local/bin/fd -> /usr/bin/fdfind` at build time (same pattern the `base` Dockerfile
  already uses for `zellij`/`coredns`: fetch/symlink into `/usr/local/bin`).
- **AI CLI tooling (Claude Code, Codex, community skills) is a shared, parameterized script,
  not copy-pasted Dockerfile RUN blocks.** `images/ai/Dockerfile` already does this install
  (`npm install -g @anthropic-ai/claude-code@… @openai/codex@…`, then
  `HOME=/etc/skel npx skills@… add mattpocock/skills`) for Debian; `dev` can't `FROM` it (different
  OS lineage), so the version-pin/npm-install/`/etc/skel` steps get pulled into a small shared
  script both Dockerfiles `COPY`/`RUN`, avoiding the two templates drifting on CLI versions.
  `dev` intentionally does **not** install Gemini CLI — the wish's section 6 only names Claude
  Code and Codex, and nothing elsewhere calls for parity with `ai`'s exact tool set.
- **`mise install` runs at image build time, not first login.** The wish's own framing
  ("provisions a full interactive dev environment") and the `ai` image's existing precedent
  (baking `npm install -g` at build time rather than deferring to first boot) both point the
  same way: a `dev` machine should come up with `go`/`node`/`starship`/`herdr` already resolved,
  not spend its first login fetching toolchains over the network. `mise install` runs under
  `HOME=/etc/skel` (matching the skills-install trick) so every tenant user created later
  inherits a populated `~/.local/share/mise`.
- **Dotfiles (starship.toml, mise config.toml, zsh history/rc additions, statusline scripts)
  bake into `/etc/skel`.** Same mechanism `ai/Dockerfile` already uses for
  `~/.claude/skills` — new tenant users (created by cloud-init's `useradd` at first boot) inherit
  them automatically. This is a "new base image" wish, not a `/.sc` shared-payload update, so
  build-time `/etc/skel` is the right layer (the `/.sc` payload volume, ADR-0022, is for content
  that must reach *already-running* machines centrally — not the right tool for one-time image
  provisioning).
- **Git identity and `gh auth login` are runtime, not build-time**, per the already-answered
  q2/q3: a first-shell-login zsh hook derives `user.name`/`user.email` from `gh api user` (only
  if `gh auth status` succeeds and no identity is set yet — idempotent, non-destructive of a
  tenant who already configured git by hand). `gh auth login` itself stays interactive/runtime;
  nothing image-build-time can hold a credential for a shared image.
- **Git aliases**: the wish explicitly delegates this ("no specific list supplied... pick a
  sensible common baseline") — a small, uncontroversial set (`co`, `br`, `st`, `last`, `unstage`,
  `amend`) goes in the same global gitconfig approach `images/base/gitconfig` already uses.
- **The `dev` template gets full build-pipeline parity with `base`/`ai`**: both local
  (`mise run image:dev:build-upload`) and remote/GHCR (`image:dev:build-remote`) tasks, added to
  the `image:all:*` aggregates — matching "Full third template" from the prior decision round,
  and mirroring `mise.toml`'s existing `image:base:*`/`image:ai:*` task shapes exactly (same
  `sandcastle-admin image build <template>` / `image build-remote <template>` CLI surface,
  extended from its current `base`/`ai` switch in `internal/images/plan.go:171-192` and
  `internal/cli/admin.go`'s `remoteBuildTemplates`).
- **A short ADR** (next available number: `docs/adr/0024-*`) records the three-template pattern
  and the "minimum tenant plumbing" scope decision — this repo's convention (`CLAUDE.md`) is to
  record resolved architectural decisions as ADRs; the shape/spec docs alone don't satisfy that
  convention.

## Decisions already taken (prior round, human-confirmed)

- **q1 — integration scope**: full third template, minimum tenant plumbing (systemd, sshd,
  cloud-init only) — no tailscale/Samba/Caddy/CoreDNS/Docker.
- **q2 — git identity**: derived from `gh api user` at first shell login, not baked at build
  time or sourced from Sandcastle's tenant-owner identity plumbing (which `sandcastle-bootstrap`
  does for `base`/`ai` machines — `dev` does not use `sandcastle-bootstrap` at all, consistent
  with q1's minimal-plumbing scope).
- **q3 — SSH agent fix reuse**: fix lives at the cloud-init layer, shared across all
  custom-built images. Turned out to already be true and already shipped (see "Key finding").

## What a good outcome looks like

- `images/dev/Dockerfile`: `FROM ubuntu:26.04`, installs `git gh ripgrep fd-find make zsh`
  (+ `fd` symlink), `systemd`/`systemd-sysv`/`openssh-server` (minimum tenant plumbing only —
  no tailscale/Samba/Caddy/CoreDNS/Docker packages from `base`), `curl`-based installs of
  `starship` and `mise`, the shared AI-CLI-install script (Claude Code + Codex + community
  skills into `/etc/skel`), the zsh history block, the verbatim `starship.toml`, the verbatim
  `mise/config.toml`, both statusline scripts, and a first-login git-identity hook — all under
  `/etc/skel` where they belong so new tenant users inherit them.
- `chsh`/`/etc/default/useradd` sets zsh as the image's system default shell (belt-and-braces —
  cloud-init's `V2DefaultProfileUserData` already independently sets `shell: /bin/zsh` for the
  actual tenant login user it creates, regardless of image, so this mainly covers root/system
  shells and keeps the image internally consistent).
- `internal/config/admin.go`: `Images` struct gains a `Dev` field + a
  `DefaultDevImageAlias` (mirroring `images:debian/13`'s pattern — worth a quick
  `incus image list images: ubuntu` check before picking the exact alias string, since
  `images:ubuntu/26.04` needs to actually exist on the public remote by the time this ships).
- `internal/images/plan.go`, `internal/cli/admin.go`: `base`/`ai` switches gain a `dev` case
  (build args, template alias, `remoteBuildTemplates` validation message).
- `mise.toml`: `image:dev:build`, `image:dev:upload`, `image:dev:build-upload`,
  `image:dev:build-remote`, folded into `image:all:build-upload`/`image:all:build-remote`.
- `docs/usage.html`, `docs/admin-developer-quickstart.html`: new template documented.
- `docs/e2e-sc2.md`: new phase/PASS criteria for `sc create --image dev` provisioning
  (shell, starship, mise tools present, Claude Code/Codex CLI present, statusline scripts
  render against synthetic JSON, git identity self-populates on first login), plus a one-line
  addition noting the existing agent-forwarding PASS criterion (`docs/e2e-sc2.md:949-969`) now
  also exercises the `dev` template.
- `docs/adr/0024-*`: records the three-template pattern + minimum-plumbing scope.
- `implementation-notes.md`: entries for anything invented along the way (exact `/etc/skel`
  layering choices, the shared AI-CLI-install script, the Codex status-line port once the
  research below lands).
- Manual verification: `sc create <m> --image dev`, then interactively confirm zsh/starship/mise
  tool versions, `claude`/`codex` CLIs present, both statusline scripts render against the
  documented synthetic JSON payloads, and the `herdr pane split` → `ssh-add -l` →
  `herdr pane close` protocol from `docs/e2e-sc2.md:949-969` still passes.

## Open items handed off as research (non-blocking)

Two facts needed before/while implementing section 7 (statusline) and section 5 (mise tools)
are outside this repository and shouldn't be guessed at:

1. **Codex CLI's status-line/prompt-hook mechanism** — its config key, the JSON (or other)
   payload it feeds to a custom command on stdin, how it consumes stdout, refresh cadence, and
   which of the six segments (model, context %, cwd, git branch, 5h/7d rate limits) it actually
   exposes data for. The wish explicitly asks for this to be researched before porting the
   Claude Code script; nothing in this repo documents Codex's contract.
2. **Whether `herdr` is installable via `mise install` out of the box** (a registered mise/asdf
   plugin or backend), or needs a custom plugin/backend URL in `mise.toml`'s `[tools]` table.
   The repo uses `herdr` extensively in tests/docs/e2e criteria but never shows how it's
   installed (`images/base/Dockerfile` hand-installs `zellij` from GitHub releases because it
   isn't packaged for Debian — `herdr` may need similar treatment rather than a bare
   `herdr = "latest"` mise entry).
