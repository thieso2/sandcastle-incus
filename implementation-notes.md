# Implementation Notes

## Running Notes

- Started implementation from the committed domain docs (`CONTEXT.md`,
  `docs/sandcastle-v1-spec.md`, ADR-0001). The existing Go code is still built
  around the superseded owner/project/sandbox model.
- First implementation slice is the foundational naming and metadata vocabulary,
  because CLI parsing, Incus resource names, DNS, routes, and tests all depend
  on those types.
- `internal/naming` now owns the new reference grammar:
  `tenant/project`, `tenant/machine`, `tenant/project/machine`, user
  `machine`, and user `project/machine`. Incus tenant project names are
  `sc-{tenant}` and machine instance names are `{project}-{machine}`.
- Local/admin config moved from `Owner`/`SANDCASTLE_OWNER` to
  `Tenant`/`SANDCASTLE_TENANT`. Local config also has a `Project` field for the
  current-project behavior.
- `internal/meta` now serializes `tenant`, `machine`, and route target tenant
  fields. I moved the previous per-project SSH public key to tenant metadata
  because the new spec has tenant-scoped infrastructure/storage and projects
  have no settings.
- Renamed the old Incus-project lifecycle package from `internal/project` to
  `internal/tenant`. Its focused tests now cover tenant creation/deletion/list
  and status. `PlanCreate` takes only a tenant name, derives `sc-{tenant}`,
  initializes the `default` project in tenant metadata, and renders DNS for the
  tenant suffix.
- Renamed the runtime package from `internal/sandbox` to `internal/machine`.
  Machine planning now uses current tenant/current project resolution, Incus
  instance names of `{project}-{machine}`, private hostnames of
  `{machine}.{project}.{tenant}`, and tenant storage defaults of
  `{project}/{machine}`.
- Local DNS, local trust, Tailscale, and restricted-user grants now resolve
  tenant references rather than owner/project references. Local DNS writes a new
  `tenants:` state schema with `dnsSuffix` entries; there is intentionally no
  migration or compatibility alias for the old `projects:` local state.
- Restricted-user grants still produce Incus restricted certificate `Projects`
  because that is the Incus API surface, but command input is now tenant refs
  and maps to `sc-{tenant}`.
- Public route planning and host overrides now target machines, not sandboxes.
  Canonical references are `tenant/project/machine`; user-side calls may resolve
  `machine` or `project/machine` through `SANDCASTLE_TENANT` and
  `SANDCASTLE_PROJECT`. Route metadata writes `targetTenant`,
  `targetProject`, and `targetMachine`.
- `sandcastle route status <hostname>` is implemented as a filtered read over
  the existing route listing API rather than a new broker endpoint. That keeps
  the current mTLS broker protocol smaller while still exposing the v1 command
  shape; it can become a dedicated metadata lookup later if route lists become
  too large.
- Route broker authorization is now tenant-grant based. The mTLS principal has
  a human user string for audit (`CreatedBy`), but route create/delete
  authorization checks whether the certificate grants the target Incus tenant
  project (`sc-{tenant}`), not whether the user name matches the tenant.
- The Incus adapter layer moved from project/sandbox method semantics to
  tenant/machine semantics. The remaining old public-surface names are Incus API
  terms such as project, or historical notes explicitly describing the
  superseded model.
- User CLI command names now expose the new no-alias surface for the main
  machine lifecycle: `list`, `create`, `connect`, and `delete`. I removed the
  old `ls`, `add`, `enter`, and `rm` registrations from the root command rather
  than keeping compatibility aliases. `status <machine>` now uses the machine
  status planner/result directly; the old `inspect` command is no longer
  registered.
- `sandcastle list` now lists machines in the current tenant instead of listing
  tenant summaries. It scopes to `SANDCASTLE_PROJECT` unless `--all-projects/-a`
  is supplied or no current project is configured. The `--include-unmanaged/-u`
  flag shows non-Sandcastle Incus instances for tenant-wide lists, while the
  unmanaged count is always printed even when unmanaged rows are hidden.
