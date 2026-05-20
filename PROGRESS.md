# Progress

Tracking implementation of GitHub issue #1: Sandcastle v1 Incus-backed
development sandboxes.

## Current Status

- Issue #1 PRD has been reviewed.
- Planning docs exist for the v1 specification, implementation milestones, and
  e2e testing phases.
- The Tailscale automation clarification from issue #1 is reflected in the
  spec, implementation plan, e2e plan, and `.env.default`: unattended
  `tailscale up` must advertise `tag:sandcastle` by default alongside the
  project private CIDR.
- Agent repo guidance exists in `AGENTS.md`, `CLAUDE.md`, and `docs/agents/`.
- Initial Go module exists at `github.com/thieso2/sandcastle-incus`.
- Cobra CLI skeleton exists for `sandcastle` and `sc`.
- Implemented initial commands:
  - `sandcastle version`
  - `sandcastle ls`
  - `sandcastle admin version`
- Implemented global `--output text|json`.
- Added e2e config loader/gate that refuses destructive e2e unless
  `SANDCASTLE_E2E=1` and defaults `SANDCASTLE_E2E_TAILSCALE_TAG` to
  `tag:sandcastle`.
- Added `internal/naming` for owner/project references, Incus project names, and
  reserved sandbox names.
- Added `internal/meta` for Sandcastle scalar metadata keys, versioned project
  and sandbox state blobs, and unmanaged-resource detection.
- Added `internal/cidr` for deterministic IPv4 project CIDR allocation,
  occupied/live-network overlap avoidance, exhaustion reporting, and role
  address derivation.
- Added `internal/project` read-only listing over an Incus project store
  abstraction.
- Wired `sandcastle ls` to the project listing service, with current default
  behavior returning an empty list until the real Incus adapter is added.
- Added `internal/config` admin defaults/env loading for remote, storage pool,
  CIDR pool, project prefix, infrastructure project, and base/AI image aliases.
- Extended `.env.default` with the admin/default CLI configuration variables.
- Added `sandcastle admin project list` / `ls` command shape backed by the same
  project listing service as `sandcastle ls`.
- Added `internal/incusx` Incus SDK adapter for project metadata listing via
  the existing Incus CLI config and `GetProjects()`.
- Wired production `sandcastle`/`sc` execution to use the Incus-backed project
  store by default, while unit tests still inject fake stores.
- Added admin project creation planning that derives the Incus project name,
  private CIDR, `sc-private` network, durable volume names, DNS/Tailscale
  sidecar names and addresses, default template, and project metadata config.
- Added `sandcastle admin project create <owner/project> --domain ... --dry-run`
  to render the creation plan in text or JSON. Non-dry-run execution still
  fails explicitly until the Incus executor is implemented.
- Added gated real-Incus e2e smoke test for project listing:
  `TestIncusProjectListingSmoke` skips unless `SANDCASTLE_E2E=1`.
- Added Incus project creation executor for idempotent project metadata
  creation/update, `sc-private` bridge creation, and durable custom volume
  creation for home, workspace, and project CA state.
- `sandcastle admin project create` now executes through the Incus executor
  when not run with `--dry-run`; dry-run remains offline and renders the plan
  without connecting to Incus.
- Added explicit sidecar plans for `sc-tailscale` and `sc-dns`, including
  stable private addresses, base image alias, root disk, bridged NIC, metadata,
  and start intent.
- Extended the Incus project create executor to create missing sidecar
  containers and start stopped existing sidecars.
- Added `internal/dns` initial CoreDNS renderer for a minimal authoritative
  project zone with NS/SOA records and no project-wide wildcard.
- Project create plans now include rendered CoreDNS files, and the Incus
  executor writes them into the DNS sidecar under `/etc/coredns`.
- Added project delete planning and `sandcastle admin project delete` command
  shape with required `--yes` confirmation and optional `--purge`.
- Added Incus delete executor that stops/removes sidecars, removes the private
  network, and only deletes durable volumes plus the Incus project when purge is
  explicit.

## Next Slice

- Extend e2e from read-only Incus detection to disposable project creation once
  the executor exists.
- Add diagnostics collection for failed disposable e2e project runs.
- Keep tests Incus-free for core logic, with e2e gated separately.

## Verification Log

- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle`
- Passed: `go build -o bin/sc ./cmd/sc`
- Passed: `./bin/sandcastle version`
- Passed: `./bin/sc --output json ls`
- Passed: `go test ./...`
- Passed: `go test ./...`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && go build -o bin/sc ./cmd/sc`
- Passed: `./bin/sandcastle --output json ls`
- Passed: `go test ./...`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle admin project list`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && go build -o bin/sc ./cmd/sc`
- Passed: `./bin/sandcastle version && ./bin/sc version`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json admin project create alice/myproject --domain myproject.project-tld --dry-run`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json admin project create alice/myproject --domain myproject.project-tld --dry-run | rg 'dnsFiles|Corefile|db.myproject|ns IN A'`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle admin project delete alice/myproject 2>&1 || true`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json admin project create alice/myproject --domain myproject.project-tld --dry-run`
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run TestIncusProjectListingSmoke -count=1 -v` with the expected skip when `SANDCASTLE_E2E` is not enabled.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json admin project create alice/myproject --domain myproject.project-tld --dry-run`

## Open Scope

- Restricted certificates, project creation, sandbox lifecycle, DNS,
  certificates, Tailscale execution, local DNS/trust, host overrides, public
  routes, and full real-Incus e2e remain to be implemented.
