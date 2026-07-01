# Progress

This file tracks the current Sandcastle Incus implementation state. Historical
owner/project/sandbox milestones were superseded by the tenant/project/machine
model described in `docs/sandcastle-v1-spec.md`.

---

# v2 MVP — Active Build (ADR-0016)

Goal: full v2 MVP per **ADR-0016**, deployed to `big`, until the e2e acceptance
script is green. Branch: `freeform-launch-profiles-dns`.

## e2e acceptance (definition of done)
```
sc-adm tenant create acme --tailscale-authkey=$TS_AUTHKEY
incus remote add big https://65.21.132.31:8443 --token=<tok>
incus launch images:debian/13 web            # into sc2-acme-default
ssh web.acme                                  # ✅
sc project create backend                     # broker at big:9443
incus launch images:debian/13 api --project sc2-acme-backend
ssh api.acme                                  # ✅ one sidecar, two projects
```

## Phases

| # | Phase | Status |
|---|-------|--------|
| 1 | v2 naming helpers (`sc2-<tenant>`, `sc2-<tenant>-<project>`, bridge) | 🔨 |
| 2 | `sc-adm tenant create`: infra project + sidecar + bridge + `default` project + profile + CA + restricted trust token | ⬜ |
| 3 | Sandcastle Broker: `project create/delete` endpoint (generalize route-broker) + appliance deploy | ⬜ |
| 4 | `sc project create/delete` client → broker | ⬜ |
| 5 | Flat DNS `<machine>.<suffix>` wiring (Corefile + dnsmasq + localdns) | ⬜ |
| 6 | Per-tenant CA install on `sc connect` | ⬜ |
| 7 | Deploy to `big` + run acceptance script until green | ⬜ |

Legend: ⬜ todo · 🔨 in progress · ✅ done · ⚠️ blocked

## v2 Log
- 2026-07-01: ADR-0016 ratified + committed (`ed1b21e`). Incus 7.2 client on this
  CT; `big` set as default remote. Starting Phase 1 (v2 naming).
- Phase 1 ✅ (`73c8ea5`): v2 naming helpers + tests.
- Phase 2a ✅ (`d06bf07`): `tenant.PlanCreateV2` + tests.
- Phase 2 topology **proven manually on big** (tenant `acme`, prefix `sc2`,
  CIDR `10.249.0.0/24`), to be codified into the incusx executor:
  - Bridge `sc2-acme` in `default` project, `dns.domain=acme`, `ipv4=10.249.0.1/24`. ✅
  - Infra project `sc2-acme` + app project `sc2-acme-default`, both
    `features.networks=false` (share the bridge). ✅
  - App `default` profile: root disk (default pool) + eth0 (bridged→sc2-acme)
    + cloud-init `dev`/uid2000/sudo + e2e ssh key + sshd. ✅
  - **Native launch works:** `incus launch images:debian/13/cloud web
    --project sc2-acme-default` → `10.249.0.x`, `web.acme` resolves via bridge
    dnsmasq (`getent hosts web.acme` ✅), cloud-init applied `dev` + key + sshd. ✅
  - **Login needs a cloud-init image** (`.../cloud`); plain `images:debian/13`
    ships no cloud-init. e2e uses the `/cloud` variant.
  - Sidecar `sc2-acme` (infra project, **system-container** imported base
    `df67318483de`, static IP `10.249.0.3`): CoreDNS active (flat `acme` zone +
    fallthrough → dnsmasq `.1`); `tailscale up --advertise-routes=10.249.0.0/24`
    joined the tailnet as `100.76.153.28` (subnet router up). ✅
  - **FULL CORE E2E GREEN (first half):** from this CT,
    `web.acme` resolves via CoreDNS `.3` → `10.249.0.18`, and
    `ssh dev@web.acme` → `host=web user=dev uid=2000`. ✅

### Proven v2 tenant-create recipe (to codify in incusx executor)
1. `incus network create sc2-<t> --project default ipv4.address=<gw>/24 ipv4.nat=true ipv6.address=none dns.domain=<suffix>`
2. infra project `sc2-<t>`: `features.networks=false features.images=false features.profiles=true features.storage.volumes=true`
3. app project `sc2-<t>-default`: `features.networks=false features.images=true features.profiles=true features.storage.volumes=true`
4. app `default` profile: root(disk,default pool) + eth0(bridged→sc2-<t>) + `cloud-init.user-data` (dev/uid2000/sudo + ssh key + openssh-server + enable ssh)
5. `sidecar` profile (infra): root(disk,default) + eth0(bridged→sc2-<t>)
6. launch sidecar from a **system-container** base image (imported; raw OCI = app container, no systemd)
7. sidecar static IP `.3` in-container (base image does NOT DHCP eth0): `ip addr replace .3/24 + default via .1` + a systemd oneshot for reboot persistence
8. push CoreDNS Corefile+zone+upstream; mask systemd-resolved; `coredns.service` enable --now
9. `tailscaled` unmask+start; `tailscale up --advertise-routes=<cidr> --auth-key=<key> --hostname=sc2-<t> --accept-dns=false`

