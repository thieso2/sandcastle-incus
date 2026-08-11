# Spec: a `dev` base image (Ubuntu 26.04)

Builds on `docs/shape/add-a-dev-base-image-ubuntu-26-04-fixed-b2qtk8g.md` — read that first. This
spec inherits its "already decided" items (q1 integration scope, q2 git-identity timing, q3
agent-forwarding reuse) without repeating their reasoning, restates a shape-doc claim only where
verification against current code sharpens or corrects it, and turns "what a good outcome looks
like" into checkable behavior.

## Problem statement

Sandcastle has two Machine Templates today (`base`, `ai`; `CONTEXT.md:256-262`), both Debian 13.
Neither gives a tenant a batteries-included *interactive* shell: no zsh, no prompt, no per-tool
version manager, no AI CLI tooling beyond what `ai` already ships (Claude Code, Codex, Gemini, all
tuned for autonomous/headless use, not for someone sitting at a terminal), and no context-usage
status line. A developer who wants to work interactively inside a Sandcastle Machine assembles all
of this by hand, every time, on top of `base` or `ai`.

## Solution, in one paragraph

A third OCI image template, `images/dev/Dockerfile`, `FROM ubuntu:26.04` — **not** `FROM
sandcastle/base:latest`, since `base` is Debian and `dev` is a different OS lineage — carrying the
minimum tenant plumbing to be a valid Sandcastle Machine (systemd, sshd, cloud-init) plus a fully
provisioned interactive shell: zsh as the default shell, a starship prompt, mise-managed toolchain,
Claude Code + Codex CLIs with community skills, and a context-usage status line for both. It plugs
into the same `sc-adm image build/upload/sync` pipeline, `config.Images`, and `sc create --image`
selection path that `base`/`ai` already use, and is purely additive — `base`/`ai` are untouched.

## Scope

**In scope:**
- `images/dev/Dockerfile` and any shared install scripts it factors out with `images/ai/Dockerfile`.
- `internal/config/admin.go`: `Images.Dev` + `DefaultDevImageAlias`, wired through
  `AdminDefaults`/`adminOverridesFromEnv`/`MergeAdmin`/`Validate` exactly as `Images.Base`/`AI` are.
- `internal/images/plan.go` (`PlanBuild`, `aliasForTemplate`, `templateAlias`) and
  `internal/images/remote.go` (`PlanRemoteBuild`) growing a `"dev"` case each.
- `internal/cli/admin.go`: every `base|ai` template switch/argument validator (`remoteBuildTemplates`
  and the `Use:` strings on the build/build-remote/sync/upload/import commands) growing a `dev` case.
