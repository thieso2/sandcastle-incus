# Per-User Subnet-Router Sidecar; Machines Reachable as `machine.project`

> Status: **accepted (implemented).** Amends ADR-0006 and ADR-0007. **As shipped, ADR-0016 amended this**: DNS is flat `<machine>.<suffix>` (one zone per tenant), not `<machine>.<project>`. Builds on ADR-0011. Captured 2026-07-01; implemented since.

Each user has one **per-user tailnet** and one **per-user sidecar** (`sc-<username>`) that runs CoreDNS, a private-only Caddy, the Tailscale subnet-router, and other global services. VMs/containers are **not** Tailscale nodes: they sit on the shared per-user bridge (`sc-<username>`, `10.x`) and are reachable over the tailnet because the sidecar advertises that subnet. Machine hostnames are **`<machine>.<project>`** (two labels; the user is implicit because the tailnet is per-user), resolving to the machine's bridge IP.

## Decision

- **Subnet-router model (unchanged from today).** The sidecar advertises the user's private subnet; machines keep only bridge IPs. `ping machine.project` → the machine's `10.x`, routed over the per-user tailnet. No `tailscaled` in machines, no per-machine tailnet node, no per-machine route approval.
- **Per-project DNS zone = the project name.** Each `sc-<username>-<project>` project is a CoreDNS zone named `<project>`; `<machine>.<project>` → bridge IP. The per-user tailnet scopes uniqueness, so no global TLD registry is needed (two users may both have project `acme`).
- **CoreDNS builds zones by scanning `sc-<username>-*` Incus projects.** Because each project is now its own Incus project, the machine→IP records come from iterating the user's project Incus-projects — the `user.sandcastle.project` metadata label is no longer needed to group machines.

## Considered Options

- Machines as direct Tailscale nodes (each runs `tailscaled`, own `100.x`). Rejected: `tailscaled` in every VM/CT, a tailnet node + auth key/tag per machine, and it contradicts "Tailscale lives in the one per-user sidecar."
- Real user-owned public domains per project (with DNS proof + split-horizon). Deferred: private per-project labels are cheaper and, under a per-user tailnet, collision-free without a registry.

## Consequences

- **Mechanics are today's mechanics** (subnet-router + static bridge records), lowering risk; the change is naming/scoping, not a new network model.
- **DNS rendering must scan multiple Incus projects** (`sc-<username>-*`) instead of one, and stops reading `user.sandcastle.project`.
- **Single-label project zones** (`acme`, `backend`) mean naming a project after a real TLD (e.g. `dev`) shadows that TLD's public resolution — but only on that user's own tailnet, and only for their devices. Acceptable; document it.
- **Public HTTP routes remain shared infrastructure** (one host owning `:80/:443`); the per-user Caddy is private-only. Public-route targeting under the new `sc-<username>-<project>/machine` identity is a separate decision (follow-up ADR).
</content>
