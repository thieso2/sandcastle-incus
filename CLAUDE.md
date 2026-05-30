# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`sandcastle-incus` is a Go CLI + Auth App that provisions multi-tenant "Sandcastle" infrastructure on [Incus](https://linuxcontainers.org/incus/). The system manages tenant namespaces, projects, machines (Incus containers/VMs), Tailscale networking, public routes, and workload identity. Domain vocabulary is canonical in `CONTEXT.md`; use it when naming code.

## Commands

- Build: `make build` (drops binaries at `bin/sandcastle`, `bin/sandcastle-admin`, and `sc`/`sc-adm` symlinks) or `mise run build` (same + copies to `/tmp/`)
- Cross-compile for Linux: `mise run build:linux-amd64`
- Unit tests: `go test ./...` or `make test`
- Single package/test: `go test ./internal/tenant -run TestCreatePlan`
- Lint: `go vet ./...` (no separate lint step; format with `gofmt`)
- Integration tests (real Incus): `SANDCASTLE_INCUS_INTEGRATION=1 go test ./internal/integration`
- E2E tests (creates/destroys real tenants): `SANDCASTLE_INCUS_E2E=1 go test ./internal/e2e`
- Safe E2E suite: `make e2e-safe` (unit + gated + local stages via `scripts/e2e.sh`)
- Build base image locally: `mise run image:base:build-upload` (docker on the host; requires `SANDCASTLE_REMOTE`)
- Build AI image locally: `mise run image:ai:build-upload` (resolves latest npm versions, requires `SANDCASTLE_REMOTE`)
- Build + publish to GHCR via the Image Builder appliance: `mise run image:all:build-remote` (requires `SANDCASTLE_REMOTE` and `SANDCASTLE_GHCR_TOKEN`; see `docs/adr/0010-image-builder-appliance.md`)

## Architecture

Two binaries under `cmd/`:

- **`sandcastle`** — user-facing CLI (`sc` alias)
- **`sandcastle-admin`** — admin CLI (`sc-adm` alias)

Both are assembled in `internal/cli/`. `root.go` and `admin_root.go` build the Cobra command tree and wire `commandConfig` — a large dependency-injection struct holding interface values for every domain capability. Commands are split into per-domain files (`machine_lifecycle.go`, `login.go`, `route.go`, `project.go`, etc.) rather than one monolithic file.

### Domain packages under `internal/`

**Tenant and machine lifecycle:**
- `tenant` — `CreatePlan`/`DeletePlan`, `List`, `Status`, `Update`, `SSHKeyUpdater`, `TopologyStore`; defines the `Topology` type read from live Incus state
- `machine` — `Creator`, `Store`, `Connector`, `Controller`, `PortSetter`; `resolve.go` handles cross-project machine lookup with unique-search semantics
- `incusx` — Incus operations: implements all tenant/machine interfaces against the Incus API (`tenant_create.go`, `machine_create.go`, `machine_lifecycle.go`, etc.); `topology.go` reads live Incus state into `tenant.Topology`; `shared_remote.go` manages per-remote Incus config paths

**Auth App (`authapp`):**
Replaces the old `server` package. Runs as a standalone HTTPS service at the Auth Hostname. Handles:
- GitHub OAuth login and web registration (`github.go`, `login.go`)
- CLI Device Login: browser-assisted device authorization that issues an Incus Certificate Add Token (`device.go`)
- User + allowlist management (`store.go`, `allowlist.go`, `access.go`)
- Sandcastle OIDC Provider — issues short-lived Workload Identity Tokens for machines (`oidc.go`, `cloud_identity.go`)
- Machine provisioning on first CLI Device Login (`provision.go`)
- Workload identity token issuance via Machine Runtime Secret (`workload.go`)

Auth Database is SQLite (not the old JSON user DB). Stored at the configured `--database-path`.

**Infrastructure (`infra`):**
- `plan.go` — plans shared infrastructure creation/deletion
- `seed.go` — `Seed` struct; reads/writes the Infrastructure Seed File (`~/.config/sandcastle/<deployment>.seed.yml`), which is the operator bootstrap bundle carrying Auth Hostname, Incus remote, CIDR pool, TLS material, and image references
- `caddy_data.go` — exports cached Caddy ACME data for infra recreation

**Routes and Route Broker:**
- `route` — `Manager` interface, `plan.go` (create/delete plans), `dnsproof.go` (DNS proof-of-ownership for public route hostnames)
- `routebroker` — narrow service that authorizes user route requests and mutates global route infrastructure (`authorize.go`, `serve.go`); authenticates callers via Incus client certificate

**Local host setup:**
- `localdns` — manages per-tenant DNS resolver configuration (`/etc/resolver/` on Darwin, `systemd-resolved` on Linux)
- `localtrust` — installs the Tenant CA into the local system trust store
- `usertrust` — user-facing trust management
- `hostoverride` — manages `/etc/hosts` entries for machine private hostnames

**Supporting packages:**
- `naming` — all Incus resource naming / slug logic (Incus project names, instance names, network names)
- `config` — `SandcastleConfig` (user config at `~/.config/sandcastle/config.yml`), `Admin` (merged from file + env), `Images`; config resolution order: CLI flags → env vars (`SANDCASTLE_*`) → seed file → built-in defaults
- `meta` — metadata key constants
- `cidr` — CIDR pool allocation and subnet role assignment
- `domain` — TLD/IANA special-use validation for Tenant DNS Suffixes
- `tailscale` — `Runner` wraps `tailscale up` and status checks
- `images` — `Manager`/`Builder`/`Importer`/`Uploader` for Sandcastle base and AI OCI images; `plan.go` generates image build plans
- `caddy`, `dns`, `certs` — generators for Caddyfile, dnsmasq config, and CA/leaf cert layouts (used by `incusx`)
- `e2e`, `integration` — test harnesses (gated by env vars)

### Configuration

User CLI config lives at `~/.config/sandcastle/config.yml` (`tenant`, `project`, `remote`, `admin_remote`). Per-remote restricted Incus credentials are stored at `~/.config/sandcastle/<remote>/incus/`. Admin commands use the global `~/.config/incus/` (admin TLS certs). The user CLI resolves `INCUS_CONF` to the per-remote directory so restricted certs are used automatically.

## Documentation

Whenever you change a CLI command, flag, mise task, script, or workflow, update the relevant docs in `docs/` in the same commit:

- `docs/usage.html` — CLI reference for all `sandcastle` commands and mise image tasks
- `docs/admin-developer-quickstart.html` — step-by-step admin onboarding guide

Do not leave docs trailing behind code changes.

## Conventions

- Issues and PRDs live in **GitHub Issues** (`thieso2/incus-sandcastle`) — use `gh` CLI. See `docs/agents/`.
- Domain terms from `CONTEXT.md` are canonical — use them in code, comments, and messages. ADRs under `docs/adr/` record resolved design decisions.
- Generated artifacts (`server.crt`, `server.key`, `sandcastle-users.json`, `*.iso`, `*.seed.yml`) are gitignored — don't commit them.
- `DOCKER_ARCHITECTURE.md` maps the original Docker+Sysbox design to Incus primitives — read it before changing topology or networking.

## Agent skills

### Issue tracker

Issues and PRDs for this repo are tracked in GitHub Issues. See `docs/agents/issue-tracker.md`.

### Triage labels

This repo uses the standard five-label triage vocabulary. See `docs/agents/triage-labels.md`.

### Domain docs

This repo uses a single-context domain documentation layout. See `docs/agents/domain.md`.
