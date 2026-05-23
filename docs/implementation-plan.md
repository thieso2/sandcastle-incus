# Sandcastle Incus Implementation Plan

This plan tracks the current Sandcastle v1 shape. Sandcastle models an Incus
project as a tenant boundary, with lightweight Sandcastle projects inside that
tenant and Incus containers as development machines. The older
owner/project/sandbox command shape is historical only and is not a compatibility
target.

## Principles

- The tenant Incus project is the isolation and restricted-certificate boundary.
- Sandcastle project names are lightweight metadata namespaces inside a tenant.
- Machine metadata on Incus instances is the source of truth for lifecycle,
  DNS, routes, app ports, local host overrides, and project membership.
- Normal users operate through restricted Incus certificates granted to tenant
  Incus projects.
- Admin commands own privileged setup: tenants, restricted users, CIDR
  allocation, image aliases, and shared route infrastructure.
- User commands own day-to-day machine lifecycle.
- Containers ship first. VM machine support stays outside v1.
- AI is a default image template, not a separate resource type.
- Management operations use the Incus Go SDK. Interactive `connect` delegates to
  Incus exec semantics for PTY quality.

## Current Package Layout

```text
cmd/sandcastle/
cmd/sandcastle-admin/
cmd/sc/
internal/caddy/
internal/certs/
internal/cidr/
internal/cli/
internal/config/
internal/dns/
internal/domain/
internal/e2e/
internal/hostoverride/
internal/images/
internal/incusx/
internal/infra/
internal/localdns/
internal/localtrust/
internal/machine/
internal/meta/
internal/naming/
internal/route/
internal/routebroker/
internal/tailscale/
internal/tenant/
internal/usertrust/
```

Feature packages own use-case orchestration. Shared infrastructure packages such
as `incusx`, `meta`, `naming`, and `cidr` stay small and stable.

## Command Surface

User CLI:

```text
sandcastle version
sandcastle remote add <name> <join-token> [--tenant <tenant>]
sandcastle config show
sandcastle config set tenant <tenant>
sandcastle config unset <key>
sandcastle list [project] [--all-projects]
sandcastle create [project/]machine [--detach]
sandcastle connect [project/]machine [-- command...]
sandcastle status [machine|tenant]
sandcastle start [project/]machine
sandcastle stop [project/]machine
sandcastle restart [project/]machine
sandcastle delete [project/]machine --yes
sandcastle project list
sandcastle project create <name>
sandcastle project status <name>
sandcastle project delete <name> --yes
sandcastle port set [project/]machine <port>
sandcastle dns apply|status|install|refresh|uninstall <tenant>
sandcastle tailscale up|status|down [tenant]
sandcastle trust install|uninstall <tenant>
sandcastle host override create [project/]machine <hostname>
sandcastle host override list [tenant]
sandcastle host override delete [project/]machine <hostname>
sandcastle route create <hostname> [project/]machine
sandcastle route list
sandcastle route status <hostname>
sandcastle route delete <hostname>
sandcastle incus [args...]
```

Admin CLI:

```text
sandcastle-admin version
sandcastle-admin tenant list
sandcastle-admin tenant create <tenant>
sandcastle-admin tenant status <tenant>
sandcastle-admin tenant delete <tenant> --yes [--purge]
sandcastle-admin tenant set-ssh-key <tenant> <key>
sandcastle-admin tenant grant <tenant> <user>
sandcastle-admin tenant revoke <tenant> <user>
sandcastle-admin tenant users <tenant>
sandcastle-admin list tenant[/project]
sandcastle-admin create tenant[/project]/machine [--detach]
sandcastle-admin connect tenant[/project]/machine [-- command...]
sandcastle-admin status tenant[/project]/machine
sandcastle-admin delete tenant[/project]/machine --yes
sandcastle-admin infra create|delete
sandcastle-admin image build base|ai
sandcastle-admin image import base|ai <source-ref>
sandcastle-admin image sync <image-ref>
sandcastle-admin tld refresh
sandcastle-admin user create <user> [--tenant <tenant>...]
sandcastle-admin user token <user> [--tenant <tenant>...]
sandcastle-admin user delete <user>
sandcastle-admin route-broker serve
```

## Delivered Vertical Slices

### 1. Foundation

- Go module, Cobra CLIs, `sandcastle` product binary, `sc` alias, and
  `sandcastle-admin` admin binary.
- Text and JSON output helpers.
- Local config file support with `tenant`, `project`, `remote`, and
  `admin_remote`.
- Incus config isolation per Sandcastle remote.
- Unit and gated e2e harnesses.