- The admin tenant lifecycle group is now `sandcastle-admin tenant ...` instead
  of `project ...`. The admin machine lifecycle is now top-level:
  `sandcastle-admin list tenant[/project]`,
  `sandcastle-admin create/connect/status/delete tenant[/project]/machine`.
  These commands translate admin refs into the same tenant-scoped machine
  planners used by the user CLI so the admin and user paths do not diverge.
- Admin tenant access is now exposed in tenant-first command shape:
  `sandcastle-admin tenant grant <tenant> <user>`,
  `sandcastle-admin tenant revoke <tenant> <user>`, and
  `sandcastle-admin tenant users <tenant>`. These commands still mutate Incus
  restricted certificate project grants internally, because Incus calls the
  access boundary a project. The duplicate user-first
  `sandcastle-admin user grant <user> <tenant>` surface has been removed so
  tenant access has one canonical admin shape.
- Bare machine resolution for existing-machine operations now searches across
  the current tenant only when no current project is configured. If exactly one
  project contains the machine name, `connect`/`status`/`delete` use it; if
  multiple projects match, the CLI returns an ambiguity error and requires an
  explicit `project/machine`. When `SANDCASTLE_PROJECT` is set, bare names stay
  scoped to that project.
- User project management now lives under `sandcastle project
  list/create/status/delete`. Projects remain lightweight tenant metadata only.
  Project status intentionally reports tenant, project, and machine count rather
  than infrastructure checks, because v1 projects do not own Incus networks,
  DNS, storage, or CA state. Delete requires `--yes`, rejects `default`, and
  checks the tenant's machine metadata to ensure the project is empty. There is
  no `createdBy` value yet for user-created projects because the current local
  config identifies the tenant but not the human principal.
- I cleaned up several command help strings and e2e fixture references that
  still said owner/project/sandbox. The e2e tests that create machines now use
  the v1 instance and DNS shape (`default-{machine}` or `{project}-{machine}`,
  `{machine}.default.{tenant}` / `{machine}.{project}.{tenant}`).
- Updated the user-facing usage docs, quickstart snippets, README overview, and
  `.env.default` examples away from `SANDCASTLE_OWNER`, `add`/`enter`/`rm`/
  `inspect`/`ls`, and sandbox wording toward tenant/project/machine command
  shape. Later docs passes replaced the deeper implementation and e2e planning
  docs with the current tenant/project/machine shape.
- Disposable VM e2e with Debian 13's Incus 6.0.4 exposed that tenant storage
  pool creation cannot pass a derived `source` path for `dir` pools: Incus does
  not create that nested path before volume file upload. The creator now omits
  `source` for `dir` pools and lets Incus manage the pool path; non-dir pools
  still derive a per-tenant source from the admin pool.
- E2E fixtures and diagnostics now use tenant references and tenant local-DNS
  state. Safe e2e tiers pass after the latest CLI-shape work:
  `go test ./...`, `scripts/e2e.sh unit`, `scripts/e2e.sh gated`, and
  `scripts/e2e.sh local`. Full destructive tiers still need more environment
  setup than the host currently provides: image-dependent tests require
  `SANDCASTLE_E2E_BASE_IMAGE_SOURCE` and `SANDCASTLE_E2E_AI_IMAGE_SOURCE`, the
  restricted tier requires a non-local HTTPS Incus remote, and Tailscale/public
  route tiers require external credentials or DNS inputs.
- Disposable VM e2e on Debian 13/Incus 6.0.4 also exposed that SDK image copy
  from the default project into a tenant project fails over a local Unix socket
  with `The source server isn't listening on the network`. Tenant image
  propagation now uses Incus relay mode for project image copies. This is less
  efficient than pull mode for remote-to-remote copies, but it works for the
  local-admin path and keeps behavior deterministic across Unix-socket and HTTPS
  remotes.
