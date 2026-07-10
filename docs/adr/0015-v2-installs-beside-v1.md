# v2 Installs as a Parallel Deployment Beside v1

> Status: **superseded — v1 retired.** The requirement to install beside a running
> v1 instance is moot now that v1 no longer exists; the v1 topology and its code were
> removed entirely in issue #52, so installs no longer coexist. Multi-installation
> coexistence on one host is instead served generally by the `--prefix` install flag
> (see `../topology.md`). Retained as a dated decision record. Built on ADR-0011→0014.
> Captured 2026-07-01.

v2 is a **different topology** (ADR-0011: User boundary, Project = Incus project) and migration is **re-onboard, preserving volumes** (ADR-0014). To migrate without a flag-day, v2 is deployed as a **separate Sandcastle deployment alongside the live v1 deployment** on the same Incus host (`big`); users migrate one at a time; v1 is retired only after v2 is verified.

## Decision

- **v2 is its own deployment, not a flag inside v1.** A second Infrastructure Seed (ADR-0008), distinct Deployment Name, with a distinct **project prefix** (e.g. `sc2`), infra project, **non-overlapping CIDR pool**, Auth Hostname, GitHub OAuth app, and Auth DB path. v2's sidecars run the **v2 build**; v1's deployment (old build) is untouched. No single binary needs a runtime v1/v2 switch — each deployment runs its own version.
- **Coexistence sharing:** the two deployments share the one Incus daemon (client certs coexist; restricted certs are project-scoped), the `default` Incus project (prefixed bridge names avoid collision), and the Tailscale account (tailnets are per-user; prefixes keep names distinct).

## Prerequisite (blocker — must land before any second deployment)

Two host-global singletons are hardcoded and must become **deployment-scoped** (seed-configurable):

1. **Host front door.** The infra Caddy publishes `tcp:0.0.0.0:80` / `:443` via proxy devices (`internal/infra/plan.go:648-654`). There is a **single public IP**, so only one deployment can own `:80/:443`. **Decision: make the Caddy proxy listen ports seed-configurable and give v2 alternate ports (e.g. `:8080/:8443`) during coexistence**; v1 keeps `:80/:443`. At cutover (v1 retired) the primary deployment takes `:443`.
   - *Private traffic is unaffected* (the per-user sidecar Caddy is reached over the tailnet; ports don't matter).
   - *Public-route caveats on the alternate port:* (a) public URLs carry the port (`https://host:8443`) — acceptable for the migration window; (b) **ACME HTTP-01/TLS-ALPN-01 require `:80/:443`** (held by v1), so a v2 Caddy on `:8443` must use **DNS-01** for real certs, or run **internal TLS** (self-signed, test-only) until it takes `:443`.
   - *Rejected for now:* a shared L4/SNI router owning `:443` and forwarding by hostname to each deployment's Caddy — only needed if v1 **and** v2 must both serve public on `:443` *permanently*; unnecessary for migrate-then-retire.
2. **Infra bridge + sidecar octets.** The infra sidecars sit on `incusbr0` (`plan.go:41`) at fixed last-octets `.20/.21/.22` (`infrastructure.go:206-211`). A second deployment derives the same three addresses on the same bridge → collision. Make the **infra bridge name and the octets seed-configurable**, or give each deployment its **own infra bridge**.

## Consequences

- **This is the migration vehicle.** Parallel deploy → migrate users + volumes per ADR-0014 (R1 re-onboard) → retire v1. Rollback is trivial (v1 never stopped).
- **The implementation plan's `v2` feature flag is dropped** in favor of "v2 is a parallel deployment": v2 code is developed and tested on a throwaway parallel deployment, then stood up beside v1 for real. No in-binary topology switch.
- **Non-overlapping CIDR is mandatory** (else tenant/user subnets collide on the host).
- **Image aliases** (`base`/`ai`) are host-level; prefix them per deployment if v1 and v2 need independent image versions.
- The prerequisite work (deployment-scoping the front door + infra bridge/octets) also unlocks running **any** number of Sandcastle deployments on one host, not just v1+v2.
