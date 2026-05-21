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
