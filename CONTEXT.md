# Sandcastle

Sandcastle provides Incus-backed development environments scoped by tenant and
project, with simple CLI management for containers and later VMs.

## Language

**Tenant**:
An admin-created top-level namespace that owns projects, DNS naming, and access boundaries.
_Avoid_: Owner, account

**Tenant DNS Suffix**:
The tenant name used as the final label of Sandcastle private hostnames.
_Avoid_: Tenant TLD, tenant domain

**User**:
An identity that can manage Sandcastle resources within one or more tenants.
_Avoid_: Owner

**Created By**:
Audit metadata recording which user created a resource.
_Avoid_: Resource owner

**Tenant Access**:
A user's permission to manage all projects and machines in a tenant.
_Avoid_: Project grant

**Project**:
A named namespace inside a tenant for grouping Sandcastle runtime resources.
_Avoid_: Incus project when discussing the product concept, project settings

**Incus Project Mapping**:
The rule that each Sandcastle tenant is represented by exactly one Incus project.
_Avoid_: Project-level Incus project

**Incus Instance Name**:
The Incus-level machine name inside a tenant's Incus project, derived from Sandcastle project and machine names.
_Avoid_: Bare machine name in Incus

**Tenant Metadata**:
The authoritative Sandcastle state stored on the tenant's Incus project.
_Avoid_: Local project registry

**Local DNS Installation**:
Machine-local resolver configuration that forwards a tenant DNS suffix to Sandcastle DNS.
_Avoid_: Project DNS install

**Tenant Network**:
The private network shared by all projects and machines in a tenant.
_Avoid_: Project network

**Tenant Infrastructure**:
The DNS, Tailscale, and certificate authority services shared by all projects in a tenant.
_Avoid_: Project sidecars

**Tenant CA**:
The certificate authority used for private machine TLS hostnames in a tenant.
_Avoid_: Project CA

**Tenant Storage**:
Persistent tenant volumes whose default paths are partitioned by project and machine names.
_Avoid_: Project storage volume

**Machine**:
A tenant project runtime environment that a user can list, create, connect to, or delete.
_Avoid_: Sandbox, add, enter, rm

**App Port**:
The machine's primary internal HTTP port.
_Avoid_: Route port

**Private Machine Proxy**:
The per-machine HTTP/HTTPS proxy that serves the machine's private hostname and forwards to its app port.
_Avoid_: Raw app port access as the primary URL

**Container**:
The default Machine type backed by an Incus container.
_Avoid_: Sandbox when discussing user-facing resources

**Machine Template**:
The base image profile used to create a machine.
_Avoid_: Project template

**VM**:
A future Machine type backed by an Incus virtual machine.
_Avoid_: Separate product resource

**Public Route**:
A public HTTP or HTTPS hostname that forwards traffic to a machine.
_Avoid_: Machine flag

**Route Broker**:
The narrow service that authorizes user route requests and mutates global route infrastructure.
_Avoid_: User infrastructure access

**Default Project**:
The normal Project named `default` that exists in every tenant from tenant creation.
_Avoid_: Implicit project, projectless container bucket

**Current Tenant**:
The tenant selected by local CLI configuration for unqualified user commands.
_Avoid_: Owner, SANDCASTLE_OWNER

**Current Project**:
The project selected by CLI input or local CLI configuration, defaulting to the Default Project.
_Avoid_: Projectless mode

## Relationships