- The same Incus 6.0.4 VM does not support the storage volume file API used by
  newer Incus clients for custom volumes (`CreateStorageVolumeFile`/
  `GetStorageVolumeFile` return `not found`). For local dir-backed storage
  volumes, the Incus adapter now falls back to reading/writing the project
  volume path under `/var/lib/incus/storage-pools/<pool>/custom/<project>_<vol>`
  after the SDK returns 404. HTTPS remotes and newer servers still use the SDK
  path first.
- Tenant private bridge names can no longer be simple 15-character truncations
  of long Incus project names. Linux bridge names have a 15-character limit, but
  truncation made e2e tenants like `sc-tenant-e2e-local...` collide on
  `sc-tenant-e2e-l`, causing sidecar IP/subnet validation failures. Long names
  now use a stable `sc-` plus 12-hex hash bridge name.
- I created disposable Incus VMs twice and seeded nested Incus images to keep
  exercising `scripts/e2e.sh local-vm`. Docker-based image building filled the
  host's 9.6 GB root filesystem, so I switched to a lean Incus-native image seed
  by copying `images:debian/13`, installing only sidecar runtime packages, and
  publishing `sandcastle/base:latest`/`sandcastle/ai:latest`. Even with that,
  nested tenant creation and image copies filled the host root filesystem and
  forced the VM into Incus `ERROR`; both disposable VMs were deleted to restore
  space. Current verified host gates after these fixes: `go test ./...`,
  `scripts/e2e.sh unit`, `scripts/e2e.sh gated`, and `scripts/e2e.sh local`.
- Host Incus e2e needed the same image shape as the Debian base Dockerfile.
  Using a quick Ubuntu cloud image seed let Caddy/CoreDNS install, but it already
  had UID/GID 1000 allocated and caused tenant user bootstrap to silently miss
  the expected Linux user in one detached create path. I replaced the host seed
  with an Incus-native Debian 13 container image customized with the base runtime
  packages and `sandcastle-bootstrap`, then pointed
  `sandcastle/base:latest`/`sandcastle/ai:latest` at that Debian image.
- Some base images expose `/etc/resolv.conf` as a symlink whose target does not
  exist when Incus' file API tries to overwrite it. Machine creation now falls
  back to an in-instance shell write for machine resolver configuration when
  `CreateInstanceFile("/etc/resolv.conf")` returns 404.
- With the Debian host seed, the destructive `incus` tier passed the CLI
  create/detach, default create/connect, connect, host override, image sync,
  local trust, tenant purge, tenant listing, and machine lifecycle cases.
  Remaining host `incus` failures from run `e2e-incus-20260521-1051`:
  infrastructure route broker mTLS probe got connection refused, and tenant DNS
  lookup from one machine timed out.
- Tenant DNS timeout was caused by Debian Incus images running
  `systemd-resolved`, which binds port 53 on loopback and prevents CoreDNS from
  binding `:53`. DNS sidecar CoreDNS restart now stops and masks
  `systemd-resolved` before launching CoreDNS. Verified with
  `TestTenantDNSE2E` on host Incus run `e2e-dns-fix-20260521-1100`.
- Infrastructure now uploads and runs `sandcastle-admin` for the route broker
  service (`sandcastle-admin route-broker serve`). `SANDCASTLE_ADMIN_BIN` is the
  preferred binary source, with `SANDCASTLE_BIN` as a local fallback for older
  setups. The e2e infrastructure tests build `./cmd/sandcastle-admin` for the
  target architecture before creating the sidecars.
- The route broker sidecar uses the mounted Incus Unix socket directly when it
  is serving inside infrastructure. Socket-mounted broker instances are marked
  privileged because an unprivileged container root cannot open the host
  `/var/lib/incus/unix.socket`; Caddy remains unprivileged and does not receive
  that mount.
- Tenant listing skips managed projects whose Sandcastle metadata kind is not
  `tenant`, so infrastructure projects (`kind=infrastructure`) no longer make
  `tenant list`/e2e diagnostics fail while parsing tenant summaries.
