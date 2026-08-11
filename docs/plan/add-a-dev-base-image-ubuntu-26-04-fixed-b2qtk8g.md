# Plan: a `dev` base image (Ubuntu 26.04)

Source: `docs/spec/add-a-dev-base-image-ubuntu-26-04-fixed-b2qtk8g.md` (read that first — this plan
does not repeat its reasoning, only slices its scope into independently-landable sessions).

## Slicing rationale

Four slices, ordered by real dependency only:

- **t1** (config/build plumbing) and **t2** (the Dockerfile) touch disjoint files and neither's Go
  code or shell script needs the other to exist to be written, built, or tested — they run in
  parallel, not sequence.
- **t3** (tenant-creation wiring: alias sync + ingress-skip) calls `admin.Images.Dev`, a field t1
  adds — real dependency, not a convenience ordering.
- **t4** (docs/ADR) is deliberately last: `docs/adr/0024-*` and the doc obligations record the
  *actual* shape of what shipped (exact `DefaultDevImageAlias` string, exact shared-script name,
  exact ingress-skip function name) — writing it before t1–t3 land would mean re-writing it anyway,
  and consolidating all cross-cutting doc edits into one session avoids three sessions editing
  `docs/usage.html`/`docs/e2e-sc2.md` concurrently.

Each slice below is independently mergeable and independently verifiable — none is a layer of an
unfinished cake.

## t1 — Config & build-pipeline plumbing for the `dev` template

No dependencies. Pure Go, no Dockerfile required to exist for these tests to pass.