- A **Tenant** has one or more **Projects**.
- A **Tenant** has exactly one **Incus Project Mapping**.
- A **Tenant** has one **Tenant Network** shared by all its **Projects**.
- A **Tenant** has one **Tenant Infrastructure** set shared by all its **Projects**.
- A **Tenant** has **Tenant Storage** shared by all its **Projects**.
- Admin tenant creation requires only the **Tenant** name; infrastructure details are derived from admin configuration.
- Admin tenant deletion refuses non-empty tenants unless explicitly purged.
- The admin CLI manages **Tenants** with `tenant list`, `tenant create`, `tenant status`, `tenant delete`, `tenant grant`, `tenant revoke`, and `tenant users`.
- The admin CLI manages **Users** with `user create` and `user token`; **Tenant Access** is managed with `tenant grant`, `tenant revoke`, and `tenant users`.
- Default machine storage paths include the **Project** and **Machine** names.
- Trust installation is tenant-scoped and trusts the **Tenant CA** for private machine hostnames in that **Tenant**.
- A **Project** belongs to exactly one **Tenant**.
- A **Project** has no settings beyond its name in v1.
- Admins create **Tenants**.
- Users with tenant access create named **Projects** inside that **Tenant**.
- Users with tenant access may delete named **Projects** only when they contain no **Machines**.
- The **Default Project** cannot be deleted.
- **Project** names are DNS-safe lowercase labels.
- Infrastructure words such as `default`, `dns`, `tailscale`, `ca`, `route`, `admin`, and `infra` are reserved **Project** names; `default` is created only by tenant creation.
- A **Project** has zero or more **Machines**.
- A **Machine** belongs to exactly one **Project**.
- A **Machine** name is unique within its **Project**.
- **Machine** names are DNS-safe lowercase labels and must not use reserved infrastructure words.
- A **Machine** has one **App Port**, defaulting to `3000`.
- A **Machine Template** is a **Machine** property, not a **Project** property.
- Machine creation defaults to the AI container **Machine Template**.
- Machine creation starts the **Machine** and connects in an interactive terminal unless detached.
- Each **Machine** has a **Private Machine Proxy** that serves the machine's private hostname and forwards to its **App Port**.
- An **Incus Instance Name** is `{project}-{machine}` so two projects in the same tenant can each have a machine with the same name.
- A **Container** is the default **Machine** type.
- A **VM** is a future **Machine** type.
- The user CLI manages **Machines** with `list`, `create`, `connect`, `start`, `stop`, `restart`, `status`, and `delete`.
- **Machine** is the implicit top-level resource in both user and admin CLIs.
- The user CLI manages **Public Routes** separately with `route list`, `route create`, `route status`, and `route delete`.
- The user CLI manages **Projects** with `project list`, `project create`, `project status`, and `project delete`.
- User **Public Route** mutations go through the **Route Broker**.
- All user **Public Route** operations go through the **Route Broker**.
- The **Route Broker** authenticates users with their Sandcastle Incus client certificate.
- **Public Routes** are globally registered in infrastructure metadata with tenant, project, and machine target identity.
- A **Public Route** hostname is any unclaimed public DNS name that proves it points at Sandcastle ingress.
- A **Public Route** hostname is not derived from a private machine hostname.
- A **Public Route** stores its target port explicitly when created.
- Changing a **Machine** app port does not silently change existing **Public Routes**.
- Any **User** with **Tenant Access** can delete **Public Routes** targeting that **Tenant**.
- The admin CLI manages **Machines** for any tenant with the same verbs as the user CLI.
- `sandcastle-admin` is the canonical admin CLI.
- Admin `status` takes a machine reference and reports machine status in the explicit **Tenant**.
- An admin machine reference is `tenant/machine` or `tenant/project/machine`; omitted project means the **Default Project**.
- Admin machine lookup references use the same unique-search behavior as user lookup references, scoped to the explicit **Tenant**.
- Admin `list` takes `tenant` for all projects or `tenant/project` for one project.
- Admin `list` uses `-u` or `--include-unmanaged` for unmanaged Incus instances; all-project scope is expressed by passing a tenant reference.
- `list` without a project lists **Machines** in the configured **Current Project** when set, otherwise across every **Project** in the current **Tenant**.
- `list project` lists only **Machines** in that **Project**.
- `list --all-projects` or `-a` overrides configured **Current Project** narrowing.
- Machine list output always includes each **Machine**'s **Project**.
- `route list` follows the same project scoping rules as machine `list`.
- Public route list output always includes each route target's **Project** and **Machine**.
- Machine `status` may show **Public Route** details.
- Machine `list` shows only a compact **Public Route** indicator.
- Every **Tenant** starts with exactly one **Default Project**.
- The **Default Project** follows the same project rules as any other **Project**.
- A **Machine** hostname is `machine.project.tenant`, where `tenant` is the **Tenant DNS Suffix**.
- A **Machine** gets exact and per-machine wildcard private DNS records.
- Sandcastle does not create project-wide or tenant-wide private DNS wildcards.
- A **Tenant DNS Suffix** must not be a public TLD, IANA special-use name, or admin-denied local suffix.
- **Local DNS Installation** is configured per **Tenant DNS Suffix**.
- **Tenant Metadata** is the source of truth for the tenant's project list.
- **Machine** metadata records the machine's **Project**, name, and type.
- Sandcastle metadata uses **Tenant**, **Project**, and **Machine** vocabulary, with no `owner` or `sandbox` compatibility aliases.
- Normal Sandcastle operations ignore unmanaged Incus instances.
- List commands may show unmanaged Incus instances when explicitly requested with `--include-unmanaged` or `-u`.
- `-u` only means include unmanaged Incus instances; it does not override project scoping.
- Unmanaged Incus instances are shown only in tenant-wide list output.
- Unmanaged Incus instance rows appear only when list scope is tenant-wide and unmanaged output is explicitly requested.
- Status output always reports unmanaged Incus instance counts.
- A bare machine reference in the user CLI belongs to the **Current Project**.
- If no project is supplied by CLI input, environment, or local configuration, the **Current Project** is the **Default Project**.
- The user CLI reads the **Current Tenant** from `SANDCASTLE_TENANT` or local configuration.
- Local configuration may store default tenant and project selections.
- Environment variables override local configuration.
- Machine creation resolves the **Current Project** from an explicit reference, `SANDCASTLE_PROJECT`, local project configuration, or the **Default Project**, in that order.
- Machine lookup commands may search across projects when no project is supplied and no `SANDCASTLE_PROJECT` is set, but only act when the machine name is unique.
- Destructive machine lookup commands require confirmation when the **Project** was inferred, unless the user supplies an explicit confirmation flag.
- A **User** may have **Tenant Access** to one or more **Tenants**.
- **Tenant Access** grants access to every **Project** and **Machine** in that **Tenant**.
- **Tenant Access** grants management rights over **Projects**, **Machines**, and **Public Routes** in that **Tenant**.
- User CLI commands operate in exactly one **Current Tenant**.
- When a user has multiple tenants and no **Current Tenant** is selected, user CLI commands fail until the tenant is selected.
- Bare user `status` reports **Current Tenant** status.
- Admins grant and revoke **Tenant Access**, not project access.
- User deletion removes **Tenant Access** and user credentials, not tenant resources.
- **Created By** metadata does not affect authorization.
- Raw Incus access is not a supported user interface.
- A **User** is not the namespace owner; the **Tenant** is.

