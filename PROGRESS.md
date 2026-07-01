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
| 1 | v2 naming helpers (`sc2-<tenant>`, `sc2-<tenant>-<project>`, bridge) | ✅ |
| 2 | `sc-adm tenant create-v2`: infra project + sidecar + bridge + `default` project + profile | ✅ code validated on big |
| 3 | Project scaffolding `CreateProjectV2` (= broker logic) + `sc-adm project create-v2` | ✅ code validated on big |
| 3b | Restricted trust-token minting in tenant-create (`incus remote add --token`) | ✅ token+scope verified on big |
| 4 | Sandcastle Broker + `sc project create-v2` client (tenant self-service) | ✅ validated on big |
| 5 | Flat DNS `<machine>.<suffix>` wiring (Corefile + dnsmasq) | ✅ (in executor) |
| 6 | Per-tenant CA install on `sc connect` | ⬜ (CA generated; install deferred) |
| 7 | Deploy to `big` + run acceptance script until green | ✅ `scripts/e2e-v2.sh` GREEN |

### CORE E2E GREEN via code (2026-07-01)
`sc-adm tenant create-v2 demo` + `sc-adm project create-v2 demo backend`, then native
`incus launch images:debian/13/cloud` into each project:
- `ssh dev@api.demo` (sc2-demo-default) → `host=api uid=2000` ✅
- `ssh dev@api2.demo` (sc2-demo-backend) → `host=api2 uid=2000` ✅
Both resolved by name via the **single** sidecar CoreDNS `10.250.0.3` across two
projects. Remaining to match the literal self-service script: restricted trust
token (tenant's own cert) + the tenant-facing broker for `sc project create`.

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

### Phase 2 validated (2026-07-01)
`sc-adm tenant create-v2 demo --cidr-pool 10.250.0.0/16 --sidecar-image df67318483de`
ran the Go executor end-to-end on big (exit 0): bridge + infra/app projects +
cloud-init profile + system-container sidecar + CoreDNS. Then native
`incus launch images:debian/13/cloud api --project sc2-demo-default` → CoreDNS
`api.demo` → `10.250.0.252` → `ssh dev@api.demo` = `host=api user=dev uid=2000`. ✅
Note: SDK needs a single-address remote — added `bigv2` (big has a multi-addr
failover list the Go SDK can't parse). Sidecar image must be a system container
(passed imported base `df67318483de`); a proper `sc-adm image import` base is the
production source.

### Repeatable e2e GREEN (2026-07-01) — `scripts/e2e-v2.sh`
```
SANDCASTLE_REMOTE=bigv2 V2_SIDECAR_IMAGE=df67318483de ./scripts/e2e-v2.sh
PASS: tenant created (sidecar DNS 10.252.0.3)
PASS: project backend created
PASS: CoreDNS resolves web.e2ev2 -> 10.252.0.195
PASS: ssh dev@web.e2ev2 -> OK:web:2000
PASS: CoreDNS resolves api.e2ev2 -> 10.252.0.134
PASS: ssh dev@api.e2ev2 -> OK:api:2000
v2 e2e: GREEN   (then clean teardown)
```
The whole flow runs through the codified `sc-adm tenant create-v2` +
`sc-adm project create-v2` + native `incus launch`, one sidecar serving two
projects. `sc-adm project create-v2` IS the Sandcastle Broker's scaffolding;
the tenant-facing broker (`sc project create` over big:9443 + cert extension)
is the remaining self-service delivery wrapper (deferred with OAuth).

### Sandcastle Broker self-service VALIDATED on big (2026-07-01)
Ran `sc-adm project broker-serve` locally (admin creds via bigv2); the tenant
called `sc project create-v2 backend --broker https://127.0.0.1:9443 --cert … --key …`
with their **own restricted cert**. Broker mapped the cert → tenant `bkr`,
created `sc2-bkr-backend`, and **extended the tenant cert** to
`[sc2-bkr-default, sc2-bkr-backend]` (verified via /1.0/certificates). Full
decision-B self-service loop works. Remaining for production: package the broker
as an infra appliance on `big:9443` (proxy device + admin socket mount) — the
serve command + handler are done; only deployment/wiring remains.

### Broker-backend refactor (in progress)
Moving admin ops behind the broker's :9443 web API (two-plane model: bootstrap =
the only direct-incus op; everything else via the broker).
- ✅ Admin config isolated to `~/.config/incus-admin` (plain `incus` is clean);
  default `~/.config/incus` purged; `binc`/`sc-adm` aliases in `~/.bashrc`.
- ✅ `sc connect-v2 <tenant>` regenerates a tenant's local incus config
  (enroll + per-project cert-pinned remotes via big.thieso2.dev:8443).
- ✅ Broker **admin plane**: AdminAuthorizer (trusted unrestricted cert) +
  TenantProvisionerAdapter; `sc-adm tenant create-v2 --broker` routes through
  :9443 (validated: created brtest via the broker, sidecar RUNNING).
- ✅ `sc-adm bootstrap` deploys the broker as an appliance on the host
  (privileged, host admin unix socket mounted, `:9443` proxy, pushed binary +
  TLS + systemd unit) — the broker uses the local socket with full rights.
  VALIDATED on big: bootstrap → `tenant create-v2 --broker big.thieso2.dev:9443`
  provisioned a tenant through the appliance. The broker `sc2-broker` is now
  LIVE on big.
- ⬜ Remaining: admin endpoints for tenant list/delete + admin project create;
  flip `sc-adm` defaults to broker mode; fold DNS-resolver + CA-trust into
  `sc connect`.

---

# 2026-07-01 — sc2 web API + full e2e (`docs/e2e-sc2.md`)

## Deployed on `big`
- **Fat binary**: one `bin/sandcastle`; `sc`/`sc-adm`/`sandcastle-admin` are symlinks (argv0 dispatch + `sc admin …`).
- **`sc-edge`** (project `infrastructure`) owns host `:80/:443`; `sc-caddy` retired/stopped.
- **`sc2-auth-app`** deployed on stock Debian, fronted at `https://sc2.thieso2.dev` (LE cert, no client certs, GitHub OAuth). New `sc admin auth-app deploy`.
- **`sc2-broker`** redeployed with current binary.

## Code changes
- `installV2SidecarPackages`: CoreDNS binary v1.14.3 + Tailscale via apt on a **stock** Debian sidecar (no `sandcastle/base`).
- `v2TailscaleUp`: readiness wait + retry + `tailscale ip -4` success gate (fixes the up race).
- `DefaultApplianceImage=images:debian/13`; deploy no longer needs `--base-image`/`--default-unix-user`.

## e2e status (`docs/e2e-sc2.md`) — **Phases 0–8 GREEN (validated live)**
- create-v2 → connect-v2 → `incus remote switch` → profile (ssh + cloud-init) ✅
- fresh VM `e2eweb` → `sc-edge` vhost `e2eweb.scdev.thieso2.dev` → **valid LE cert** ✅
- CoreDNS resolves machine names at the sidecar **tailscale IP** ✅

## Remaining for full green
1. Auth-app → broker/v2 **login provisioning** (currently v1 `EnsurePersonalTenant`, fails on stale image).
2. Machine **DNS auto-registration** on create.
3. Per-tenant **tenant-CA** cert path + `sc trust install` (Linux/macOS).
4. **Split-DNS** over tailscale.
5. Deploy-command multi-address remote fix.

## Problems encountered & fixes
- **OCI base has no systemd** → appliances wouldn't run services. Fix: CONTAINER-type systemd image (`images:debian/13` / `d31c34fadc08`).
- **systemd gives Caddy no `$HOME`** → it ignored copied certs and tried to re-issue. Fix: pin `storage file_system /root/.local/share/caddy` in the sc-edge Caddyfile.
- **`sc-adm` wrapper mangled the multi-address `big` remote** → drove ops via `incus` + broker `exec` directly. (Fix pending.)
- **`text file busy`** pushing the binary into a running appliance → stop service, push, start.
- **default project has its own image store** → copy the stock image in before launching machines.
- **CoreDNS/Tailscale not in Debian apt** → CoreDNS binary download; Tailscale official apt repo.
- **Tailscale `up` race** on a freshly-installed sidecar → readiness wait + retry (code fix above).

## Update — unattended v2 login GREEN
`sc login https://sc2.thieso2.dev --debug-approve` now works end-to-end:
provision (v2 default project + sidecar, SSH key **baked into the profile at create**,
CIDR via OccupiedCIDRs) → approve → client cert → **enrolled remote** `sandcastle-logintest`
(works: `incus list` clean) → local incus config written from scratch.
Code: auth-app `V2Create` closure (`SANDCASTLE_AUTH_PROVISION_V2=1`), `ensurePersonalTenantV2`
(occupied-CIDR aware, bakes SSH key), v1 SSH-key reconcile/set made no-op for v2, `User.SSHPublicKey`.
- ⚠️ Tailscale up still flaky on **reused/re-keyed** sidecars (readiness fix helps; fresh sidecars connect).

## NEXT: remove all v1 code (user directive, repeated)
Going v2-only. v1 surface to delete: `internal/tenant` v1 create/plan (`tenant_create.go`,
`create_plan.go`), `internal/infra` (v1 sc-caddy/route-broker/auth-app deploy), v1 `Provisioner`
path, v1 machine/route code, v1 CLI (`tenant create`, `infra ...`), v1 e2e tests. Large refactor —
keep the build green as it shrinks.

## Update — CT+VM launch → DNS + SSH GREEN
Tenant `incus launch` of **both** a CT (`images:debian/13/cloud`) and a VM (`…/cloud --vm`):
cloud-init applies the profile (user `dev` + SSH key + openssh) → **SSH works into both**
(`dev@ct1`, `dev@vm1`), CoreDNS resolves both names. sc-dev reaches the tenant bridge via host routing.
- **Key gotcha:** tenant machines MUST use the `/cloud` image variant — the plain image has no
  cloud-init, so the profile (dev user/ssh key) never applies and sshd is absent. (Appliances/sidecar
  keep the plain systemd image since they're configured via `incus exec`, not cloud-init.)

## v1 removal — progress
- ✅ **infra chunk removed** (commit): internal/infra + incusx/infrastructure.go + `sc-adm infra` CLI + v1 infra tests + v1 e2e test files (shared e2e helpers extracted). Build/vet/tests green.
- **Next:** auth-app provisioner v2-only (drop v1 EnsurePersonalTenant), v1 tenant create, routes/routebroker, v1 machine create, and their tests.
- Added protocol MD Phase 7c (🚧): shared `$HOME` + `/workspace` per project (v2 profile currently mounts only root disk — feature to build).

## v1 removal — progress (cont.)
- ✅ **provisioner chunk removed** (commit 5905b44): auth-app is v2-only (v1 EnsurePersonalTenant body + helpers + fields + interfaces + provision_test.go gone).
- ✅ **e2e re-run GREEN after removals**: fat binary dispatches; `sc2.thieso2.dev/healthz` 200; CoreDNS resolves ct1/vm1; SSH into CT+VM works. (Per the "run e2e on feature finish/removal" rule.)
- **Entanglement found:** `internal/routebroker` is partly shared — `routebroker.Principal` + `incusx.RouteBrokerTrustMapper` are used by the **v2** projectbroker. So the routes chunk must keep those and remove only the v1 route mutation (RouteManager, `internal/route`, `cli/route.go`, `incusx/route.go`, route-broker serve).
- **Remaining v1 chunks:** routes (partial), v1 tenant create (`tenant.PlanCreate`, `incusx.CreateTenant`, CLI), v1 machine create, + their tests.

## v1 removal — progress (cont. 2)
- ✅ **v1 `tenant create` CLI + tests removed** (create-v2 is the only tenant create). e2e smoke re-run green.
- Dead-but-present v1 machinery to remove in deeper increments: `incusx.CreateTenant`, `tenant.PlanCreate` (entangled with shared `create_plan.CreateRequest` / `tenant_create.go` helpers used by v2).
