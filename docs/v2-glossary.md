# Sandcastle v2 Glossary (proposed)

> The ratified v2 domain vocabulary from the design grilling of 2026-07-01 (ADR-0011, ADR-0012, ADR-0013; overview in `v2-topology.md`). **`CONTEXT.md` remains canonical for the current v1 code** until v2 is implemented; this file supersedes it *for v2 work only*. When v2 lands, fold this into `CONTEXT.md` and retire the superseded terms.

## Core nouns

- **User** — The GitHub-authenticated identity that is the top-level owning, identity, and infrastructure boundary. Owns one per-user tailnet, one per-user sidecar, and many projects. *Replaces v1 **Tenant** / **Personal Tenant**.*
- **Project** — An Incus project owned by a User (`sc-<username>-<projectname>`), holding that project's machines, profiles, storage, and DNS zone. Created/deleted as a real Incus project, not a metadata label. *Redefines v1 **Project** (was a namespace label inside the one tenant Incus project).*
- **Machine** — A container/VM inside a project. Its name is unique within its project (two projects may both have `dev`); it sits on the shared per-user bridge with a private IP and is **not** a Tailscale node.
- **Per-user infra project** — The `sc-<username>` Incus project holding the user's single sidecar. This is where the old per-tenant `-infra`/`-native` split went: up to one project per user, not per project.
- **Per-user sidecar** — One instance in `sc-<username>` running CoreDNS, a private-only Caddy, the Tailscale subnet-router, and other global services for all of the user's projects. *Consolidates v1's per-tenant CoreDNS + Tailscale sidecars.*
- **Shared per-user network** — One bridge, `sc-<username>`, created in the `default` Incus project; every `sc-<username>-*` project references it via `features.networks=false`. Projects are **not** network-isolated from each other.
- **Per-user tailnet** — The Tailscale network dedicated to one User; the sidecar is its subnet-router advertising the `sc-<username>` subnet. *Replaces v1 tenant tailnet (ADR-0006 tenant→user).*
- **Machine hostname** — `<machine>.<project>` (two labels; the User is implicit because the tailnet is per-user). Resolves via the per-user CoreDNS to the machine's bridge IP, reachable over the tailnet subnet route. *Replaces v1 `<machine>.<project>.<tenant>`.*
- **Project DNS zone / domain** — Each project is a CoreDNS zone named after the project (the project name is the top DNS label). Unique only within a user (per-user tailnet), so no global registry.
- **Per-user CA** — The certificate authority for private machine TLS hostnames, scoped to the User. *Replaces v1 Tenant CA.*
- **Project profile** — Each project's Incus `default` profile bundles the shared `/home` + `/workspace` volume pair and login (user/SSH/idmap). This is how a machine "in a project" gets its shared, persistent home and workspace for free. *Replaces the v1 `meta.Project` config-default fields (CloudIdentity/DockerAutostart) and the per-machine inline device injection.*

## Unchanged from v1

- **GitHub OAuth login / allowlist / device login** — auth flow unchanged (the User Key is still the normalized GitHub username; ADR-0003, ADR-0004).
- **Public Route** — a public HTTP(S) hostname → a machine, served by **shared** infrastructure Caddy (one host owns `:80/:443`); Route Broker authorizes and mutates global route infra. In v2 the target identity is `sc-<username>-<project>/machine` and the broker principal is the User (ADR-0013).
- **Route Broker**, **Workload Identity / OIDC**, **Infrastructure Seed File**, **Auth Hostname** — concepts unchanged; re-scoped from tenant to user where they carried a tenant (e.g. the OIDC issuer is now per-user).
- **CIDR** — one `/24` per user (was per tenant); role addresses gateway `.1`, Tailscale `.2`, DNS `.3` unchanged.

## Retired terms

- **Tenant**, **Personal Tenant**, **Tenant DNS Suffix**, **Incus Project Mapping** (tenant=project), **Incus Instance Name** as `{project}-{machine}`, per-tenant `-infra`/`-native` projects.
</content>
