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

## Next Slice

- Add the Incus SDK adapter for project metadata listing.
- Add initial admin project create planning/orchestration types.
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

## Open Scope

- Metadata, naming, CIDR allocation, Incus access, restricted certificates,
  project creation, sandbox lifecycle, DNS, certificates, Tailscale execution,
  local DNS/trust, host overrides, public routes, and full real-Incus e2e
  remain to be implemented.