**Learnings:** tenant machines need a **cloud-init image** (`images:debian/13/cloud`); sidecar needs a **system-container** base; `features.images=false` on infra avoids a 750MB copy; subnet route needs Tailscale approval for a remote (non-big) device.

---


## Current Shape

- Product CLI: `sandcastle`, with `sc` installed as a symlink alias.
- Admin CLI: `sandcastle-admin`, with `sc-adm` available from local builds.
- Tenant boundary: one managed Incus project named `sc-<tenant>`.
- Project boundary: lightweight Sandcastle metadata namespace inside a tenant.
- Machine boundary: Incus container instance named `{project}-{machine}`.
- User machine refs: `machine` or `project/machine`.
- Admin machine refs: `tenant/machine` or `tenant/project/machine`.
- Local config keys: `tenant`, `project`, `remote`, and `admin_remote`.
- Restricted user access: Incus restricted certificate project grants to tenant
  Incus projects.

## Implemented

- Tenant lifecycle:
  - `sandcastle-admin tenant list`
  - `sandcastle-admin tenant create <tenant>`
  - `sandcastle-admin tenant status <tenant>`
  - `sandcastle-admin tenant delete <tenant> --yes [--purge]`
  - tenant metadata, private bridge, storage volumes, CA volume, CoreDNS
    sidecar, Tailscale sidecar, and tenant-local image aliases.
- Restricted users:
  - `sandcastle-admin user create <user>`
  - `sandcastle-admin user token <user>`
  - `sandcastle-admin tenant grant|revoke|users`
  - `sc remote add <name> <join-token> [--tenant <tenant>]`.
- Machine lifecycle:
  - `sandcastle list`
  - `sandcastle create [project/]machine [--detach]`
  - `sandcastle connect [project/]machine [-- command...]`
  - `sandcastle status [machine|tenant]`
  - `sandcastle start|stop|restart [project/]machine`
  - `sandcastle delete [project/]machine --yes`
  - `sandcastle port set [project/]machine <port>`.
- Project metadata namespaces:
  - `sandcastle project list`
  - `sandcastle project create <name>`
  - `sandcastle project status <name>`
  - `sandcastle project delete <name> --yes`.
- Tenant DNS and local resolver support:
  - `sandcastle dns apply|status <tenant>`
  - `sandcastle dns install|refresh|uninstall <tenant>`
  - local DNS forwarder service management.
- Tenant Tailscale sidecar:
  - `sandcastle tailscale up|status|down [tenant]`.
- Local trust and host overrides:
  - `sandcastle trust install|uninstall <tenant>`
  - `sandcastle host override create|list|delete`.
- Public routes:
  - `sandcastle route create|list|status|delete`
  - route broker mTLS authorization based on restricted certificate tenant
    grants.
- Images:
  - `sandcastle-admin image build base|ai`
  - `sandcastle-admin image import base|ai <source-ref>`
  - `sandcastle-admin image sync <image-ref>`.
- E2E harness:
  - `scripts/e2e.sh unit`
  - `scripts/e2e.sh gated`
  - `scripts/e2e.sh local`
  - destructive `incus`, `restricted`, `tailscale`, `images`, `route-broker`,
    `public-routes`, `local-vm`, and `cleanup` tiers.
  - `scripts/e2e-local-vm.sh` host-side disposable VM harness.

## Recent Verified Checkpoints

- `sandcastle-admin version --help` now uses admin-specific wording while
  preserving the existing version output and JSON payload shape. Verified with
  `go test ./internal/cli`, `go test ./...`, `scripts/e2e.sh gated`, and
  `scripts/e2e.sh local` run `e2e-20260521-161041-228322`.
- Documentation audit corrected stale admin command descriptions in
  `CONTEXT.md` and `docs/sandcastle-v1-spec.md`: restricted user management is
  `user create`, `user token`, and tenant access commands, and
  `sandcastle-admin status` requires a machine reference. Verified with
  `go test ./...`, `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-160859-226933`.
- `go test ./internal/cli ./internal/config`, `go test ./...`,
  `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-160626-224980` passed on 2026-05-21 after implementing the
  documented `sandcastle config unset <key>` command and updating docs.
- `go test ./internal/cli ./internal/usertrust`, `go test ./...`,
  `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-160343-223640` passed on 2026-05-21 after changing
  `sandcastle-admin user token` help from planning language to create
  language.
- `go test ./internal/cli ./internal/route ./internal/hostoverride`,
  `go test ./...`, `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-160128-222392` passed on 2026-05-21 after changing
  `sandcastle route create` and `sandcastle host override create` help from
  planning language to create language.
