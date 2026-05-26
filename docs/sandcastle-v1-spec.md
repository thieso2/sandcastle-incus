# Sandcastle v1 Specification

This document captures the v1 product and architecture model for Sandcastle.
The canonical domain language is **Tenant**, **Project**, **Machine**, and
**Public Route**. The older owner/project/sandbox model is intentionally not
carried forward.

Sandcastle gives developers Incus-backed development machines that are easy to
create from a CLI, reachable through short private DNS names, and optionally
published through public HTTP routes.

## Goals

- Provide a user-facing CLI named `sandcastle`, with `sc` as an alias.
- Provide a canonical admin CLI named `sandcastle-admin`.
- Let users manage machines with simple top-level commands: `list`, `create`,
  `connect`, `start`, `stop`, `restart`, `status`, and `delete`.
- Make **Tenant** the access, networking, DNS, storage, and Incus project
  boundary.
- Let users organize machines with lightweight **Projects** inside a tenant.
- Create a real `default` project for every tenant.
- Store authoritative Sandcastle state in Incus metadata.
- Use Incus restricted client certificates as the primary security primitive.
- Support optional public HTTP/HTTPS routes through shared infrastructure and a
  narrow route broker.

## Non-Goals For v1

- Backward compatibility with owner/project/sandbox metadata or commands.
- Per-project access control. Access is tenant-wide in v1.
- Per-project networking, DNS, certificate authority, or storage.
- VM machines. The model keeps room for VMs later, but v1 implements
  containers first.
- A broad Sandcastle control-plane server.
- Tailscale DNS or Tailscale DNS API integration.
- Public TCP/UDP exposure beyond HTTP/HTTPS hostname routes.
- Project-wide or tenant-wide private DNS wildcards.
- Raw `incus` usage as a supported user interface.

## Core Model

### Tenant

A tenant is an admin-created top-level namespace. Each tenant maps to exactly
one Incus project.

Example:

```text
tenant: acme
Incus project: sc-acme
private DNS suffix: acme
```

Tenant creation requires only the tenant name. CIDR allocation, images,
infrastructure, storage, and defaults come from admin configuration.

Each tenant contains:

- one Incus project;
- one private tenant network;
- one DNS service;
- one Tailscale attachment;
- one tenant CA;
- tenant-scoped home/workspace storage;
- one or more Sandcastle projects;
- zero or more machines.

Every tenant starts with a `default` project. The default project is a normal
project named `default`; it is not an implicit projectless bucket.

Tenant names are DNS-safe lowercase labels. The tenant name is also the private
DNS suffix, so Sandcastle denies names that are public TLDs, IANA special-use
names, or admin-denied local suffixes.

### Project

A project is a lightweight namespace inside a tenant. Projects do not map to
Incus projects and have no v1 settings beyond their names.

Project names are DNS-safe lowercase labels. Infrastructure words such as
`default`, `dns`, `tailscale`, `ca`, `route`, `admin`, and `infra` are reserved.
The `default` project is created only by tenant creation.

Users with tenant access may create named projects. Users may delete named
projects only when they contain no machines. The default project cannot be
deleted.

### Machine

A machine is the runtime environment users manage. The v1 machine type is an
Incus container. VM support is a later extension.

Machine names are DNS-safe lowercase labels and are unique within a project.
Inside the tenant's Incus project, the Incus instance name is:

```text
{project}-{machine}
```

Examples:

```text
default/codex -> default-codex
website/codex -> website-codex
```

Machine creation defaults to an AI container template and app port `3000`.
Users can select another machine template, such as `base`, per machine.
Template is a machine property, not a project property.

Machine creation starts the machine. In an interactive terminal it connects to
the machine after successful setup unless detached.

Every machine has a private machine proxy that serves the private hostname on
HTTP/HTTPS and forwards to the machine's app port:

```text
https://codex.default.acme -> http://127.0.0.1:3000 inside default/codex
```

Changing a machine app port changes private proxy behavior, but does not
silently change existing public routes.

### User And Access

A user is an identity that can receive access to one or more tenants. Users do
not own namespaces; tenants do.