- Route broker mutation e2e now uses canonical `tenant/default/machine` target
  references. The host route-broker tier passed as
  `e2e-route-broker-20260521-1212`, including unowned-target 403, DNS-proof 400,
  add/list/remove 201/200, and route cleanup checks.
- The broader host `incus` tier passed again as `e2e-incus-20260521-1220` after
  the route broker service and route ingress fixes. The broker mutation path now
  uses the default host Incus socket mount, and the dedicated `route-broker` tier
  covers that socket-mounted path.
- Added `scripts/e2e-local-vm.sh` as a reusable host-side harness for the
  VM-only local mutation tier. It launches a disposable local Incus VM, installs
  Go, mise, and nested Incus, copies the checkout, seeds nested image aliases
  from host `sandcastle/base:latest` and `sandcastle/ai:latest`, starts root's
  systemd user service manager for the local DNS service test, and runs
  `scripts/e2e.sh local-vm` inside the VM. This replaces prior ad hoc VM setup
  attempts and gives the remaining disk-constrained verification a repeatable
  entry point.
- Debian 13/Incus 6.0.4 also returned success for custom-volume file uploads on
  local dir-backed tenant pools while leaving the CA files empty. When running
  against local dir-backed volumes as root, the adapter now writes directly to
  `/var/lib/incus/storage-pools/<pool>/custom/<project>_<vol>` instead of using
  the broken upload path. The host-side VM harness passed end-to-end as
  `e2e-local-vm-20260521-122306`, covering host overrides, local DNS service
  install/reload/uninstall, local trust, and platform trust.
- The same custom-volume subdirectory fix initially regressed host e2e runs
  executed as a non-root Incus admin user: the SDK returned 404 for volume-file
  directory creation, but the process could not write `/var/lib/incus` directly.
  Machine creation now falls back to a short-lived storage helper container in
  the tenant project. The helper mounts the top-level `sc-home` and
  `sc-workspace` custom volumes, creates the requested subdirectories with UID
  and GID 1000, then is deleted before the real machine is created. This keeps
  non-root local Incus, root VM, and newer remote Incus paths working. Verified
  host tiers: `incus` passed as `e2e-20260521-123440-18835`; `route-broker`
  passed as `e2e-20260521-123909-31423`; the host-side VM harness passed again
  as `e2e-local-vm-20260521-124224`.
- Removed stale user-as-tenant bootstrap output from `sandcastle-admin user
  create/token`. The human output now tells developers to run `sc remote add
  ...` and set the default tenant explicitly after access is granted, while
  `sc remote add --tenant` remains the one-step handoff path when the tenant is
  already known.
- Replaced the old implementation and e2e planning docs with the current
  tenant/project/machine shape. The docs now describe tenant-scoped DNS,
  Tailscale, local trust, route broker authorization by restricted certificate
  grants, the disposable VM harness, and the current command names instead of
  owner/project/sandbox milestones.
- Renamed the route broker mTLS principal identity from `Owner` to `User`.
  Authorization was already grant-based; this removes the misleading implication
  that the user name must own or match the target tenant. Route metadata keeps
  `CreatedBy` as the audit field.
- Renamed the remaining private Go `sandbox` vocabulary to `machine` across the
  CLI, Incus adapter, cert, Caddy, route, and e2e helpers. The behavioral API
  was already machine-oriented; this pass removes stale type/function/file names
  such as `SandboxCreator`, `RenderSandbox`, and `sandbox_lifecycle.go`.
- Cleaned up more private e2e/test vocabulary after the public docs pass:
  restricted-user, Tailscale, route-broker, local DNS, and cleanup fixtures now
  distinguish human users from tenants instead of using `owner` as a generic
  variable name. This was behavior-preserving, but it makes the grant-based
  route broker model harder to misread.
- Renamed the private machine connection path from `enter`/`add` vocabulary to
  `connect`/`create`: `PlanConnect`, `ConnectPlan`, `MachineConnector`, and the
  e2e runner's CLI test names now match the public command surface. The executor
  still delegates to Incus `ExecInstance`; only Sandcastle's internal naming
  changed.
