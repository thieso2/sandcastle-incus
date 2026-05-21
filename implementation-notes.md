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
- Local/admin config has started moving from `Owner`/`SANDCASTLE_OWNER` to
  `Tenant`/`SANDCASTLE_TENANT`. Local config also has a `Project` field for the
  new current-project behavior.
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
- Route broker authorization is now tenant-grant based. The mTLS principal still
  has a human/user owner string for audit (`CreatedBy`), but route
  create/delete authorization checks whether the certificate grants the target
  Incus tenant project (`sc-{tenant}`), not whether the user name matches the
  tenant.
- The Incus adapter layer has started moving from project/sandbox method
  semantics to tenant/machine semantics. Some concrete type names in
  `internal/incusx` are still old (`ProjectCreator`, `SandboxCreator`) because
  the surrounding CLI has not been renamed yet, but their metadata behavior now
  writes and reads tenant/machine state.
- User CLI command names now expose the new no-alias surface for the main
  machine lifecycle: `list`, `create`, `connect`, and `delete`. I removed the
  old `ls`, `add`, `enter`, and `rm` registrations from the root command rather
  than keeping compatibility aliases. `status <machine>` currently reuses the
  machine inspect payload so existing status/detail tests have one canonical
  command while the old `inspect` command is no longer registered.
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
  access boundary a project.
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
  shape. Some deeper planning and e2e design docs still use historical
  milestone language and need a separate documentation pass.
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