Tenant access grants management rights over every project, machine, and public
route in that tenant. Created-by metadata is audit-only and does not affect
authorization.

Admin user deletion removes tenant access and user credentials. It does not
delete tenant resources.

Raw Incus access is not a supported user interface. Users may technically have
restricted Incus access to a tenant's Incus project, but Sandcastle commands
manage only Sandcastle-managed resources by default.

## Source Of Truth

All authoritative Sandcastle state is stored in Incus metadata. Local files are
only machine-local configuration, DNS resolver state, trust installation state,
or caches.

Use scalar metadata keys for searchable facts and versioned JSON state blobs for
structured data. New metadata uses tenant/project/machine vocabulary only. There
are no `owner` or `sandbox` compatibility aliases.

Tenant Incus project metadata conceptually stores:

```text
user.sandcastle.kind=tenant
user.sandcastle.version=1
user.sandcastle.tenant=acme
user.sandcastle.private_cidr=10.88.17.0/24
user.sandcastle.projects=["default","website"]
user.sandcastle.state={...}
```

Machine instance metadata conceptually stores:

```text
user.sandcastle.kind=machine
user.sandcastle.version=1
user.sandcastle.tenant=acme
user.sandcastle.project=website
user.sandcastle.machine=codex
user.sandcastle.type=container
user.sandcastle.app_port=3000
user.sandcastle.created_by=alice
user.sandcastle.state={...}
```

Public routes are authoritative in global infrastructure metadata because public
hostnames are globally unique.

## Networking

Networking is tenant-scoped. Each tenant gets one private Incus managed bridge
inside the tenant Incus project. The admin CLI allocates the tenant CIDR from a
configured pool and records the allocation in tenant metadata.

Example:

```text
tenant:     acme
CIDR:       10.88.17.0/24
gateway:    10.88.17.1
tailscale:  10.88.17.2
dns:        10.88.17.3
machines:   10.88.17.20-10.88.17.199
reserved:   10.88.17.200-10.88.17.254
```

Machines get one private IP by default. A machine gets ingress networking only
when a public route targets it.

The tenant network is routed over Tailscale by the tenant Tailscale attachment.
This is the normal developer access path.

## Tailscale

Each tenant has one Tailscale attachment. The admin creates and prepares tenant
infrastructure, but does not choose the user's tailnet.

The user connects the current tenant:

```bash
sandcastle tailscale up
```

The CLI execs into the tenant Tailscale service and runs `tailscale up` with the
tenant private CIDR advertised. The user chooses the tailnet by authenticating
that Tailscale login. A tenant has exactly one active Tailscale attachment.

For unattended automation and e2e tests, Sandcastle can pass an auth key and an
advertised tag. Do not store auth keys, reusable secrets, or stale login URLs in
metadata.

## Private DNS

Sandcastle does not use Tailscale DNS in v1.

Each tenant runs one DNS service on the tenant network. It is authoritative for
the tenant DNS suffix, which is the tenant name:

```text
acme
```

Machine hostnames use:

```text
machine.project.tenant
```

Each machine gets exact and per-machine wildcard private DNS records:

```text
codex.default.acme          A 10.88.17.20
*.codex.default.acme        A 10.88.17.20
app.website.acme            A 10.88.17.21
*.app.website.acme          A 10.88.17.21
```

Sandcastle does not create project-wide or tenant-wide wildcards:

```text
*.default.acme   # not created
*.acme           # not created
```

## Local DNS Installation

Developer machines use per-suffix local resolver configuration. Local DNS
installation is per tenant DNS suffix, not per project.

On macOS, the CLI installs a resolver for the tenant suffix that points directly
at the tenant `sc-dns` sidecar:

```text
/etc/resolver/acme
nameserver 10.248.0.3
port 53
```

## Tenant CA And TLS

Each tenant has one tenant CA. Trust installation is tenant-scoped:

```bash
sandcastle trust install acme
sandcastle trust uninstall acme
```

Installing tenant trust means trusting that tenant's infrastructure to mint
private certificates for hostnames under the tenant suffix, such as:

