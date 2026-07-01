# Sandcastle v2 Topology — Design Overview

> Status: proposed, arrived at via a design grilling on 2026-07-01. Decisions are recorded in ADR-0011, ADR-0012, ADR-0013 (which supersede/amend ADR-0001, 0006, 0007). This doc ties them together and maps them onto the code that has to change. Nothing here is implemented yet.

## The model

```
User  (GitHub identity — auth unchanged; the owning/identity/infra boundary)
 │
 ├── sc-<username>            Incus project = per-user infra
 │     └── one per-user sidecar: CoreDNS + private Caddy + Tailscale subnet-router + global services
 │
 ├── sc-<username> (bridge, in the `default` Incus project)   ← one shared network for all the user's projects
 │
 ├── sc-<username>-<projectA> Incus project   (features.networks=false → uses the sc-<username> bridge)
 │     ├── its own default profile = shared /home + /workspace + login (a project config bundle)
 │     └── machines: web, db, …   → reachable as  web.projectA , db.projectA
 │
 └── sc-<username>-<projectB> Incus project
       └── machines: dev, …       → dev.projectB
```

- **Boundary = User.** Access, DNS, tailnet, CA, storage, and the OIDC issuer are all scoped to the user, spanning that user's project Incus-projects. (ADR-0011)
- **Project = its own Incus project.** Native instance namespace (no `{project}-{machine}` hack — `web.projectA` and `web.projectB` coexist), native per-project profiles, native per-project storage. No `-infra`/`-native` siblings. (ADR-0011)
- **One shared network per user** (`sc-<username>` bridge in `default`); projects are *not* network-isolated from each other (single owner, no threat model for it). (ADR-0011, Option A)
- **Per-user tailnet, subnet-router sidecar.** Machines are **not** tailnet nodes; they sit on the bridge (`10.x`) and are reached over the tailnet via the sidecar's advertised subnet. (ADR-0012)
- **DNS = `<machine>.<project>`.** The project name is the top label (user is implicit — per-user tailnet); resolves to the machine's bridge IP; per-user tailnet scopes uniqueness so no global registry. (ADR-0012)
- **Public routes stay shared infra** (P1): global Caddy terminates + proxies to the machine; per-user Caddy is private-only; Route Broker principal = User. (ADR-0013)

## What stays the same (low risk)

The **networking mechanics are today's**: subnet-router sidecar + machines on a bridge + CoreDNS serving static `name→bridge-IP` records + shared Caddy owning public `:80/:443`. The private bridge already lives in the `default` Incus project. So v2 is largely a **re-scoping (tenant→user) + hostname shortening + promoting project to a real Incus project**, not a new network design.

## What changes in the codebase

| Area | Change |
|---|---|
| `internal/naming` | Drop `{project}-{machine}` instance names (each project is its own Incus project). New project names: per-user infra `sc-<username>`, per-project `sc-<username>-<project>`. Retire `TenantIncusProjectName`/native/infra-per-tenant helpers. |
| `internal/meta` | `Tenant` → `User`. `Project` stops being a metadata label list; it's an Incus project. Drop reliance on the `user.sandcastle.project` instance key for grouping. `CloudIdentity`/`DockerAutostart` become per-project profile/config rather than `meta.Project` fields. |
| `internal/dns` (`render.go`, `apply.go`) | Hostname `<machine>.<project>` (drop the tenant label). One CoreDNS zone **per project**; build records by scanning all `sc-<username>-*` Incus projects instead of one tenant project. |
| `internal/incusx/tenant_create.go` | Split into: create per-user infra project `sc-<username>` + the consolidated sidecar (CoreDNS + private Caddy + Tailscale); create each project as `sc-<username>-<project>` with `features.networks=false` referencing the shared `sc-<username>` bridge (in `default`). One sidecar instead of per-tenant CoreDNS + Tailscale. |
| `internal/tenant` | Becomes user-scoped (`internal/user`?). Project create/delete = create/delete an Incus project, not append to a metadata list. |
| `internal/authapp` | Provision per-user infra + projects; OIDC issuer keyed **per user** (was per tenant); workload token claims `user/project/machine`. |
| `internal/routebroker` | Principal = User (restricted cert per user); scope routes to `sc-<username>-<project>`; target identity `sc-<username>-<project>/machine`. |
| Profiles | Each project's `default` profile carries the shared `/home` + `/workspace` volume pair + login (the "cheap machine spin-up" goal) — natively, since each project is its own Incus project. |
| `internal/cidr` | Per-user `/24` (was per-tenant); role addresses (.1/.2/.3) unchanged. |

## Open / deferred

- **Multi-host clusters:** shared Caddy reaching per-user bridges relies on single-host routing (ADR-0013). Revisit for clustering.
- **Project named after a real TLD** (`dev`, `com`) shadows that TLD on the user's own tailnet only — document, don't block.
- **Migration** from v1 (tenant-as-Incus-project + `{project}-{machine}`) to v2 is unaddressed here.
- **CONTEXT.md glossary rewrite** (Tenant→User, redefine Project) — pending ratification.
</content>