## Example Dialogue

> **Dev:** "When the admin creates a tenant, which project does the first container go into?"
> **Domain expert:** "Every Tenant starts with a default Project, so a container can be created before the user names a project."
>
> **Dev:** "What is the private hostname for container `codex` in tenant `acme`'s default project?"
> **Domain expert:** "`codex.default.acme`; Sandcastle keeps hostnames short and validates that `acme` does not collide with known DNS roots."

## Flagged Ambiguities

- "owner" was previously used as the top-level namespace in code and specs; resolved: the canonical domain term is **Tenant**, and **User** is only an access identity.
- "default" could mean CLI shorthand or a real project; resolved: **Default Project** is a real **Project** named `default`.
- "tenant-tld" suggested that the tenant name is a public DNS top-level domain; resolved: use **Tenant DNS Suffix** for the private final hostname label.
- A bare machine name in the CLI could imply a projectless resource; resolved: it means the machine in the **Current Project**, which defaults to the **Default Project**.
- `SANDCASTLE_OWNER` would preserve old terminology; resolved: use `SANDCASTLE_TENANT` only, with no compatibility alias.
- Existing owner/project/sandbox resources and metadata do not require backward compatibility migration.
- Older command words such as `add`, `enter`, and `rm` were considered; resolved: the canonical machine CLI verbs are `list`, `create`, `connect`, `start`, `stop`, `restart`, `status`, and `delete`.
- `inspect` was considered for detailed state; resolved: `status` is the canonical detail command.
- `docs/sandcastle-v1-spec.md` previously described the superseded owner/project model; resolved domain language now lives here, in ADR-0001, and in the rewritten v1 spec.