```text
codex.default.acme
*.codex.default.acme
```

The CLI must warn that trusting a tenant CA affects all private machine
hostnames in that tenant.

## Storage

Storage is tenant-scoped. Tenant home and workspace volumes are shared by all
projects, with project-aware subdirectories by default.

Default paths include the project name, so all machines in a project share the
same home and workspace trees:

```text
home/default
workspace/default
home/website
workspace/website
```

Machine creation may still allow explicit storage subdirectories for advanced
cases, but project-level sharing is the default.

## Images And Templates

v1 requires Sandcastle-maintained base images.

Images are published as OCI images and synced into Incus as managed aliases by
admin setup. The minimal base image contains:

- Caddy;
- OpenSSH server;
- tmux;
- mosh;
- SSH agent socket handoff for refreshed forwarded-agent sockets;
- vim;
- sudo;
- curl, ca-certificates, bash;
- Sandcastle bootstrap script or helper binary;
- expected directories such as `/etc/sandcastle/caddy`, `/var/lib/sandcastle`,
  and `/workspace`.

The AI image extends the base image and includes pinned versions of AI and
developer tools:

- Codex CLI;
- Claude Code;
- Gemini CLI;
- GitHub CLI;
- Git;
- package managers and common build tools;
- container client tooling where useful.

The AI image should not include credentials. Credentials live in mounted home
state, for example `/home/alice/.codex`, `/home/alice/.claude`, and
`/home/alice/.config/gh`.

AI is not a separate resource type or command group in v1. AI machines are
normal container machines that use the AI template.

Container build/run capability is opt-in per machine. The AI image may include
client tooling, but privileged Docker-in-Docker or equivalent nesting is not
enabled by default.

## Public HTTP Routes

Public routes expose HTTP/HTTPS hostnames through global infrastructure Caddy.
They are HTTP/HTTPS only in v1.

Public TLS terminates at infrastructure Caddy using Let's Encrypt.
Infrastructure Caddy proxies HTTP to the target machine's ingress IP and route
port.

Public route hostnames are arbitrary public DNS names. They are not derived from
private machine hostnames.

Example:

```bash
sandcastle route create app.example.com website/codex
```

The route broker verifies:

- the caller has tenant access;
- the target machine exists and is Sandcastle-managed;
- the requested public hostname is unclaimed;
- public DNS for the requested hostname points at Sandcastle ingress;
- target ingress networking exists.

Default route port resolution:

```text
route port -> current machine app port -> 3000
```

The resolved route port is stored explicitly at creation time. Later changes to
the machine app port do not silently change existing public routes.

Routes are global infrastructure metadata because hostname uniqueness and Caddy
configuration are global host concerns. Tenant metadata may cache backlinks for
display, but the authoritative route table lives in infrastructure metadata.

All user route operations go through the route broker:

```bash
sandcastle route list
sandcastle route create app.example.com website/codex
sandcastle route status app.example.com
sandcastle route delete app.example.com
```

The route broker:

- authenticates users with their Sandcastle Incus client certificate;
- maps the certificate fingerprint to tenant access;
- authorizes list/status/create/delete against tenant access;
- updates global route metadata;
- regenerates and reloads infrastructure Caddy.

The route broker is intentionally narrow. It does not create tenants, manage
users, allocate CIDRs, manage Tailscale, or provide broad infrastructure access.

## User CLI Shape

The product command is `sandcastle`. Install `sc` as a symlink or alias to the
same binary.

Machine is the implicit top-level resource:

```bash
sandcastle list
sandcastle create codex
sandcastle connect codex
sandcastle start codex
sandcastle stop codex
sandcastle restart codex
sandcastle status codex
sandcastle delete codex --yes
```

Project references:

```bash
sandcastle create codex          # current project, or default
sandcastle create website/codex  # explicit project
```

Machine creation resolves project in this order:

1. explicit project in the CLI reference;
2. `SANDCASTLE_PROJECT`;
3. local project configuration;
4. `default`.

