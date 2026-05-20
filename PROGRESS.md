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

## Next Slice

- Add naming and metadata packages for owner/project parsing, Incus project
  names, scalar metadata keys, and versioned JSON state blobs.
- Wire read-only project listing to an Incus access abstraction.
- Keep tests Incus-free for core logic, with e2e gated separately.

## Verification Log

- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle`
- Passed: `go build -o bin/sc ./cmd/sc`
- Passed: `./bin/sandcastle version`
- Passed: `./bin/sc --output json ls`

## Open Scope

- Metadata, naming, CIDR allocation, Incus access, restricted certificates,
  project creation, sandbox lifecycle, DNS, certificates, Tailscale execution,
  local DNS/trust, host overrides, public routes, and full real-Incus e2e
  remain to be implemented.
