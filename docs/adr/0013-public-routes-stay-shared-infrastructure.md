# Public Routes Stay Shared Infrastructure (Per-User Caddy is Private-Only)

> Status: proposed (v2 topology). Builds on ADR-0011 and ADR-0012. Captured 2026-07-01 during a design grilling.

Public HTTP(S) routes remain **shared infrastructure**: the one global infra Caddy owns the public `:80/:443`, terminates TLS, and reverse-proxies to the target machine on its per-user bridge — the current public-route data path, unchanged. The **per-user sidecar Caddy is private-only** (tenant/tailnet TLS). The Route Broker authorizes the request by the caller's Incus client certificate (principal = **User**), scopes the route to the user's `sc-<username>-<project>` Incus project, and targets `sc-<username>-<project>/machine`.

## Considered Options

- **(P1) Shared Caddy terminates + proxies to the machine.** Chosen — matches today; the per-user Caddy stays private-only.
- (P2) Shared L4/SNI router forwards `:443` by hostname to the target user's sidecar Caddy, which terminates and proxies locally. Rejected: adds a `hostname → user` map and forces **DNS-01 ACME** in every per-user Caddy (HTTP-01 is awkward behind an SNI router) — more moving parts for marginal gain.

## Consequences

- **The public path is preserved**, minimizing risk; only the route's *identity* changes from `tenant/project/machine` to `sc-<username>-<project>/machine`.
- **Route Broker principal becomes the User** (restricted Incus cert per user); it must authorize that the user owns the targeted project and scope the route to `sc-<username>-<project>`.
- **The shared Caddy must reach every user's bridge.** On a single Incus host this is host routing between `incusbr0` and the `sc-<username>` bridges (as today). A multi-host cluster would need overlay routing or a per-host public Caddy — flag for whenever multi-host is on the table.
- **The per-user sidecar Caddy never sees public traffic**, so Concept 5's "Caddy in the per-user sidecar" is the *private* half only; public HTTP stays global.
</content>
