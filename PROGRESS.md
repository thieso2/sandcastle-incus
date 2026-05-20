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

## Next Slice

- Create the initial Go module.
- Add a Cobra-based `sandcastle` CLI with `sc` binary support.
- Add `version`, `ls`, and `admin version` command skeletons.
- Add text/JSON output support.
- Add an e2e harness package that refuses to run unless explicitly enabled.

## Verification Log

- Pending: `go test ./...`
- Pending: build `sandcastle` and `sc`

## Open Scope

- Metadata, naming, CIDR allocation, Incus access, restricted certificates,
  project creation, sandbox lifecycle, DNS, certificates, Tailscale execution,
  local DNS/trust, host overrides, public routes, and full real-Incus e2e
  remain to be implemented.