- `go test ./internal/route ./internal/hostoverride ./internal/routebroker
  ./internal/incusx ./internal/cli ./internal/e2e`, `go test ./...`,
  `scripts/e2e.sh gated`, `scripts/e2e.sh local` run
  `e2e-20260521-153741-192726`, host `incus` run
  `e2e-20260521-154044-195768`, route-broker runs
  `e2e-20260521-154507-208246` and `e2e-20260521-155343-213621`, and
  `scripts/e2e-local-vm.sh` run `e2e-local-vm-20260521-154755` passed on
  2026-05-21 after aligning route and host override delete internals, help,
  docs, and e2e probe labels with the public `delete` commands.
- `go test ./internal/machine ./internal/incusx ./internal/cli ./internal/e2e`,
  `go test ./...`, `scripts/e2e.sh gated`, `scripts/e2e.sh local` run
  `e2e-20260521-153150-187628`, and targeted local Incus
  `TestMachineLifecycleE2E` run `e2e-20260521-153200-000000000` passed on
  2026-05-21 after aligning the machine delete lifecycle action with the
  public `delete` command.
- `go test ./internal/tailscale ./internal/tenant ./internal/e2e
  ./internal/incusx`, `go test ./...`, `scripts/e2e.sh gated`, and
  `scripts/e2e.sh local` run `e2e-20260521-152606-185485` passed on
  2026-05-21 after renaming private tenant-summary helper variables away from
  project wording.
- `go test ./internal/cli ./internal/e2e`, `go test ./...`,
  `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-152337-183929` passed on 2026-05-21 after renaming the private
  admin tenant CLI constructors and e2e test names away from `AdminProject`.
- `go test ./...`, `scripts/e2e.sh gated`, `scripts/e2e.sh local` run
  `e2e-20260521-150755-165557`, host `incus` run
  `e2e-20260521-150808-165654`, route-broker run
  `e2e-20260521-151232-178254`, and `scripts/e2e-local-vm.sh` run
  `e2e-local-vm-20260521-151522` passed on 2026-05-21 after renaming the
  tenant Incus project prefix config to `SANDCASTLE_INCUS_PROJECT_PREFIX`.
- `go test ./...`, `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-150242-162965` passed on 2026-05-21 after changing
  `sandcastle tailscale up|status|down` to default to the current tenant.
  The destructive Tailscale tier was not run because
  `SANDCASTLE_E2E_TAILSCALE_AUTHKEY` is not present in this environment.
- `go test ./...`, `scripts/e2e.sh gated`, `scripts/e2e.sh local` run
  `e2e-20260521-145647-159278`, and targeted local Incus
  `TestCLICreateDetachE2E` run `e2e-20260521-145657-000000000` passed on
  2026-05-21 after renaming the internal machine inspect path to status.
- `go test ./...`, `scripts/e2e.sh gated`, `scripts/e2e.sh local` run
  `e2e-20260521-145351-156029`, and targeted local Incus
  `TestHostOverrideE2E` passed on 2026-05-21 after changing host override list
  to `sandcastle host override list [tenant]`.
- `go test ./...`, `scripts/e2e.sh gated`, `scripts/e2e.sh local` run
  `e2e-20260521-143922-137741`, host `incus` run
  `e2e-20260521-143930-137902`, route-broker run
  `e2e-20260521-144404-150446`, and `scripts/e2e-local-vm.sh` run
  `e2e-local-vm-20260521-144651` passed on 2026-05-21 after removing the old
  project-domain validator surface.
- `go test ./internal/cli ./internal/usertrust` and `go test ./...` passed on
  2026-05-21 after removing the duplicate user-first admin grant command.
- `scripts/e2e.sh gated` and `scripts/e2e.sh local` passed on 2026-05-21
  after the admin grant command cleanup; local run id
  `e2e-20260521-143541-135669`.
- `go test ./...` passed on 2026-05-21 after removing obsolete e2e domain
  suffix plumbing.
- `scripts/e2e.sh gated` passed on 2026-05-21 after removing obsolete e2e
  domain suffix plumbing.
- `scripts/e2e.sh local` passed on 2026-05-21 with run id
  `e2e-20260521-142654-132797`.
- Host destructive `incus` tier passed on 2026-05-21 with local image aliases,
  run id `e2e-20260521-135924-105773`.
- Dedicated route-broker tier passed on 2026-05-21 with run id
  `e2e-20260521-142123-127636`.
- `scripts/e2e-local-vm.sh` passed end-to-end on 2026-05-21 with run id
  `e2e-local-vm-20260521-142730`.

## Remaining External Gates

The following tiers require environment or credentials that are not generally
available in a plain local checkout:

- `restricted`: non-local HTTPS Incus remote plus disposable image sources.
- `tailscale`: Tailscale auth key and route approval policy.
- `images`: image build tooling and pinned AI CLI versions.
- `public-routes`: public domain, infrastructure DNS proof target, route broker
  socket, image sources, and Let's Encrypt contact email.

The active goal remains open until those gates can be exercised in an
environment that provides their prerequisites.