- Renamed the tenant lifecycle Incus adapters from project-facing store,
  creator, deleter, and SSH-key updater names to tenant-facing names. Literal
  Incus API methods and struct fields still use project terminology where Incus
  itself exposes projects, but Sandcastle-facing dependencies now read as
  tenant stores, creators, and resources.
- Verified the tenant adapter rename against full host Incus, dedicated route
  broker, and disposable local VM tiers on 2026-05-21:
  `e2e-20260521-134205-85388`, `e2e-20260521-134629-98093`, and
  `e2e-local-vm-20260521-134918`.
- Renamed private imports of `internal/tenant` from the old `project` alias to
  `tenant`, and renamed the tenant lifecycle/DNS/listing e2e tests plus the
  `scripts/e2e.sh incus` regex to match. Incus SDK method names still say
  `Project` where they call Incus projects directly.
- Verified the tenant import/test rename against safe and destructive tiers on
  2026-05-21: `scripts/e2e.sh local` run `e2e-20260521-135906-105611`, host
  `incus` run `e2e-20260521-135924-105773`, route-broker run
  `e2e-20260521-140348-118342`, and disposable VM run
  `e2e-local-vm-20260521-140634`.
- Local trust help, output, and adapter errors now say tenant CA instead of
  project CA. The command was already tenant-scoped; this only fixes stale
  wording and docs examples that omitted the tenant argument during cleanup.
- Refreshed the docs front doors after the tenant/machine rename pass. README
  now links the usage guide and admin/developer quickstart; the usage guide's
  tenant sections no longer call tenant delete/grant operations "project"
  operations; the quickstart uses the current private DNS shape
  `machine.project.tenant` and includes the tenant argument for Tailscale
  cleanup; the e2e plan examples now match the Debian 13 image aliases used by
  the runner examples.
- Renamed the route creation internals from `Add` vocabulary to `Create`
  vocabulary across the route planner, route broker client/server, Incus route
  manager, CLI wiring, and tests. The HTTP broker endpoint remains `POST
  /routes`; only Sandcastle's internal architecture now matches the public
  `sandcastle route create` command. I left host override internals as `Add`
  because that package is modeling local hosts-file entry addition rather than
  a public command verb.
- Verified that route create rename with `go test ./...`, `scripts/e2e.sh
  gated`, `scripts/e2e.sh local` run `e2e-20260521-142111-127490`, and the
  dedicated route broker mutation tier run `e2e-20260521-142123-127636`.
- Removed the obsolete `SANDCASTLE_E2E_DOMAIN_SUFFIX` harness/workflow setting.
  Tenant DNS suffixes are now always derived from tenant names, so keeping a
  separate e2e domain suffix suggested the superseded project-domain model.
- Verified the e2e harness cleanup with `go test ./...`, `scripts/e2e.sh
  gated`, `scripts/e2e.sh local` run `e2e-20260521-142654-132797`, and
  `scripts/e2e-local-vm.sh` run `e2e-local-vm-20260521-142730`.
- Removed the remaining public `ValidateProjectDomain` helper and moved domain
  validation directly into `ValidateTenantDNSSuffix`. Tenant DNS suffixes are
  intentionally single-label tenant-derived names, not configurable project
  domains. I also refreshed the low-level Caddy, certificate, and local DNS
  fixtures away from `project-tld` examples so test vocabulary matches the
  tenant DNS model.
- Verified the tenant DNS validator cleanup with focused domain/local DNS/cert
  tests, `go test ./...`, `scripts/e2e.sh gated`, `scripts/e2e.sh local` run
  `e2e-20260521-143922-137741`, host `incus` run
  `e2e-20260521-143930-137902`, route-broker run
  `e2e-20260521-144404-150446`, and disposable VM run
  `e2e-local-vm-20260521-144651`.