**Files:**
- `internal/config/admin.go` — add `DefaultDevImageAlias` (same pattern as
  `DefaultBaseImageAlias`/`DefaultAIImageAlias`, lines 25-26: a stock public `images:` alias, no
  prebuilt-image requirement). Add `Images.Dev string` to the `Images` struct (line ~58-59), wire it
  through `AdminDefaults` (line ~77-78), `adminOverridesFromEnv` (`SANDCASTLE_DEV_IMAGE`, line
  ~111-112), `MergeAdmin` (line ~188-192), and `Validate()` (line ~227-231, same "X image alias is
  required" shape as Base/AI).
  - Note (don't resolve, just don't block on it): the exact alias string for
    `DefaultDevImageAlias` depends on whether Incus's public `images:` remote actually publishes an
    Ubuntu 26.04 entry and under what name — this is an external fact, not something this repo's
    source answers. Pick the most plausible candidate (e.g. `images:ubuntu/26.04`), document the
    choice in `implementation-notes.md`, and leave a one-line TODO-style note if you cannot verify
    it against a live Incus `images:` remote from this environment.
- `internal/images/plan.go` — `PlanBuild` (line 140, switch at 172-174), `aliasForTemplate` (line
  418, switch at 420-422), `templateAlias` (line 439, switch at 445-447) each grow a `case "dev":`
  arm. Per spec B6/B8, the `dev` template's version-arg validation requires Codex + Claude Code
  versions but **not** Gemini (unlike `ai`'s three-arg requirement) — mirror `ai`'s arm but drop the
  Gemini requirement.
- `internal/images/remote.go` — `PlanRemoteBuild` (line 138): the `template != "base" && template !=
  "ai"` guard (line 143) grows a `dev` allowance; the `if template == "ai"` special-casing (line 220)
  gets a look — check whether `dev` needs the equivalent branch or explicitly does not (it doesn't
  install Gemini, so whatever that branch does for the Gemini arg likely doesn't apply to `dev`).
- `internal/cli/admin.go` — `remoteBuildTemplates` (line 716-723) grows a `"dev"` case returning
  `[]string{"dev"}`, and the `"all"` case's returned slice grows to include `"dev"`. Update the
  `Use:` strings that currently read `base|ai` (lines 655 `build-remote base|ai|all`, 848 `build
  base|ai`, 919 `import base|ai source-ref`) to `base|ai|dev`. Any other argument validator in this
  file with a hardcoded `base`/`ai` switch (grep for `"base"` and `"ai"` string literals in this
  file beyond what's listed above) needs the same third arm.
- `mise.toml` — add `image:dev:build`, `image:dev:upload`, `image:dev:build-upload`,
  `image:dev:build-remote` tasks mirroring the existing `image:base:*`/`image:ai:*` tasks (lines
  61-201), pointing at `images/dev/Dockerfile` (built in t2 — this task's target not existing yet is
  fine, the task definition itself doesn't require the file to build cleanly, only to run). Fold
  `image:dev:build-upload` into `image:all:build-upload` (line 106-112) and
  `image:dev:build-remote` into `image:all:build-remote` (line 201) alongside `base`/`ai`.

**Tests (per spec's Test seams section):**
- `internal/images/plan_test.go` — `dev` arm of `PlanBuild`/`aliasForTemplate`/`templateAlias`:
  requires Codex+Claude versions, does NOT require Gemini; alias round-trips `admin.Images.Dev`.
- `internal/images/remote_test.go` — equivalent `dev` coverage for `PlanRemoteBuild`.
- `internal/cli/admin_test.go` — `remoteBuildTemplates("dev")` and `("all")` include `dev`.
- `internal/config/admin_test.go` — `Images.Dev` defaults to `DefaultDevImageAlias`,
  `SANDCASTLE_DEV_IMAGE` overrides it, `Validate()` behaves for `dev` as it does for `base`/`ai`, and
  — this is the acceptance-scenario-8 backward-compat check — `Validate()` on an `Admin` with no
  dev-specific env set still passes.

**Definition of done:** `go build ./...` and `go test ./internal/config/... ./internal/images/...
./internal/cli/...` pass; `base`/`ai` template behavior is bit-for-bit unchanged (acceptance scenario
8).

## t2 — `images/dev/Dockerfile`: the full interactive dev image

No dependencies (independent of the Go codebase and of t1 — buildable standalone with `docker build
-f images/dev/Dockerfile .`). Covers wish §§1-8 / spec §§B1(partial)-B9.

**Base:** `FROM ubuntu:26.04` — explicitly **not** `FROM sandcastle/base:latest` (base is Debian;
dev is a different OS lineage — see spec Ground truth / Solution paragraph).

**Minimum tenant plumbing** (mirrors `images/base/Dockerfile:43,48-49`, minus everything in the
out-of-scope list — no Tailscale, Samba, CoreDNS, Docker-in-Docker): `systemd systemd-sysv
openssh-server`.

**B2 — packages & shell:** `apt-get install --no-install-recommends git gh ripgrep fd-find make zsh`
plus `jq` (hard dependency of the statusline script below — not already present the way it is in
`base`, `images/base/Dockerfile:37`, so add it explicitly). `fd-find` installs as `fdfind`; symlink
`/usr/local/bin/fd -> /usr/bin/fdfind` at build time (same pattern as `base`'s `zellij`/`coredns`
hand-install, `images/base/Dockerfile:90-102`). Set zsh as the image's system default shell
(`chsh`/`/etc/default/useradd`) — belt-and-braces; the tenant login user's shell is already forced to
zsh elsewhere (`create_plan_v2.go:43`), this is about image-level consistency, not a load-bearing
mechanism.

**B1 obligation (no new mechanism — verification only):** just make sure zsh lands so
`/etc/zsh/zshrc` exists for the cloud-init `/.sc` shim's `append: true` step (ADR-0022,
`create_plan_v2.go:148-258`) to write into on first boot. Do not add any new SSH-agent code — that
mechanism is centrally shipped and image-agnostic already.

**B3 — zsh history:** bake the exact five-line block into `/etc/skel/.zshrc` (per-user rc under
`/etc/skel`, inherited by cloud-init's `useradd` at first boot) — **not** `/etc/zsh/zshrc`, which is
reserved for the `/.sc` shim and must not be touched:
```
HISTFILE=~/.zsh_history
HISTSIZE=10000
SAVEHIST=10000
setopt INC_APPEND_HISTORY
setopt SHARE_HISTORY
setopt HIST_IGNORE_ALL_DUPS
```

**B4 — starship:** install via the official `curl`-based installer at build time. Write the verbatim
`starship.toml` from the wish (§4) to `/etc/skel/.config/starship.toml` — no deviation from its
content.

**B5 — mise:** install via `curl https://mise.run | sh` run under `HOME=/etc/skel` (mirrors `ai`'s
`HOME=/etc/skel npx ...` trick, `images/ai/Dockerfile:45-46`), landing under
`/etc/skel/.local/bin`/`/etc/skel/.local/share/mise`. Write the verbatim
`~/.config/mise/config.toml` (wish §5: `go`, `herdr`, `node`, `starship`, all `"latest"`) to
`/etc/skel/.config/mise/config.toml`, then run `mise install` at build time under the same `HOME` so
tool versions are pre-resolved (no first-boot network fetch). `herdr` is a first-class entry in
mise's own tool registry — no special plugin handling needed.

In `/etc/skel/.zshrc`, activate mise and starship using `$HOME` (not a hardcoded path):
```
eval "$($HOME/.local/bin/mise activate zsh)"
eval "$(starship init zsh)"
```

**B6 — AI CLI tooling:** factor a small shared shell script out of `images/ai/Dockerfile`'s existing
`npm install -g @anthropic-ai/claude-code@… @openai/codex@…` step (lines 26-33) plus its
`HOME=/etc/skel npx skills@… add mattpocock/skills` step (lines 39-46), so `ai` and `dev` don't drift
on CLI versions. Put the shared script somewhere both Dockerfiles can `COPY`/reference (e.g.
`images/scripts/install-ai-cli-tools.sh`, taking version args). **`dev` installs Claude Code + Codex
only — no `@google/gemini-cli`** (wish §6 names only those two; no `ai`-parity requirement on tool
set). After factoring, rebuild `images/ai` and confirm it is byte-identical in installed package
versions to before (acceptance scenario 8 — `ai` must be unaffected). Skills install:
`HOME=/etc/skel npx -y skills@latest add mattpocock/skills -a claude-code -g -y` (same as `ai`).

**B7 — Claude Code status line:** write, byte-for-byte, the exact script from wish §7 (including the
Linux dual-form `date -r ... || date -d "@..."` reset-time clock) to
`/etc/skel/.claude/statusline-command.sh`, `chmod +x`. Write the `statusLine` stanza to
`/etc/skel/.claude/settings.json`:
```json
{
  "statusLine": {
    "type": "command",
    "command": "~/.claude/statusline-command.sh"
  }
}
```
Add a script-level test (bats, or a Go test that shells out) asserting exact byte output for fixed
payloads: all-fields-present, rate-limits-absent (API-key user), directory outside `$HOME`, directory
containing a glob metacharacter (the `set -f` requirement named in the wish/script comments).

**B8 — Codex status line (capability boundary, not a script):** Codex has no command-hook status
line — no config key runs an external script, no JSON-stdin/ANSI-stdout contract. Write
`/etc/skel/.codex/config.toml` with a `[tui] status_line = [...]` stanza selecting the closest
built-in items:

| Claude Code segment | Codex item | Fidelity |
|---|---|---|
| Model name | `model-with-reasoning` | full |
| Context bar + % | `context-used` | percentage only — no bar, no green/yellow/red ramp |
| Directory | `current-dir` | full (Codex's own formatting) |
| Git branch | `git-branch` | full, auto-omits outside a repo |
| 5h rate limit | `five-hour-limit` | **verify** remaining-vs-used sense on a live Codex session before documenting a specific percentage semantic |
| 7d rate limit | `weekly-limit` | same remaining-vs-used caveat |

No shared code with the Claude Code script is possible (nothing to run it on) — do not fake a bar,
a color ramp, or a reset-time display for Codex; there is no such field. Record the verified
remaining-vs-used sense in `implementation-notes.md`.

**B9 — Git setup:**
- Build-time: `/etc/gitconfig` (mirror `images/base/Dockerfile:159-160`'s `COPY gitconfig
  /etc/gitconfig` pattern using a new `images/dev/gitconfig`) with a common alias baseline: `co`,
  `br`, `st`, `last`, `unstage`, `amend`.
- First-login (not build-time — a shared image cannot hold per-tenant identity): a hook sourced from
  `/etc/skel/.zshrc` (or a script it calls) that runs `gh api user` to populate
  `git config --global user.name`/`user.email`, **only if** `gh auth status` succeeds **and** neither
  value is already configured (idempotent — never overwrites a hand-set identity).
- `gh auth login` itself is a documented first-run step, not image/hook code — the image/hook must
  never hold or write a credential. Do not write any plaintext token file, and do not put anything
  credential-shaped under `~/.ssh/`.

**Definition of done:** `docker build -f images/dev/Dockerfile .` succeeds; a container run from the
image has zsh as `$SHELL` for a `useradd`-created user, `fd`/`rg`/`gh`/`make`/`git` on `$PATH`,
`mise ls` shows `go`/`node`/`herdr`/`starship` pre-installed, `claude --version`/`codex --version`
resolve and `gemini` does not, the wish's synthetic-payload command against
`~/.claude/statusline-command.sh` produces the documented output, and `~/.codex/config.toml` has the
six-item stanza with no faked bar/ramp/reset-time. `images/ai/Dockerfile` still builds and its
installed CLI versions are unchanged after the B6 refactor.

## t3 — Tenant creation: Dev Image alias sync + ingress-skip mechanism

Depends on **t1** (`admin.Images.Dev` must exist for the alias-sync call below).

This is the one place the shape doc's scope undercounted the work: Caddy/TLS ingress is applied
once per project via the shared default profile (`V2DefaultProfileUserData`,
`internal/tenant/create_plan_v2.go:28-92`), unconditionally on image, whenever `jinja &&
signerURL != ""` (line 64). There is currently no per-image or per-machine toggle. Satisfying "no
HTTPS ingress for Dev Image machines" needs new code, not just a new Dockerfile.

**Files:**
- `internal/tenant/create_plan_v2.go:559` — `uniqueImageAliases(admin.Images.Base, admin.Images.AI)`
  grows to include `admin.Images.Dev`, deduplicated the same way, so a freshly created tenant
  project's local Incus image aliases resolve `--image <dev-alias>` without a fully-qualified
  `<remote>:<fingerprint>` reference.
- A new ingress-skip user-data function, structurally the same lever `V2BareUserData` (line 112,
  "applied as INSTANCE config, overriding the project default profile's user-data",
  `create_plan_v2.go:100-111`) already uses to override the project profile's user-data per-instance
  — **except unlike bare, a Dev Image machine keeps its login user, SSH key, and sshd; it only drops
  the Caddy branch** (lines 64-87 of `V2DefaultProfileUserData`). Research where machine creation
  currently decides between the project-default user-data and a bare override (`internal/cli/create_v2.go`,
  `internal/incusx/machine_create.go`, wherever `V2BareUserData` is actually invoked — it is not
  called from `create_plan_v2.go` itself, only defined there) to find the right hook point for a
  Dev-Image-machine override. The detection signal is most likely "the image alias/reference passed
  to `--image` matches `admin.Images.Dev`" — confirm this is knowable at the point the instance
  user-data is assembled, and if not, name the actual constraint you hit.
- `CONTEXT.md:261` — "the Base Image and the AI Image are its two variants" → include the Dev Image
  as a third variant (canonical domain vocabulary per `CLAUDE.md`) — small enough to land with this
  ticket since it directly documents the mechanism this ticket adds; leave the rest of the doc
  obligations to t4.

**Tests:**
- `internal/tenant/create_plan_v2_test.go` — extend wherever it asserts today's
  `uniqueImageAliases(admin.Images.Base, admin.Images.AI)` call to assert the Dev Image alias is
  included and deduplicated.
- A new `TestV2DevUserData*` (name it for whatever function you land), following the existing pattern
  `TestV2BareUserDataHasNoWayIn`/`TestV2BareUserDataBakesBootShims` (lines 209, 273 — pure string
  assertions on rendered cloud-init user-data, no Incus involved). Assert three independently
  falsifiable properties: a login user + SSH key + enabled sshd **are** present (unlike bare), the
  `/.sc` shims **are** baked (B1 depends on it), and the Caddy/generalize `write_files`/`runcmd`
  entries are **absent**.

**Definition of done:** `go test ./internal/tenant/...` passes with the new/extended tests; a
Dev-Image machine created end-to-end (manually, against a real Incus deployment, not just unit
tests) has no Caddy process, fetched no TLS leaf, does not answer on 443, but its private hostname
still resolves and `sc c`/`incus exec` still reach it (acceptance scenario 6) — note this manual
check in `implementation-notes.md` if you can't run it in this environment, don't claim it as verified
if you didn't do it.

## t4 — Documentation & ADR-0024

Depends on **t1**, **t2**, **t3** — records the actual shape of what shipped (exact
`DefaultDevImageAlias` string chosen, exact shared-script name/path, exact ingress-skip function
name), not a speculative design.

**Files:**
- `docs/usage.html` — document the `dev` template alongside `base`/`ai`: packages, default shell,
  mise-managed toolchain, AI CLIs, both status lines, the Nerd Font terminal prerequisite for the
  Claude Code statusline glyphs (U+F07C folder, U+E0A0 branch), and the explicit Codex capability-gap
  note from t2/§B8 (no bar, no ramp, no reset-time, remaining-vs-used caveat as verified).
- `docs/admin-developer-quickstart.html` — add `dev` alongside the existing base/AI image
  prepare/build/upload steps (mirroring the `SANDCASTLE_AI_IMAGE=sandcastle/ai:latest` convention
  with a `SANDCASTLE_DEV_IMAGE=sandcastle/dev:latest` example).
- `docs/e2e-sc2.md` — new phase covering `sc create --image <dev-alias>` provisioning (shell/starship/
  mise/tool versions, Claude Code/Codex CLI present, both statusline artifacts render/exist against
  the documented synthetic payloads, git identity self-populates on first login, no Caddy/tailscale/
  SMB surface), plus a one-line addition to the existing agent-forwarding PASS criterion
  (`docs/e2e-sc2.md:949-969`) noting it now also covers the Dev Image.
- `docs/adr/0024-*.md` — record: the three-Machine-Template pattern, the minimum-tenant-plumbing
  scope decision and *why it needed new code* (the ingress finding, not just a new Dockerfile), and
  its relationship to ADR-0022 (the `/.sc` shim this wish reuses unmodified) and ADR-0011 (Caddy
  HTTPS ingress, referenced by name at `create_plan_v2.go:58` — the ADR this wish's ingress-skip
  mechanism partially opts out of, for Dev Image machines only).
- `implementation-notes.md` — consolidate/finalize entries (if t1-t3 didn't already record their own):
  the exact shared-script factoring for B6, the exact ingress-skip function name/shape, the exact
  `DefaultDevImageAlias` string chosen (and whether it was verified against a live Incus `images:`
  remote or left as a best guess), and the Claude-vs-Codex install-mechanism note.

**Definition of done:** all four docs obligations from the spec are satisfied; no doc trails the code
it describes (`CLAUDE.md`'s standing rule); acceptance scenarios 1-8 in the spec are each either
demonstrated or explicitly flagged as unverified-in-this-environment with the reason why.