### 2. Tenant Metadata And Admin Lifecycle

- Tenant references map to Incus projects named `sc-<tenant>`.
- Tenant creation initializes tenant metadata, tenant-local image aliases,
  private bridge network, home/workspace/CA volumes, CoreDNS sidecar, Tailscale
  sidecar, and default project metadata.
- CIDR allocation scans managed tenant metadata and live Incus networks.
- Tenant deletion preserves durable volumes by default and purges only with
  explicit `--purge`.
- Domain suffix validation uses embedded public TLD and special-use deny-list
  snapshots.

### 3. Restricted Users

- Admin user creation issues restricted Incus certificate add tokens.
- Tenant grants mutate Incus certificate project restrictions.
- Human token output points developers at `sc remote add` and explicit tenant
  configuration instead of treating a user name as a tenant.
- Route broker authorization checks tenant grants, not user-name ownership.

### 4. Machine Lifecycle

- Machine refs are `[project/]machine` for users and
  `tenant[/project]/machine` for admin operations.
- Bare machine refs use the configured current project when set; otherwise they
  resolve only when unambiguous across the tenant.
- Instance names are `{project}-{machine}`.
- Private hostnames are `{machine}.{project}.{tenant}` under the tenant DNS
  suffix.
- Machine creation supports `--template`, `--home-dir`, `--workspace-dir`, and
  `--detach`.
- Machine setup creates Linux user state, storage mounts, app-port metadata,
  Caddy config, and tenant CA leaf certificates.
- Lifecycle commands are `create`, `connect`, `start`, `stop`, `restart`, and
  `delete`.

### 5. Projects Inside A Tenant

- User project management lives under `sandcastle project`.
- Projects are metadata namespaces, not Incus projects.
- Project status reports tenant, project, and machine count.
- Project deletion rejects `default` and requires the project to be empty.

### 6. Tenant DNS And Tailscale

- Tenant CoreDNS records are rendered from machine metadata.
- Exact and per-machine wildcard records are supported.
- Tenant-wide wildcard records are intentionally not generated.
- `sandcastle dns apply|status <tenant>` reconciles sidecar DNS.
- `sandcastle dns setup|teardown [tenant]` applies tenant DNS and installs or
  removes local resolver configuration in one flow.
- `sandcastle dns install|refresh|uninstall <tenant>` manages local resolver
  state for the tenant DNS sidecar.
- `sandcastle tailscale up|status|down [tenant]` manages the tenant Tailscale
  sidecar and advertises the tenant private CIDR.

### 7. Local Trust And Host Overrides

- `sandcastle trust install|uninstall <tenant>` manages local trust for a
  tenant CA.
- Host overrides are exact hostnames only.
- Override create/delete rewrites managed local host state and reissues the
  machine certificate with the extra SAN.

### 8. Public Routes

- Route targets are machines.
- Route metadata stores target tenant, project, machine, app port, route port,
  and creator audit identity.
- The route broker accepts mTLS clients, maps their certificate to restricted
  Incus tenant grants, verifies DNS proof, and mutates route metadata.
- Shared infrastructure Caddy serves public HTTP/HTTPS routes when public route
  environment is configured.

### 9. Images

- Base and AI image aliases are tenant-local after tenant creation.
- Admin image import/sync flows copy images into the target Incus project.
- Local Unix-socket image propagation uses relay mode so project image copies do
  not require a network-listening source server.
- Image build e2e is gated by explicit image-build env and pinned AI CLI
  versions.

### 10. E2E And Disposable VM Harness

- `scripts/e2e.sh` owns reproducible tiers: `unit`, `gated`, `local`, `incus`,
  `restricted`, `tailscale`, `images`, `route-broker`, `public-routes`,
  `local-vm`, and `cleanup`.
- `scripts/e2e-local-vm.sh` creates a disposable local Incus VM, installs Go,
  mise, and nested Incus, seeds nested image aliases, and runs local mutation
  tests inside the VM.
- Destructive tiers fail closed unless their required environment variables are
  present.
- Cleanup requires an explicit long run id.

## Remaining Work

- Continue reducing internal type-name drift where old names remain in private
  adapters, provided the rename lowers future maintenance cost.
- Run external e2e tiers when their environment dependencies are available:
  HTTPS Incus remote for restricted-user flows, Tailscale auth key, Docker or
  equivalent image build tooling with pinned AI CLI versions, and delegated
  public route DNS plus Let's Encrypt contact email.
- Keep `docs/usage.html`, `docs/admin-developer-quickstart.html`, and this plan
  in sync with every CLI command or flag change.