- Changed `sandcastle host override list` from the misleading required
  `project` argument to `list [tenant]`. Host overrides are tenant-level
  machine metadata, so the command now defaults to the current tenant while
  still allowing an explicit tenant for admin-style inspection.
- Verified the host override list shape with `go test ./internal/cli
  ./internal/hostoverride`, `go test ./...`, `scripts/e2e.sh gated`,
  `scripts/e2e.sh local` run `e2e-20260521-145351-156029`, and targeted local
  Incus `TestHostOverrideE2E`.
- Renamed the remaining internal machine inspect planner/formatter/test
  vocabulary to status vocabulary. This is behavior-preserving, but it removes
  the last old command-shape name from the machine status path.
- Verified the machine status rename with `go test ./internal/machine
  ./internal/cli ./internal/e2e`, `go test ./...`, `scripts/e2e.sh gated`,
  `scripts/e2e.sh local` run `e2e-20260521-145647-159278`, and targeted local
  Incus `TestCLICreateDetachE2E` run `e2e-20260521-145657-000000000`.
- Changed `sandcastle tailscale up|status|down` to take an optional tenant
  argument. The Tailscale planners already defaulted an empty reference to the
  current tenant; the CLI now matches the spec's current-tenant user flow while
  still allowing explicit tenant references.
- Verified the Tailscale current-tenant CLI shape with `go test ./...`,
  `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-150242-162965`. The destructive Tailscale tier still requires
  a `SANDCASTLE_E2E_TAILSCALE_AUTHKEY` and route approval policy, which this
  environment does not provide.
- Removed the stale `.env.default` `SANDCASTLE_E2E_DOMAIN_SUFFIX` example that
  survived the earlier harness cleanup. The e2e harness already derives tenant
  DNS suffixes from tenant names, so leaving the old project-domain knob in the
  template would send operators toward a setting the code no longer reads.
- Renamed the admin/runtime "project prefix" config field to "Incus project
  prefix" and changed the documented env template to
  `SANDCASTLE_INCUS_PROJECT_PREFIX`. This prefix controls Incus project names
  like `sc-<tenant>`, not Sandcastle project namespaces. The loader still
  accepts the old `SANDCASTLE_PROJECT_PREFIX` as a fallback so existing local
  environments do not silently fall back to `sc`; the new env var wins when
  both are set.
- While updating the prefix docs, fixed older admin examples that still used
  `SANDCASTLE_PRIVATE_CIDR_POOL` and `SANDCASTLE_INFRASTRUCTURE_PROJECT`; the
  code reads `SANDCASTLE_CIDR_POOL` and `SANDCASTLE_INFRA_PROJECT`.
- Verified the Incus project prefix/env cleanup with `go test ./...`,
  `scripts/e2e.sh gated`, `scripts/e2e.sh local` run
  `e2e-20260521-150755-165557`, host `incus` run
  `e2e-20260521-150808-165654`, route-broker run
  `e2e-20260521-151232-178254`, and disposable VM run
  `e2e-local-vm-20260521-151522`.
- Renamed the private admin CLI command constructors and e2e test names from
  `AdminProject` to `AdminTenant`. The public command was already
  `sandcastle-admin tenant`; this removes stale internal vocabulary without
  changing command behavior.
- Verified the admin tenant private rename with `go test ./internal/cli
  ./internal/e2e`, `go test ./...`, `scripts/e2e.sh gated`, and
  `scripts/e2e.sh local` run `e2e-20260521-152337-183929`.
- Renamed a few remaining private tenant-summary helpers and local variables
  that still used project wording (`tailscale.projectSummary`, tenant status
  list results, route-broker e2e delete plans). Incus API variables that hold
  actual Incus projects remain project-named.
- Verified the tenant-summary helper rename with `go test ./internal/tailscale
  ./internal/tenant ./internal/e2e ./internal/incusx`, `go test ./...`,
  `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-152606-185485`.
