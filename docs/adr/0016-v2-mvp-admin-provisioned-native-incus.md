# v2 MVP: Admin-Provisioned Tenants, Native Incus Access, OAuth Deferred

> Status: proposed (v2 MVP). Amends ADR-0011 (Tenant survives as the boundary), ADR-0012 (flat per-tenant DNS, suffix retained), and ADR-0015 (front-door/parallel-seed work dropped for the MVP). Builds on ADR-0011→0014. Captured 2026-07-01 during a design grilling.

The first shippable slice of v2 runs on `big` **beside the live v1 deployment** and gets tenants creating projects and machines with **native `incus` commands** — **without** GitHub OAuth or public routes (both deferred, because we do not yet own the public endpoint). Tenants are **provisioned by an admin** (`sc-adm`), not by OAuth device login. This ADR records the nine decisions that define that slice.

## Context

v1 provisions a tenant only through the Auth App's GitHub OAuth device-login path (`authapp/provision.go`). The MVP defers OAuth and the public front door, so provisioning moves to the admin CLI, and the tenant's day-to-day interface becomes the native Incus client (restricted TLS cert) plus a thin Sandcastle broker for project lifecycle. The freeform-launch DNS work (`dns.domain` on the bridge + CoreDNS fallthrough) already makes native `incus launch` instances resolvable, which this MVP builds on.

## Decisions

1. **Boundary = Tenant (admin-minted handle), not User.** With no GitHub identity, the boundary is an arbitrary handle the admin chooses (e.g. `acme`). ADR-0011's "Tenant collapses into User" is **amended**: Tenant *survives* as the owning/identity/infra boundary; **User** becomes the later auth identity that *owns* a Tenant (1:1 for personal tenants) when OAuth lands. Names are keyed on the tenant handle.

2. **Split the interface.** *Project lifecycle* is Sandcastle-mediated (creates the Incus project, wires the shared bridge, installs the `default` profile, registers DNS, and extends the tenant's restricted cert). *Instance lifecycle within a project* is **native `incus`** (`launch`, `start/stop/delete`, `exec`, `file push`, …) over the tenant's restricted cert. Restricted Incus certs cannot create projects, so `incus project create` is intentionally not the tenant path.

3. **Project creation is tenant self-service via a broker** (not admin-only). The tenant runs `sc project create`; a server-side admin-credentialed broker performs the privileged scaffolding. The tenant holds no Incus admin.

4. **One central Sandcastle Broker appliance**, generalizing the v1 Route Broker: a single infra container with the Incus admin unix socket bind-mounted, serving TLS on **public `big:9443`**, authenticated by the tenant's **same restricted Incus client cert** used for the Incus API on `:8443`. Route handling stays dormant (public routes deferred); the appliance serves the project endpoint. The broker maps cert fingerprint → tenant principal, validates ownership, creates `sc2-<tenant>-<project>` + profile + DNS zone, and extends the cert's project list via `usertrust`.

5. **Per-tenant Tailscale — each tenant has her own tailnet/account.** The sidecar's `tailscale up --advertise-routes=<the /24> --auth-key=<tenant key>` joins **the tenant's own tailnet** on first run. The auth key is a **per-tenant onboarding input** (`sc-adm tenant create --tailscale-authkey=…`), never committed or serialized. Real per-tenant isolation, no shared-account ACLs.

6. **`sc-adm tenant create <tenant> --tailscale-authkey=<key>` is the atomic bring-up.** It: allocates a `/24` from the v2 pool; creates bridge `sc2-<tenant>` in `default`; creates infra project `sc2-<tenant>` + the sidecar (CoreDNS + Tailscale subnet-router + Caddy) and runs `tailscale up`; **seeds the first project** `sc2-<tenant>-default` (Incus project, `features.networks=false`) + `default` profile (shared `/home`+`/workspace`, login, UID 2000) + flat DNS zone; generates the **per-tenant CA**; and mints a **restricted Incus Certificate Add Token** scoped to `[sc2-<tenant>-default]`. The tenant redeems the token with their own Incus client, so **the private key never leaves the tenant**.

7. **Per-tenant CA, installed client-side on connect.** Each tenant gets her own CA at create time; the sidecar's private Caddy serves machine HTTPS with leaves signed by it; `sc connect` installs the tenant CA into the *connecting client's* trust store (v1 `usertrust`/`localtrust` pattern), lazily at connect time. (HTTPS-via-CA is not in the e2e acceptance bar but the CA is provisioned.)

8. **Coexist with v1 on `big` cheaply — drop ADR-0015's Phase P for the MVP.** The MVP serves nothing on `:80/:443` and runs no Auth App, so the front-door/parallel-seed refactor is unnecessary. Coexistence is: **`sc2-` project/bridge prefix** (no collision with v1's `sc-`), a **non-overlapping CIDR pool `10.249.0.0/16`** (v1 owns `10.248/16`), the **broker on the free host port `:9443`** (v1's route broker is bridge-internal only), and the broker placed on `incusbr0` at a free octet. v1 is untouched.

9. **DNS is flat per-tenant: `<machine>.<suffix>`, suffix = the tenant handle.** All of a tenant's projects share the one `sc2-<tenant>` bridge (ADR-0011 Option A), so dnsmasq publishes every instance under one `dns.domain` and cannot express a project label; the single sidecar (one NIC, one advertised `/24`) therefore reaches **every** project. Machine names are unique **within the tenant**. This **amends ADR-0012** (which proposed per-project zones / dropping the suffix): the deployed freeform-launch code keeps a single-label suffix and one zone per tenant, and that model is adopted. Inter-project network isolation is intentionally absent (single owner, no threat model).

## Acceptance (definition of done for the MVP on `big`)

```
sc-adm tenant create acme --tailscale-authkey=$TS_AUTHKEY
incus remote add big https://65.21.132.31:8443 --token=<tok>
incus launch images:debian/13 web              # into sc2-acme-default
ssh web.acme                                    # ✅ over acme's tailnet
sc project create backend                       # broker at big:9443
incus launch images:debian/13 api --project sc2-acme-backend
ssh api.acme                                    # ✅ one sidecar, two projects
```

Done = both `ssh web.acme` and `ssh api.acme` succeed from the tenant's tailnet-connected machine. HTTPS-via-CA and public routes are out of scope for this bar.

## Consequences

- **No new public auth surface** beyond the broker on `:9443` (client-cert authenticated). OAuth, the Auth App, public routes, and the front-door refactor are a coherent deferred "step 2".
- **The tenant's mental model is plain Incus** for machines, plus `sc project create` and `sc connect`. `sc create`/v1 machine verbs are not required for the MVP.
- **Project label is absent from hostnames.** If per-project hostnames become a requirement, that re-opens per-project bridges (ADR-0011 Option B) and multi-homing the sidecar — explicitly rejected here.
- **Machine-name uniqueness is per-tenant**, not per-project — must be documented in tenant-facing docs.
- **Deferred, tracked for step 2:** GitHub OAuth + Auth App; public routes + front-door port work (ADR-0015 Phase P); Tailscale inter-tenant ACL isolation; HTTPS-via-CA polish.
</content>
</invoke>
