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