- `internal/tenant/create_plan_v2.go`: `uniqueImageAliases` including the Dev Image alias so a
  freshly created tenant project can resolve it without a fully-qualified remote reference (see
  §B9), **and** a new ingress-skip mechanism for Dev Image machines (see §B8 — this is the one
  place the shape doc's "good outcome" list undercounts the required change).
- `mise.toml`: `image:dev:build`, `:upload`, `:build-upload`, `:build-remote`, folded into
  `image:all:build-upload`/`image:all:build-remote`.
- Documentation: `docs/usage.html`, `docs/admin-developer-quickstart.html`, `docs/e2e-sc2.md`,
  `CONTEXT.md` (see §Ground truth), `implementation-notes.md`, `docs/adr/0024-*`.

**Out of scope (per the shape doc's q1, restated as hard boundaries):**
- Tailscale, Samba, CoreDNS sidecar, Docker-in-Docker — `dev` never installs these packages, so
  this boundary is satisfied by omission from the Dockerfile, no code changes elsewhere needed.
- Any change to `base`/`ai` behavior, defaults, or build pipeline.
- `sandcastle-bootstrap` — `dev` machines are not tenant-owner-identity-provisioned the way
  `base`/`ai` are (per q2); git identity is a first-login shell hook instead (§B7).
- A new SSH-agent-forwarding mechanism — §B1 is verification only, per q3 and the "Key finding" in
  the shape doc.

## Ground truth this spec relies on (verified against current code, not the shape doc's paraphrase)

- **`--image` takes a literal Incus image reference, not a template keyword.** `sc create <m>
  --image <ref>` (`internal/cli/create.go:42`, `internal/cli/create_v2.go:307-333`) passes
  `options.Image` straight through to `incusx.CreateMachineV2Request.Image`; there is no "base" /
  "ai" / "dev" keyword resolution anywhere in that path, and the compiled-in default is the literal
  constant `v2DefaultMachineImage = "images:debian/13/cloud"` (`internal/cli/create_v2.go:22`),
  independent of `admin.Images.Base`. So "`sc create <machine> --image dev`" (shape doc, "What this
  wish is") only works verbatim if an operator happens to publish the Dev Image under the literal
  Incus alias `dev` — which nothing in this wish makes happen automatically. What ships instead is
  parity with how `ai` is selected *today*: an operator builds and uploads the Dev Image to
  whatever alias they choose (by convention `sandcastle/dev:latest`, mirroring
  `docs/admin-developer-quickstart.html`'s `SANDCASTLE_AI_IMAGE=sandcastle/ai:latest` convention),
  and tenants pass that alias to `--image`. This spec's acceptance criteria use `<dev-alias>` for
  exactly this reason — don't read it as the literal string `dev`.
- **Tenant-local image alias sync.** `V2CreatePlan` seeds each new tenant project's local Incus
  image aliases via `uniqueImageAliases(admin.Images.Base, admin.Images.AI)`
  (`internal/tenant/create_plan_v2.go:559`) so `--image <alias>` resolves inside the tenant's
  restricted project without a fully-qualified `<remote>:<fingerprint>` reference. Left unchanged,
  a freshly created tenant project would not carry the Dev Image alias, and `sc create --image
  <dev-alias>` would fail to resolve for tenants created after this ships (existing tenants
  created before this ships need the same backfill mechanism `sc fix`/`payload-sync` already uses
  for other per-project additions — exact mechanism is an implementation call, but the requirement
  is: a tenant project created after this ships must resolve the Dev Image alias without a manual
  step).
- **Caddy/TLS ingress is applied by the project's shared default profile, not per-image — this is
  the one place q1's "no Caddy ingress" scope decision needs *new* code, not just a new
  Dockerfile.** `V2DefaultProfileUserData` (`internal/tenant/create_plan_v2.go:28-92`) is rendered
  **once per project** into the Incus default profile every non-bare machine in that project
  inherits; its Caddy branch (lines 64-87) fires whenever `jinja && signerURL != ""` — true for
  every ordinary (non-`--bare`) v2 machine, unconditionally on which OCI image it boots. Today
  there is no per-image or per-machine toggle: any non-bare machine in a project — `base`, `ai`, or
  (unmodified) `dev` — gets Caddy apt-installed, a tenant-CA leaf fetched, and HTTPS ingress started
  at first boot. The shape doc's "good outcome" section does not list a change to this file for the
  ingress boundary (only for the alias-sync line above), which undercounts the work: satisfying "no
  HTTPS ingress" for Dev Image machines requires a new *instance-level* user-data override for
  Dev Image machines, structurally the same lever `V2BareUserData` already uses to override the
  project profile's user-data per-instance (`internal/tenant/create_plan_v2.go:100-111`,
  "applied as INSTANCE config, overriding the project default profile's user-data") — except unlike
  `--bare`, a Dev Image machine keeps its login user, SSH key, and sshd; it only drops the Caddy
  branch. See §B8.
- **`CONTEXT.md:261`** currently reads "the Base Image and the AI Image are its two variants" — this
  is canonical domain vocabulary (`CLAUDE.md`: "Domain terms from CONTEXT.md are canonical") and
  becomes wrong the moment a third variant ships; it needs a one-line update alongside the code,
  even though `CONTEXT.md` is not itself in the `docs/` obligations list in `CLAUDE.md`.
- **`images:ubuntu/26.04` (or `/cloud` variant) as the stock fallback alias.** `DefaultBaseImageAlias`
  / `DefaultAIImageAlias` both default to the public upstream `images:debian/13` so Sandcastle
  "requires NO prebuilt images" (`internal/config/admin.go:21-26`) — a tenant can use `base`/`ai`
  with zero custom build. `DefaultDevImageAlias` should follow the identical pattern (a stock Incus
  `images:` alias, used until an operator builds+uploads the real Dev Image), **but** whether Incus's
  public `images:` remote actually publishes an Ubuntu 26.04 entry — and under what exact alias
  string — is an external fact this repo cannot answer by reading its own source; the shape doc
  flagged this itself ("worth a quick `incus image list images: ubuntu` check ... since
  `images:ubuntu/26.04` needs to actually exist ... by the time this ships") but did not send it out
  as a research detour. This spec does the same — flags it, does not resolve it — because resolving
  it requires touching a live Incus `images:` remote, not repo research.
- **`packages: [openssh-server, zsh]` is also declared in cloud-init's own `packages:` block**
  (`internal/tenant/create_plan_v2.go:47-50`), which apt-installs zsh at first boot regardless of
  the image. Pre-baking zsh into the Dev Image (§B2) is still required — it makes `/etc/skel`
  dotfile inheritance and the image-level `chsh`/`useradd` default land before cloud-init ever runs,
  and it means first boot doesn't depend on network apt access — but don't read cloud-init's
  existing `packages:` line as evidence that a bare `ubuntu:26.04` base without zsh would already
  "work"; the ordering and `/etc/skel` requirements make pre-baking necessary, not merely faster.

## Behavior specification

### B1 — SSH agent forwarding: verification only, no new mechanism

Per q3 / the shape doc's "Key finding": the stable-symlink indirection (`~/.ssh/ssh_auth_sock`,
`/etc/ssh/sshrc` republish, `-h`-guarded consume snippet in `/etc/zsh/zshrc`/`/etc/bash.bashrc`) is
already shipped centrally (ADR-0022, `internal/tenant/create_plan_v2.go:148-258`), image-agnostic.
This wish adds **no new code** for it. Its only obligations here:
- The Dev Image ships zsh pre-installed (§B2) so `/etc/zsh/zshrc` exists for the `append: true`
  write_files step to land in on first boot.
- End-to-end verification on a real Ubuntu 26.04 Dev Image machine: `ssh -A` in, `herdr pane split`,
  `ssh-add -l` inside the new pane, `herdr pane close` back to the original, close the original SSH
  connection, reconnect fresh, `herdr attach`, confirm `ssh-add -l` (and an agent-dependent op, e.g.
  `git fetch` over SSH) still works in the reattached pane.
- `docs/e2e-sc2.md:949-969`'s existing PASS criterion gets a one-line addition noting it now also
  covers the Dev Image — not a new criterion.

**How you'd know it's wrong:** the verification protocol above fails on a Dev Image machine even
though it already passes on `base`/`ai` — that would mean something about Ubuntu 26.04 (not the
mechanism itself, which is OS-agnostic Go/cloud-init code) broke it, e.g. zsh missing so the shim
never lands, or `/etc/ssh/sshrc` not invoked by Ubuntu's OpenSSH build.

### B2 — Base packages and default shell

The Dockerfile installs, via `apt-get install --no-install-recommends`: `git gh ripgrep fd-find
make zsh`, plus the minimum-tenant-plumbing set `systemd systemd-sysv openssh-server` (mirroring
`base`'s equivalent packages, `images/base/Dockerfile:43,48-49`, minus everything in the out-of-scope
list). `fd-find` installs its binary as `fdfind`, not `fd`
(Debian/Ubuntu packaging quirk) — the image adds `/usr/local/bin/fd -> /usr/bin/fdfind` at build
time, the same pattern `base` already uses for `zellij`/`coredns` (`images/base/Dockerfile:90-102`,
hand-fetch-and-symlink into `/usr/local/bin`).

zsh becomes the image's system default shell (`chsh`/`/etc/default/useradd`) — belt-and-braces,
since `V2DefaultProfileUserData` already independently sets `shell: /bin/zsh` for the actual tenant
login user regardless of image (`internal/tenant/create_plan_v2.go:43`). This mainly keeps
root/system shells and the image's own internal consistency, not the tenant user's login shell,
which was already zsh before this wish for every v2 machine.

**How you'd know it's wrong:** `sc create m --image <dev-alias>` then `sc c m` — the login shell is
zsh (`echo $SHELL` / `getent passwd $(whoami)`), `fd`, `rg`, `gh`, `make`, `git` all resolve on
`$PATH`.

### B3 — Zsh history config

The exact five-line block from the wish (`HISTFILE=~/.zsh_history`, `HISTSIZE=10000`,
`SAVEHIST=10000`, `INC_APPEND_HISTORY`, `SHARE_HISTORY`, `HIST_IGNORE_ALL_DUPS`) lands in a
per-user zsh rc file baked under `/etc/skel` (e.g. `/etc/skel/.zshrc`) — **not** `/etc/zsh/zshrc`,
which is reserved for the `/.sc` shim (§B1) and must not be perturbed by this wish (the shim's
`append: true` cloud-init step and the SSH-agent consume snippet both key off that exact file's
content). `/etc/skel` is the mechanism `images/ai/Dockerfile` already uses for `~/.claude/skills`
(lines 39-46) — new tenant users, created by cloud-init's `useradd` at first boot, inherit
`/etc/skel`'s contents automatically; this is a new-image wish, not a `/.sc` shared-payload update
(ADR-0022's payload is for content that must reach *already-running* machines centrally, not
one-time image provisioning — same reasoning the shape doc already gives for §B4/§B5/§B6/§B7).

**How you'd know it's wrong:** a fresh login user's `~/.zsh_history` grows across sessions
(`INC_APPEND_HISTORY`), is shared live between concurrent panes (`SHARE_HISTORY`), and
`HIST_IGNORE_ALL_DUPS` collapses repeated commands — checkable with `history` after running the
same command twice in two panes.

### B4 — Prompt (starship)

`starship` is installed via its official `curl`-based installer at build time. The verbatim
`starship.toml` from the wish lands at `/etc/skel/.config/starship.toml`. Starship's `init`
invocation is wired into the shell rc (§B5, since it's bundled with the same rc-activation step as
mise). No deviation from the verbatim config content — this spec does not re-derive the prompt
segments; the wish's TOML is the source of truth.

**How you'd know it's wrong:** a fresh login shows the documented one-line prompt: `user@fqdn:`
left-aligned with `>` colored green on success / red on last-command failure, directory + git
branch right-aligned, git branch/status colored per the TOML's dirty/clean rules.

### B5 — Tool version management (mise)

`mise` is installed via `curl https://mise.run | sh` at build time (not the apt-repo method `base`
uses for its own `mise` — the wish specifies the installer script, which installs to
`$HOME/.local/bin`; since the Dockerfile step needs a `$HOME`, this runs under `HOME=/etc/skel`,
mirroring the `ai` image's `HOME=/etc/skel npx ...` skills-install trick, so mise lands under
`/etc/skel/.local/bin` and `/etc/skel/.local/share/mise` for every future tenant user to inherit).
The verbatim `~/.config/mise/config.toml` (`go`, `herdr`, `node`, `starship`, all `"latest"`) is
baked to `/etc/skel/.config/mise/config.toml`, and `mise install` runs at build time under the same
`HOME`, so the image ships with pinned-at-build-time tool versions already resolved (per the shape
doc's "provisions a full interactive dev environment" framing — no first-boot network fetch).
`herdr` needs no special plugin handling: it is a first-class entry in mise's own tool registry
(`aqua:herdrdev/herdr` / `github:herdrdev/herdr` backends), resolvable by a bare `mise install`
exactly like `go`/`node`/`starship` — no `zellij`-style hand-download treatment required.

The shell rc activates mise and starship using `$HOME`, not a hardcoded path — the wish's own
example (`eval "$($HOME/.local/bin/mise activate bash)"`) already does this; the zsh rc (default
shell, §B2) gets the zsh-flavored equivalents (`mise activate zsh`, `starship init zsh`).

**How you'd know it's wrong:** a fresh login user has `go`, `node`, `starship`, `herdr` resolving on
`$PATH` at their `mise`-pinned "latest-as-of-build" versions with no network fetch (`mise ls`
shows them installed, not just configured); `mise --version` / `starship --version` work without
first invoking `mise install` interactively.

### B6 — AI CLI tooling

Claude Code (`curl -fsSL https://claude.ai/install.sh | bash`) and the Codex CLI are installed at
build time. Per the shape doc's decision, this reuses a small shared script factored out of
`images/ai/Dockerfile`'s existing `npm install -g @anthropic-ai/claude-code@… @openai/codex@…`
+ `HOME=/etc/skel npx skills@… add mattpocock/skills` steps (`images/ai/Dockerfile:26-46`), so the
two templates don't drift on CLI versions — **`dev` does not install `@google/gemini-cli`**; the
wish's §6 names only Claude Code and Codex, and nothing calls for `ai`-parity on tool set. Community
skills install the same way `ai` does: `HOME=/etc/skel npx skills@latest add mattpocock/skills`, so
new tenant users inherit `~/.claude/skills`.

Note the mechanism mismatch worth naming explicitly: `ai`'s CLI install already uses plain
`npm install -g`, not the wish's literal `curl -fsSL https://claude.ai/install.sh | bash` — this
spec follows the shape doc's "shared script" decision (dedupe against `ai`, avoid version drift)
over the wish's literal install-script text; if a future reviewer wants the two to diverge (`dev`
using the official installer verbatim, `ai` keeping npm), that's a one-line call to make when
writing the shared script, not a spec ambiguity — either installs the same binaries.

**How you'd know it's wrong:** `claude --version` and `codex --version` resolve for a fresh tenant
user with no network fetch; `~/.claude/skills` is populated from `mattpocock/skills`; `gemini` does
**not** resolve (confirms the intentional non-parity with `ai`).

### B7 — Status line: Claude Code

Verbatim per the wish: `~/.claude/statusline-command.sh` (the exact script given, byte-for-byte,
including the Linux-portable dual-form `date -r ... || date -d "@..."` reset-time clock) and
`~/.claude/settings.json`'s `statusLine` stanza both land under `/etc/skel` so every tenant user
inherits them. `jq` is a hard dependency — installed in the Dockerfile's package list if not
already pulled in transitively (it is not in the §B2 package list above, so it must be added
explicitly; `base` doesn't need it added since it already has it, `images/base/Dockerfile:37`).
A Nerd Font is a terminal-side requirement, not something the image can provide — document it as a
client-side prerequisite (`docs/usage.html`/`docs/admin-developer-quickstart.html`), not a package.

**How you'd know it's wrong:** the wish's own synthetic-payload verification command, run against
the baked script on a Dev Image machine, produces the documented one-line output with the model
name, a 10-segment context bar colored by the shared green/yellow/red ramp, `~`-relative cwd, git
branch (only inside a repo), and both rate-limit segments with the reset clock rendered in local
`HH:MM` (Linux `date -d` path, not the BSD `date -r` path, taken).

### B8 — Status line: Codex CLI (research-informed, and a hard capability boundary)

Per the pre-shape research memo (`01-shape-research_memo.md`, corroborated against
`codex-rs/core/config.schema.json` and `codex-rs/tui/src/bottom_pane/status_line_setup.rs` on
`openai/codex@main`): **Codex has no command-hook status line.** There is no config key that runs an
external script, no JSON-on-stdin/ANSI-on-stdout contract, nothing to "port" the Claude Code script
to. What exists instead is `[tui] status_line = [...]`, an ordered array of fixed built-in item
identifiers Codex renders with its own formatting and its own (theme-derived, not color-ramp-based)
coloring.

This spec's obligation is therefore **not** a script but a `~/.codex/config.toml` `[tui]` stanza
(baked to `/etc/skel/.codex/config.toml`) selecting the closest built-in items to the six Claude
Code segments:

| Claude Code segment | Codex item | Fidelity |
|---|---|---|
| Model name | `model-with-reasoning` | full |
| Context bar + % | `context-used` | percentage only — **no bar**, no green/yellow/red ramp; Codex's own theme coloring |
| Directory | `current-dir` | full (Codex's own truncation/formatting, not `~`-collapse-guaranteed) |
| Git branch | `git-branch` | full — documented to auto-omit outside a repo, matching Claude Code's behavior |
| 5h rate limit | `five-hour-limit` | **caveat:** documented as *remaining*, not *used* — confirm sense against a live Codex session before treating it as `100 - used_percentage` |
| 7d rate limit | `weekly-limit` | same remaining-vs-used caveat |

No shared code with the Claude Code script is possible or expected: `round_pct`/`color_for`, the
unit-separator `jq` parsing, the 10-segment bar, and the specific glyphs/ANSI codes have nowhere to
run. Gaps are **explicit, not faked**: no bar rendering, no shared color-ramp thresholds, no
reset-time display for either rate-limit window (no such field was found in Codex's item
descriptions). `implementation-notes.md` and `docs/usage.html` must say this plainly — a reviewer
comparing the two status lines side by side should not go looking for a bug where there's a
documented capability boundary instead.

**How you'd know it's wrong:** don't ship a shell script Codex will never invoke; don't claim visual
parity with Claude Code's bar/colors in docs; do verify, on a live Codex session, whether
`five-hour-limit`/`weekly-limit` read as remaining or used before writing any doc sentence that
states a specific percentage semantic.

### B9 — Git setup

**Global config + aliases (build time).** `/etc/gitconfig` (or an `/etc/skel/.gitconfig`, consistent
with wherever `[user]` identity gets set in §B7-adjacent first-login hook) ships a sensible common
alias baseline — the wish explicitly delegates the exact list; `co`, `br`, `st`, `last`, `unstage`,
`amend` (the shape doc's own pick) satisfies "a common set" without inventing anything contentious.
Mirrors the mechanism `images/base/gitconfig` → `/etc/gitconfig` already uses.

**Identity (first login, not build time).** A zsh first-login hook derives `user.name`/`user.email`
from `gh api user`, **only if**: `gh auth status` succeeds (a token already exists) **and** no
`user.name`/`user.email` is already configured (idempotent — never overwrites a tenant who
configured git by hand). This cannot be build-time, since a shared image cannot hold a per-tenant
identity or credential.

**`gh` authentication.** `gh auth login` is interactive/runtime, and persists through `gh`'s own
credential storage (OS keyring where available, an 0600 file fallback otherwise) — never a
hand-written plaintext token file, and never co-located with the SSH private key (`~/.ssh/`). This
wish adds no code for `gh auth login` itself; it is a documented first-run step (`sc c
<dev-machine>` → `gh auth login`), not something the image or a hook can do non-interactively
without holding a credential the image build must never contain.

**How you'd know it's wrong:** `git config --global --get-regexp 'alias\.'` lists the baseline
aliases on a fresh machine before any login. After `gh auth login` succeeds and a fresh shell
starts, `git config --global user.email` is populated from `gh api user` — running the same login
again does not overwrite a value the tenant then changed by hand. `gh auth status` reports a stored
credential; `ls -la ~/.config/gh/` (or the platform keyring) shows no plaintext token file
co-located with `~/.ssh/`.

## Acceptance scenarios

1. **Build.** `mise run image:dev:build-upload` (local) and `mise run image:dev:build-remote`
   (Image Builder → GHCR) both succeed and are folded into `image:all:*`; `sc-adm image build dev
   --dry-run` renders a plan analogous to `base`'s/`ai`'s.
2. **Fresh interactive machine.** `sc-adm tenant create <t>` (after this ships) then `sc create m
   --image <dev-alias>` then `sc c m` → zsh default shell, starship prompt renders per §B4, `mise
   ls` shows `go`/`node`/`herdr`/`starship` already installed, `claude --version`/`codex --version`
   resolve, `gemini` does not, `fd`/`rg`/`gh`/`make`/`git` resolve.
3. **Status lines.** The wish's synthetic-JSON verification command against
   `~/.claude/statusline-command.sh` produces the documented output (§B7); `codex` (interactively,
   or by inspecting `~/.codex/config.toml`) shows the six-item `status_line` stanza (§B8), with the
   documented gaps (no bar, no ramp, no reset clock) visibly absent rather than silently faked.
4. **Agent forwarding.** The `herdr pane split` → `ssh-add -l` → detach/close/reconnect →
   `herdr` reattach protocol (§B1) passes on a Dev Image machine.
5. **Git identity self-populates.** Fresh machine, no prior `gh auth login`: `git config
   user.email` is empty. After `gh auth login` and a new shell: populated from `gh api user`.
   Change it by hand, log in again: unchanged (idempotent, non-destructive).
6. **No ingress.** A non-bare Dev Image machine has no Caddy process, fetched no TLS leaf, and does
   not answer on 443 for its private hostname — while its private hostname still resolves in DNS
   (standard v2 behavior, §Ground truth) and `sc c`/`incus exec` still reach it.
7. **No tailnet, no SMB.** The machine is not visible in `tailscale status` from another tenant
   machine, and no SMB share is offered for it (`smbclient -L` against its IP fails to connect).
8. **`base`/`ai` unaffected.** Building/creating `base` or `ai` machines behaves identically to
   before this wish — same packages, same defaults, same `Validate()` behavior with no `SANDCASTLE_
   DEV_IMAGE` set.

## Test seams

- **Image-alias/template-name plumbing (highest-value, cheapest seam).** `internal/images/plan_test.go`
  already covers `PlanBuild`/`aliasForTemplate`/`templateAlias`'s `base`/`ai` switch arms as pure Go
  against `config.Admin` values, no Docker/Incus involved — the `dev` arm is a same-shape addition:
  assert `PlanBuild(admin, BuildRequest{Template: "dev", ...})` requires Codex+Claude versions but
  **not** Gemini (unlike `ai`), and that `aliasForTemplate`/`templateAlias` round-trip `admin.Images.Dev`.
  `internal/images/remote_test.go` needs the equivalent for `PlanRemoteBuild`. `internal/cli/admin_test.go`
  needs `remoteBuildTemplates("dev")`/`("all")` coverage.
- **Config defaulting/env override.** `internal/config/admin_test.go`-style table test: `Images.Dev`
  defaults to `DefaultDevImageAlias`, `SANDCASTLE_DEV_IMAGE` overrides it, `Validate()` behaves for
  `dev` exactly as it does for `base`/`ai` today, and — critically — `Validate()` on an `Admin` built
  with **no** dev-specific env set (the pre-this-wish shape) still passes, proving §Acceptance
  scenario 8's backward-compatibility claim at the unit level.
- **Tenant-local alias sync.** Wherever `internal/tenant/create_plan_v2_test.go` asserts today's
  `uniqueImageAliases(admin.Images.Base, admin.Images.AI)` call — extend to assert the Dev Image
  alias is included, deduplicated the same way.
- **The ingress-skip mechanism (the seam this spec adds that the shape doc didn't scope — highest
  scrutiny seam).** `internal/tenant/create_plan_v2_test.go` already has the pattern this needs:
  `TestV2BareUserDataHasNoWayIn`/`TestV2BareUserDataBakesBootShims` (lines 209, 273) assert
  properties of a cloud-init user-data string by parsing/grepping it, no Incus involved. A new
  `TestV2DevUserData*` (or equivalent, whatever the new function ends up named) should assert: a
  login user + SSH key + enabled sshd **are** present (unlike bare), the `/.sc` shims **are** baked
  (§B1 depends on it), and the Caddy/generalize `write_files`/`runcmd` entries are **absent** —
  three assertions, each independently falsifiable, on a pure string.
- **Dockerfile build.** No Go test seam — `mise run image:dev:build` (or `sc-adm image build dev
  --dry-run` for the command-construction part only) is the seam; a CI job that actually builds the
  image (matching whatever `base`/`ai` already have, if anything) is the closest thing to a unit
  test this layer gets.
- **Status line scripts.** Both are pure POSIX shell reading JSON from stdin and writing to stdout —
  the wish's own synthetic-payload invocation *is* the test seam, already fully specified
  (`echo '{...}' | ~/.claude/statusline-command.sh`); wrap it in a script-level test harness (e.g.
  `bats`, or a Go test that shells out) asserting exact byte output for a fixed payload, covering:
  all-fields-present, rate-limits-absent (API-key user), directory outside `$HOME`, directory
  containing a glob metacharacter (the `set -f` requirement named in the wish). The Codex side has
  no script to test — its seam is the static `~/.codex/config.toml` content, testable as a file-
  content assertion only.
- **End-to-end (`docs/e2e-sc2.md`).** New phase: `sc create --image <dev-alias>` provisioning
  (shell/starship/mise/tool versions, Claude Code/Codex CLI present, both statusline
  artifacts render/exist against the documented synthetic payloads, git identity self-populates on
  first login, no Caddy/tailscale/SMB surface), plus the one-line addition to the existing
  agent-forwarding PASS criterion (§B1) noting Dev Image coverage.

## Documentation obligations (per root `CLAUDE.md`, same commits as the code)

- `docs/usage.html` — new template documented (mirroring how `base`/`ai` are documented today),
  including the Nerd Font terminal prerequisite for the statusline glyphs and the explicit Codex
  capability-gap note from §B8.
- `docs/admin-developer-quickstart.html` — `dev` added alongside the existing base/AI image
  prepare/build/upload steps.
- `docs/e2e-sc2.md` — extended per §Test seams; this file must not trail the code.
- `CONTEXT.md:261` — "two variants" → "three variants" (Base, AI, Dev Images).
- `implementation-notes.md` — record, as they're made: the exact shared-script factoring for §B6,
  the exact new-function name/shape for the ingress-skip user-data (§B8/Ground truth), the exact
  `DefaultDevImageAlias` string chosen (pending the external `images:` remote check), and the
  Claude-vs-Codex install-mechanism note from §B6.
- A new ADR, `docs/adr/0024-*`, recording: the three-Machine-Template pattern, the
  minimum-tenant-plumbing scope decision and *why it needed new code* (the ingress finding above,
  not just a new Dockerfile), and its relationship to ADR-0022 (the `/.sc` shim mechanism this wish
  reuses unmodified) and ADR-0011 (Caddy HTTPS ingress, referenced by name in
  `create_plan_v2.go:58` — the ADR this wish's ingress-skip mechanism partially opts out of, for
  Dev Image machines only).
