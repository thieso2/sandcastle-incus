# User as the Owning Boundary; Project as the Incus Project

> Status: **accepted (implemented).** Supersedes ADR-0001; amends ADR-0006 and ADR-0007. **As shipped, ADR-0016 amended this**: the boundary is the **Tenant** (handle-keyed, `sc2-<tenant>` projects), not a GitHub-identity "User". Captured 2026-07-01; implemented since.

In v2, the **User** (GitHub identity) is the top-level owning, identity, and infrastructure boundary. A **Project** is promoted from a lightweight metadata label to **its own Incus project**, with no `-infra`/`-native` sibling projects. All of a user's projects share **one network** (Option A below): the private bridge lives in the `default` Incus project (`features.networks=false`) and every project references it, so a single per-user sidecar reaches every project. This replaces ADR-0001's "tenant = Incus project" — the term *Tenant* collapses into *User*.

## Considered Options

- **(A) One shared network per user** — all of the user's project Incus-projects reference one bridge; one per-user sidecar serves them all. **Chosen.**
- (B) One network per project — true per-project network isolation, requiring the per-user sidecar to multi-home a NIC per project plus a subnet + Tailscale route-approval per project. Rejected: a single user owns all their own projects, so there is no threat that per-project network isolation defends against; the plumbing cost is not justified.

## Consequences

- **The boundary is the User.** Access control, the private network, DNS, the Tailscale tailnet, the certificate authority, storage, and the OIDC issuer are re-scoped from tenant to **user**, straddling all of that user's project Incus-projects.
- **Projects are real Incus projects**, giving each its own instance namespace, profiles, and storage — for free. This **retires the `{project}-{machine}` instance-name hack** (two projects can each hold a machine named `dev` natively) and lets each project carry its own `default` profile (the shared-home/workspace + login bundle).
- **No per-project network isolation.** Projects are isolated only at the instance/profile/storage level, not the network level. Accepted because the single owner trusts all their projects.
- **Per-project DNS domains** ("user-managed TLDs") are served as multiple zones by the one per-user sidecar on the shared bridge (mechanics in a follow-up ADR).
- **Caddy split:** the per-user sidecar runs a **private-only** Caddy (tenant/tailnet TLS); **public HTTP routes remain shared infrastructure** (one host binding `:80/:443`), since a per-user Caddy cannot own the single public ingress.

## Naming

- **Per-user infra project:** `sc-<username>` — one Incus project per user, home of the per-user sidecar (DNS + private Caddy + Tailscale + global services). This is where `-infra` went: not per-project, but per-user.
- **Project:** `sc-<username>-<projectname>` — one Incus project per project, holding that project's machines, profiles, and storage. No `-infra`/`-native` siblings.
- **Shared network:** a bridge named `sc-<username>` created in the **`default`** Incus project (Incus requires shared networks to live in `default`; this matches current practice where `sc-<tenant>` bridges already live in `default`). Every `sc-<username>-*` project (and `sc-<username>`) runs `features.networks=false` to reference it, so the one per-user sidecar reaches all projects.

## Vocabulary changes (to fold into CONTEXT.md when ratified)

- **Tenant** → retired; its role (owner, identity, infra boundary) moves to **User**.
- **Project** → redefined: an Incus project owned by a User; carries its own machines, profiles, storage, and DNS domain.
- **Incus Project Mapping** → "each Project is one Incus project; a User owns many."
- **Incus Instance Name** → no longer `{project}-{machine}`; machine names are scoped by the project's Incus project.
</content>