- Renamed the machine lifecycle delete action from the private/internal
  `remove` value to `delete`. The public command has been
  `sandcastle delete`; keeping `remove` in JSON plans and executor messages
  was unnecessary command-shape drift.
- Verified the machine delete action rename with `go test ./internal/machine
  ./internal/incusx ./internal/cli ./internal/e2e`, `go test ./...`,
  `scripts/e2e.sh gated`, `scripts/e2e.sh local` run
  `e2e-20260521-153150-187628`, and targeted local Incus
  `TestMachineLifecycleE2E` run `e2e-20260521-153200-000000000`.
- Renamed public route and local host override delete internals from remove to
  delete vocabulary: `DeleteRequest`, `DeletePlan`, `PlanDelete`, manager
  `Delete`, broker `AuthorizeDelete`, CLI formatters, and e2e probe labels.
  I left low-level helpers named `RemoveHostsEntry`, `removeMachineExtraSAN`,
  `removeRouteIngressAttachment`, and `removeRouteBacklink` because those
  describe removing individual entries/devices from local files or Incus
  metadata, not the public command action.
- Tightened user-facing help/docs for the same slice: `sandcastle route delete`
  and `sandcastle host override delete` now say delete, `dns service
  uninstall` says uninstall instead of stop/remove, and the usage/e2e docs use
  delete wording for route and host override workflows.
- Verified the route/host override delete vocabulary slice with `go test
  ./internal/route ./internal/hostoverride ./internal/routebroker
  ./internal/incusx ./internal/cli ./internal/e2e`, `go test ./...`,
  `scripts/e2e.sh gated`, `scripts/e2e.sh local` run
  `e2e-20260521-153741-192726`, host `incus` run
  `e2e-20260521-154044-195768`, route-broker runs
  `e2e-20260521-154507-208246` and `e2e-20260521-155343-213621`, and
  disposable VM run `e2e-local-vm-20260521-154755`.
- Adjusted `sandcastle route create` and `sandcastle host override create`
  help text from "Plan..." to "Create..." because both commands mutate by
  default and only render plans when `--dry-run` is supplied. The dry-run flag
  text still uses plan vocabulary intentionally.
- Verified the create-help wording cleanup with `go test ./internal/cli
  ./internal/route ./internal/hostoverride`, `go test ./...`,
  `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-160128-222392`.
- Adjusted `sandcastle-admin user token` help text from "Plan..." to
  "Create..." because it creates a restricted certificate add token by default
  and only renders the token plan with `--dry-run`.
- Verified the user-token help cleanup with `go test ./internal/cli
  ./internal/usertrust`, `go test ./...`, `scripts/e2e.sh gated`, and
  `scripts/e2e.sh local` run `e2e-20260521-160343-223640`.
- Implemented the documented `sandcastle config unset <key>` command for the
  same local config keys supported by `config set`: `tenant`, `project`,
  `remote`, and `admin_remote`. The v1 spec already showed `config unset
  project`; the command was missing from the CLI. Unsetting clears only the
  selected key and preserves the rest of `~/.config/sandcastle/config.yml`.
- Updated the usage guide, admin/developer quickstart, and implementation plan
  for `config unset`.
- Verified `config unset` with `go test ./internal/cli ./internal/config`,
  `go test ./...`, `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-160626-224980`.
- Corrected stale docs that still described removed or unimplemented admin
  command shapes: `CONTEXT.md` now lists the implemented restricted-user
  surface (`user create`, `user token`, and tenant access commands), and the v1
  spec now states that admin `status` requires an explicit machine reference.
- Verified the docs audit cleanup with `go test ./...`, `scripts/e2e.sh
  gated`, and `scripts/e2e.sh local` run `e2e-20260521-160859-226933`.
- Wired `sandcastle-admin version` to the existing admin-specific version
  command helper instead of the generic user CLI version helper. The output
  payload remains unchanged; only admin help now says "Print the Sandcastle
  admin command version".
- Verified the admin version help cleanup with `go test ./internal/cli`, `go
  test ./...`, `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-161041-228322`.