Machine lookup commands may search across projects when no project is supplied
and no current project is configured, but only act when the machine name is
unique. Destructive lookup commands require confirmation when the project was
inferred, unless the user supplies an explicit confirmation flag.

User commands operate in exactly one current tenant. The current tenant comes
from `SANDCASTLE_TENANT` or local configuration. If a user has multiple tenants
and no current tenant is selected, user commands fail until the tenant is
selected.

Local config commands:

```bash
sandcastle config set tenant acme
sandcastle config set project website
sandcastle config unset project
sandcastle config show
```

Environment variables override local configuration.

Listing behavior:

```bash
sandcastle list                  # current project if configured, otherwise all projects
sandcastle list default          # default project only
sandcastle list --all-projects   # all projects
sandcastle list -a               # short for --all-projects
sandcastle ls                    # alias for list
```

Machine list output always includes the project, machine, FQDN, private IP,
local creation time, and state. List output includes unmanaged Incus instances.
Machine status may show public route details. Status output always reports
unmanaged Incus instance counts.

Project commands:

```bash
sandcastle project list
sandcastle project create website
sandcastle project status website
sandcastle project delete website
```

Route commands:

```bash
sandcastle route list
sandcastle route create app.example.com website/codex
sandcastle route status app.example.com
sandcastle route delete app.example.com
```

Route listing follows machine list project scoping rules. Route list output
always includes target project and machine.

Bare user `status` reports current tenant status:

```bash
sandcastle status
```

Command output defaults to human-readable text. JSON is opt-in:

```bash
sandcastle list --output json
sandcastle status --json
```

## Admin CLI Shape

The canonical admin command is `sandcastle-admin`.

Tenant commands:

```bash
sandcastle-admin tenant list
sandcastle-admin tenant create acme
sandcastle-admin tenant status acme
sandcastle-admin tenant delete acme --yes
sandcastle-admin tenant delete acme --yes --purge
sandcastle-admin tenant grant acme alice
sandcastle-admin tenant revoke acme alice
sandcastle-admin tenant users acme
```

Tenant deletion refuses non-empty tenants unless explicitly purged. Purge
removes routes, machines, tenant infrastructure, tenant storage, and the tenant
Incus project.

User commands:

```bash
sandcastle-admin user create alice --tenant acme
sandcastle-admin user token alice --tenant acme
sandcastle-admin user delete alice
```

Tenant access can be embedded in user create/token flows for asynchronous
onboarding. Existing accepted certificates are managed with the tenant-first
grant, revoke, and users commands above.

Admin machine commands use the same verbs as the user CLI, scoped by an explicit
tenant reference:

```bash
sandcastle-admin list acme
sandcastle-admin list acme/website
sandcastle-admin create acme/codex
sandcastle-admin create acme/website/codex
sandcastle-admin connect acme/website/codex
sandcastle-admin status acme/website/codex
sandcastle-admin delete acme/website/codex --yes
```

For admin machine references, `tenant/machine` means the tenant's default
project. Admin lookup references use the same unique-search behavior as user
lookup references, scoped to the explicit tenant.

Admin list behavior:

```bash
sandcastle-admin list acme       # all projects in tenant
sandcastle-admin list acme/site  # project only
```

Admin `status` takes an explicit machine reference. There is no bare admin
status command in v1.

## Unmanaged Incus Resources

Normal Sandcastle operations ignore unmanaged Incus instances. List commands show
unmanaged Incus instances by default.

Status output always reports unmanaged instance counts.

Tenant purge may remove unmanaged instances because it is explicitly destructive
and removes the tenant Incus project.

## Implementation Notes

- This spec supersedes the older owner/project/sandbox model and does not
  require backward-compatible aliases or metadata migration.
- See `CONTEXT.md` for canonical domain language.
- See `docs/adr/0001-tenant-as-incus-project-boundary.md` for the tenant Incus
  project boundary decision.
- Management operations should use the official Incus Go SDK wherever practical.
- Local DNS, local trust, and CLI config are machine-local state, not Incus
  source of truth.
- Public route operations are the user workflow that crosses into global
  infrastructure, and they are mediated by the route broker.
