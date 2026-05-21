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
- Route broker authorization is now tenant-grant based. The mTLS principal still
  has a human/user owner string for audit (`CreatedBy`), but route add/remove
  authorization checks whether the certificate grants the target Incus tenant
  project (`sc-{tenant}`), not whether the user name matches the tenant.
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
  of `project ...`. I have not yet finished the top-level admin machine shape
  (`sandcastle-admin list/create/connect/delete tenant/...`); that remains a
  real gap against the final spec.
- E2E fixtures and diagnostics now use tenant references and tenant local-DNS
  state. Safe e2e tiers pass (`scripts/e2e.sh unit`, `gated`, `local`). The
  destructive `incus` tier was attempted with `SANDCASTLE_E2E=1` and currently
  cannot complete in this environment because Incus `local` cannot connect on
  this non-Linux host; image-dependent tests also skip without
  `SANDCASTLE_E2E_BASE_IMAGE_SOURCE` and `SANDCASTLE_E2E_AI_IMAGE_SOURCE`.
