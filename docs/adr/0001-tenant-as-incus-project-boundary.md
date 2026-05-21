# Tenant as the Incus Project Boundary

Sandcastle tenants are represented by Incus projects; Sandcastle projects are lightweight namespaces inside a tenant, not separate Incus projects. This keeps access control, networking, DNS, Tailscale, certificate authority, and storage aligned around the tenant boundary while still letting users organize machines into named projects.

## Considered Options

- Map each Sandcastle project to an Incus project.
- Map each Sandcastle tenant to an Incus project.

## Consequences

- Tenant access grants restricted Incus access to one tenant Incus project.
- Sandcastle project names are stored in tenant metadata and have no v1 settings beyond their names.
- Machine Incus instance names include the Sandcastle project name, such as `default-codex` or `website-codex`, so machine names remain project-scoped.
- Tenant-scoped infrastructure replaces project-scoped infrastructure: one tenant network, DNS service, Tailscale attachment, certificate authority, and storage set serve every project in the tenant.
