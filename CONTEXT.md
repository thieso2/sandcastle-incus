# Sandcastle

Sandcastle provides Incus-backed development environments scoped by tenant and
project, with simple CLI management for containers and later VMs.

## Language

**Tenant**:
An admin-created top-level namespace that owns projects, DNS naming, and access boundaries.
_Avoid_: Owner, account

**Personal Tenant**:
An automatically created Tenant scoped to one allowlisted User.
_Avoid_: User-owned tenant, GitHub tenant

**Tenant DNS Suffix**:
The tenant name used as the final label of Sandcastle private hostnames.
_Avoid_: Tenant TLD, tenant domain

**Personal Tenant DNS Suffix**:
The Tenant DNS Suffix initially derived from the allowlisted GitHub Username for a Personal Tenant.
_Avoid_: Numeric GitHub account ID suffix, auto-updated GitHub username

**GitHub Username Tenant Name**:
The normalized GitHub username form allowed for Personal Tenant names.
_Avoid_: Generic tenant name validation

**User**:
An identity that can manage Sandcastle resources within one or more tenants.
_Avoid_: Owner

**Sandcastle User Key**:
The Sandcastle identifier for a User, derived from the allowlisted GitHub Username in v1.
_Avoid_: Numeric GitHub account ID as user-facing identity, email as identity key

**GitHub Username**:
The GitHub login name used as the Sandcastle User Key, Personal Tenant name, and Personal Tenant DNS Suffix in v1.
_Avoid_: Display-only metadata

**Normalized GitHub Username**:
The lowercase form of a GitHub Username used for Sandcastle identifiers.
_Avoid_: GitHub display casing

**GitHub Email**:
The email address collected from GitHub OAuth and stored as inactive User metadata in v1.
_Avoid_: User identity, notification channel in v1

**Login Allowlist**:
The admin-managed set of GitHub accounts allowed to authenticate to Sandcastle.
_Avoid_: Tenant Access, user registry

**Sandcastle Admin**:
A User allowed to manage login allowlisting and tenant access delegation.
_Avoid_: Tenant owner, infrastructure user

**GitHub OAuth Login**:
The browser sign-in flow that maps a GitHub identity to a Sandcastle User.
_Avoid_: GitHub OpenID login, GitHub OIDC login

**Web Registration**:
The browser flow that creates or confirms a Sandcastle User session after GitHub OAuth Login without provisioning tenant infrastructure.
_Avoid_: Tenant registration, browser tenant creation

**Onboarding Page**:
The signed-in Auth App page that shows GitHub identity status, allowlist status, CLI installation instructions, and the CLI login command.
_Avoid_: Browser provisioner, SSH key upload page

**Sandcastle OIDC Provider**:
The public issuer that signs Sandcastle workload identity tokens for external cloud trust.
_Avoid_: GitHub OIDC provider, OAuth login provider

**Per-Tenant OIDC Issuer**:
The tenant-scoped Sandcastle OIDC issuer used for external cloud trust isolation.
_Avoid_: Global tenant claim filter, per-project issuer

**Workload Identity Token**:
A short-lived OIDC token that identifies both the User and the Machine for external cloud trust.
_Avoid_: Sandbox token, cloud key

**Cloud Identity Audience**:
The external cloud provider audience value that a Workload Identity Token is minted for.
_Avoid_: Auth Hostname, OIDC issuer, service account email

**OIDC Signing Key**:
The Auth App private key used to sign Workload Identity Tokens.
_Avoid_: OAuth client secret, GitHub secret

**Machine Runtime Secret**:
A per-Machine secret used by the Machine to request Workload Identity Tokens from the Auth App.
_Avoid_: User token, Incus certificate, cloud credential

**Cloud Identity Config**:
A User-owned external cloud trust configuration that a Machine can use for workload identity.
_Avoid_: Tenant cloud config, project cloud setting

**Auth App**:
The infrastructure service that handles GitHub login, CLI device login, user registry, and workload identity issuing.
_Avoid_: Route Broker, Incus metadata app

**Infrastructure Seed File**:
An operator-supplied, portable, secret-bearing bootstrap bundle for shared infrastructure configuration and reusable working public TLS material.
_Avoid_: Environment-only deployment, tenant backup, Auth Database backup

**Deployment Name**:
The local operator name that identifies one Sandcastle shared infrastructure seed and stack.
_Avoid_: Tenant name, Auth Hostname, Incus project name

**Auth Hostname**:
The public HTTPS hostname for the Auth App and Sandcastle OIDC Provider issuer.
_Avoid_: Route hostname, tenant hostname

**Auth Database**:
The Auth App's persistent SQLite store for login identities, device login state, token records, and audit state.
_Avoid_: Tenant Metadata, Incus project config

**CLI Device Login**:
The browser-assisted CLI sign-in flow that lets a User authorize a local CLI without pasting long-lived credentials.
_Avoid_: API token login, password login

**Incus Certificate Add Token**:
A one-time Incus token that lets the CLI add its locally generated client certificate as the User's restricted Incus credential.
_Avoid_: Incus auth token, Sandcastle API token, private key

**User SSH Public Key**:
The single current public SSH key uploaded by the CLI during device login and authorized for Machine shell access.
_Avoid_: SSH private key, browser-uploaded SSH key, named SSH key set

**Machine SSH Access**:
The user-facing shell connection path into a Machine using the User's SSH private key.
_Avoid_: Incus exec as user shell, browser shell

**Tailscale Machine IP**:
The Tailscale-assigned IP address used by the CLI for Machine SSH Access.
_Avoid_: Local DNS SSH target, Incus internal IP

**Tailscale Prerequisite**:
The requirement that the user's local machine has a working Tailscale client connected to the relevant Tenant Tailnet before Machine SSH Access can succeed.
_Avoid_: Optional VPN setup, silent CLI Tailscale enrollment

**Sandcastle SSH Key**:
The CLI-generated local SSH key pair used when the user has not selected or already created a default SSH key.
_Avoid_: Server-generated SSH key, tenant SSH key

**Login Readiness**:
The completed CLI state in which credentials, SSH access, Personal Tenant, Default Project, and Tailscale-backed Tenant Infrastructure are ready for Machine creation and connection.
_Avoid_: OAuth completion, browser login success

**CLI Login Result**:
The structured final response from CLI Device Login that lets the CLI persist local configuration and print the next command.
_Avoid_: Browser session payload, provisioning log

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

**Tenant Tailnet**:
The Tailscale network dedicated to exactly one Tenant.
_Avoid_: Shared Sandcastle tailnet, project tailnet

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
- A **Tenant** has exactly one **Tenant Tailnet**.
- A **Tenant** has **Tenant Storage** shared by all its **Projects**.
- A **Deployment Name** maps to one default **Infrastructure Seed File** at `~/.config/sandcastle/<deployment-name>.seed.yml`.
- Shared infrastructure creation may create the default **Infrastructure Seed File** when it does not already exist.
- Shared infrastructure creation may update the **Infrastructure Seed File** only with captured reusable working TLS material, not with transient CLI or environment overrides.
- An **Infrastructure Seed File** is YAML with domain-shaped sections for infrastructure, authentication, routing, images, and reusable TLS material.
- An **Infrastructure Seed File** may contain deployment secrets and must be treated as private operator material.
- Reusable public TLS material in an **Infrastructure Seed File** belongs to a specific Auth Hostname and must not be restored for a different Auth Hostname.
- Shared infrastructure creation prepares configured Sandcastle images unless the image reference is a full external OCI source.
- Admin tenant creation requires only the **Tenant** name; infrastructure details are derived from admin configuration.
- Admin-created non-personal **Tenants** keep the existing Sandcastle tenant naming rule.
- The Auth App creates a **Personal Tenant** for an allowlisted **User** during first CLI Device Login.
- **Web Registration** creates or confirms a **User** session but does not create a **Personal Tenant**.
- The **Onboarding Page** does not accept SSH keys or provision tenant infrastructure.
- The **Onboarding Page** shows install instructions for supported CLI platforms and highlights the detected platform when possible.
- A **Personal Tenant** uses the **Normalized GitHub Username** as its Tenant identity in v1.
- A **Personal Tenant** name follows **GitHub Username Tenant Name** rules in v1.
- A **Personal Tenant DNS Suffix** is initialized from the allowlisted **Normalized GitHub Username**.
- A **Personal Tenant DNS Suffix** does not automatically change when the **GitHub Username** changes.
- Only a **Sandcastle Admin** may delete a **Personal Tenant** in v1.
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
- **Project** names are DNS-safe lowercase labels and may start with a digit.
- **GitHub Username Tenant Name** may start with a digit.
- Infrastructure words such as `default`, `dns`, `tailscale`, `ca`, `route`, `admin`, and `infra` are reserved **Project** names; `default` is created only by tenant creation.
- A **Project** has zero or more **Machines**.
- A **Machine** belongs to exactly one **Project**.
- A **Machine** name is unique within its **Project**.
- **Machine** names are DNS-safe lowercase labels and must not use reserved infrastructure words.
- A **Machine** has one **App Port**, defaulting to `3000`.
- A **Machine Template** is a **Machine** property, not a **Project** property.
- Machine creation defaults to the AI container **Machine Template**.
- Machine creation starts the **Machine** and connects in an interactive terminal unless detached.
- Machine creation authorizes the User's uploaded **User SSH Public Key** for shell access.
- Machine creation waits until the **Machine** has joined the **Tenant Tailnet** and recorded its **Tailscale Machine IP** before reporting success.
- Machine connection uses **Machine SSH Access** over the Machine's **Tailscale Machine IP** as the user-facing shell path.
- Each **Machine** has a **Private Machine Proxy** that serves the machine's private hostname and forwards to its **App Port**.
- A **Machine** private hostname resolves to the Machine's **Tailscale Machine IP**.
- An **Incus Instance Name** is `{project}-{machine}` so two projects in the same tenant can each have a machine with the same name.
- A **Container** is the default **Machine** type.
- A **VM** is a future **Machine** type.
- The user CLI manages **Machines** with `list`, `create`, `connect`, `start`, `stop`, `restart`, `status`, and `delete`.
- **Machine** is the implicit top-level resource in both user and admin CLIs.
- The user CLI manages **Public Routes** separately with `route list`, `route create`, `route status`, and `route delete`.
- The user CLI manages **Projects** with `project list`, `project create`, `project status`, and `project delete`.
- User **Public Route** mutations go through the **Route Broker**.
- All user **Public Route** operations go through the **Route Broker**.
- Users cannot claim the **Auth Hostname** as a **Public Route**.
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
- Shared infrastructure creation resolves input from CLI flags, environment variables, the **Infrastructure Seed File**, and built-in defaults, in that order.
- Machine creation resolves the **Current Project** from an explicit reference, `SANDCASTLE_PROJECT`, local project configuration, or the **Default Project**, in that order.
- Machine lookup commands may search across projects when no project is supplied and no `SANDCASTLE_PROJECT` is set, but only act when the machine name is unique.
- Destructive machine lookup commands require confirmation when the **Project** was inferred, unless the user supplies an explicit confirmation flag.
- A **User** may have **Tenant Access** to one or more **Tenants**.
- A **User** has one **Sandcastle User Key**.
- A **Sandcastle User Key** is the allowlisted **Normalized GitHub Username** in v1.
- A **GitHub Username** rename requires explicit future migration code.
- GitHub is the only external login provider in v1.
- Sandcastle v1 has no password login for the Auth App.
- **GitHub Email** may be stored but is not used for notifications in v1.
- **GitHub Email** is not used for identity, allowlisting, tenant names, or OIDC subject claims.
- Admins manage the **Login Allowlist** by entering **GitHub Usernames**.
- The **Login Allowlist** authorizes by **Normalized GitHub Username** in v1.
- The **Login Allowlist** contains explicit GitHub users only in v1, not GitHub organizations or teams.
- The Auth App verifies a **GitHub Username** with GitHub before adding it to the **Login Allowlist**.
- The **Login Allowlist** stores the numeric GitHub account ID as metadata for audit and future migration.
- A GitHub account rename blocks **GitHub OAuth Login** until a **Sandcastle Admin** performs a migration or allowlist repair.
- Adding a GitHub account to the **Login Allowlist** is enough to provision that user's **Personal Tenant** during CLI Device Login.
- Adding a GitHub account to the **Login Allowlist** does not immediately create a **Personal Tenant**.
- Removing a GitHub account from the **Login Allowlist** blocks new **GitHub OAuth Login** and **CLI Device Login**.
- Removing a GitHub account from the **Login Allowlist** revokes that User's active **Tenant Access** and restricted Incus certificate grants by default.
- Removing a GitHub account from the **Login Allowlist** does not delete the User's **Personal Tenant** or Machines.
- The Auth App creates a **Personal Tenant** lazily during the user's first successful **CLI Device Login**.
- Browser-only **GitHub OAuth Login** creates a web session but does not create a **Personal Tenant**.
- Only a **Sandcastle Admin** may manage the **Login Allowlist**.
- Only a **Sandcastle Admin** may grant or revoke **Tenant Access** through the Auth App.
- The first **Sandcastle Admin** is bootstrapped from deployment configuration.
- Initial **Sandcastle Admins** are bootstrapped from configured **GitHub Usernames**.
- The **Auth App** manages **Tenant Access** by applying the same restricted Incus certificate grants as `sandcastle-admin`.
- A **User** may authenticate to Sandcastle through **GitHub OAuth Login**.
- The **Sandcastle OIDC Provider** issues workload identity tokens for **Machines**, not browser login sessions.
- A **Per-Tenant OIDC Issuer** is scoped to exactly one **Tenant**.
- A **Workload Identity Token** issuer is the **Per-Tenant OIDC Issuer** for the token's **Tenant**.
- A **Workload Identity Token** identifies both the **User** and the **Machine**.
- A **Workload Identity Token** may expose **Tenant**, **Project**, **Machine**, **Sandcastle User Key**, **GitHub Username**, and **Cloud Identity Audience** claims.
- A **Workload Identity Token** does not use the legacy `sandbox` vocabulary.
- A **Workload Identity Token** expires after 15 minutes in v1.
- An **OIDC Signing Key** is stored encrypted in the **Auth Database**.
- An **OIDC Signing Key** belongs to one **Per-Tenant OIDC Issuer**.
- The **Sandcastle OIDC Provider** publishes public signing keys through JWKS.
- The Auth App deployment secret protects encrypted sensitive state and web sessions.
- A **Machine** uses a **Machine Runtime Secret** to request **Workload Identity Tokens**.
- A **Machine Runtime Secret** is rotated when workload identity is enabled, re-enabled, or the **Machine** is rebuilt.
- The **Auth App** stores only a verifier for a **Machine Runtime Secret**, not the raw secret.
- A **User** may define one or more **Cloud Identity Configs**.
- The `sc cloud-identity gcp setup` command configures external GCP trust for a **Tenant** and prints the values stored in a **Cloud Identity Config**.
- The `sc workload enable` command rotates a **Machine Runtime Secret** through the **Auth App** and injects workload identity files into a **Machine** through the tenant-scoped user remote.
- **Cloud Identity Configs** are selected per **Machine** when workload identity is enabled.
- Sandcastle v1 does not apply **Cloud Identity Configs** automatically at the **Tenant** or **Project** level.
- The **Auth App** is implemented as part of the Go Sandcastle codebase.
- The **Auth App** runs as its own infrastructure service, separate from the **Route Broker**.
- The **Auth App** uses minimal server-rendered HTML for its user and admin workflows.
- The **Auth App** is served publicly at the configured **Auth Hostname**.
- The **Auth Hostname** is the issuer host for the **Sandcastle OIDC Provider**.
- The **Auth Hostname** is reserved infrastructure routing, not a user-created **Public Route**.
- The **Auth App** stores login and device authorization state in the **Auth Database**.
- The **Auth Database** lives on persistent infrastructure storage and is scoped to a single Auth App instance in v1.
- **Tenant Metadata** remains the authoritative Sandcastle state for tenant runtime resources, not the Auth App's session or login store.
- **Tenant Metadata** is authoritative for **Tenant Tailnet** configuration.
- **CLI Device Login** returns an **Incus Certificate Add Token**, not a generated client private key.
- During **CLI Device Login**, the CLI generates and stores its own Incus client private key locally.
- During **CLI Device Login**, the CLI uploads a **User SSH Public Key** and keeps the matching private key local.
- **CLI Device Login** prefers the user's existing `id_ed25519.pub`, can use an explicit SSH public key path, and otherwise creates a local **Sandcastle SSH Key**.
- Each successful **CLI Device Login** replaces the **User SSH Public Key** when the uploaded key differs from the stored key.
- **CLI Device Login** reconciles the current **User SSH Public Key** onto existing **Machines** in the User's **Personal Tenant** before reaching **Login Readiness**.
- Users start **CLI Device Login** with `sandcastle login <auth-host>`.
- **CLI Device Login** shows Personal Tenant provisioning progress in the terminal.
- **CLI Device Login** reports provisioning progress through polling status messages in v1.
- The browser authorization page for **CLI Device Login** only confirms authorization and sends the user back to the terminal.
- **CLI Device Login** provisioning is idempotent and safe to retry after partial failure.
- **CLI Device Login** provisions Tailscale-backed **Tenant Infrastructure**, including the **Tenant Tailnet**, server-side and may offer **Local DNS Installation** client-side for hostname convenience.
- **CLI Device Login** reaches **Login Readiness** only when the User can create and connect to a **Machine**.
- **CLI Device Login** ends with a **CLI Login Result** containing the selected User, Current Tenant, Current Project, credential enrollment data, SSH key fingerprint, Tenant Tailnet status, and next command.
- **CLI Device Login** guides the user through joining the relevant **Tenant Tailnet** and verifies the **Tailscale Prerequisite** before reaching **Login Readiness**.
- **CLI Device Login** sets the current tenant only when the User has exactly one accessible **Tenant**.
- First-time **CLI Device Login** defaults the selected **Current Tenant** to the User's **Personal Tenant**.
- **CLI Device Login** joins only the selected **Current Tenant**'s **Tenant Tailnet**, not every accessible tenant tailnet.
- **CLI Device Login** may succeed for a **User** with no **Tenant Access**.
- A **User** with no **Tenant Access** cannot manage Sandcastle resources until a **Sandcastle Admin** grants access.
- A **User** receives **Tenant Access** to their **Personal Tenant** when it is created.
- **Tenant Access** grants access to every **Project** and **Machine** in that **Tenant**.
- **Tenant Access** grants management rights over **Projects**, **Machines**, and **Public Routes** in that **Tenant**.
- Revoking **Tenant Access** revokes **Machine SSH Access** by removing that User's **User SSH Public Key** from Machines in that **Tenant**.
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
- "GitHub username as UID" makes Sandcastle identity mutable; resolved: accepted for v1, with future migration code needed if a GitHub account is renamed.
- "Whitelisted GitHub username" is both admin-facing input and the v1 authorization key; resolved: **Login Allowlist** entries are keyed by **GitHub Username**.
- "Admins create all tenants" is not true for allowlisted users; resolved: the Auth App creates a **Personal Tenant** during first **CLI Device Login** for each allowlisted GitHub account.
- "GitHub OpenID login" conflated GitHub browser authentication with Sandcastle workload identity; resolved: use **GitHub OAuth Login** for browser sign-in and **Sandcastle OIDC Provider** for external cloud trust.
- The older Rails app used `sandbox` in OIDC claims; resolved: Incus Sandcastle **Workload Identity Tokens** use **Tenant**, **Project**, and **Machine** vocabulary.
- "Store everything in Incus metadata" would mix login/session state with tenant runtime metadata; resolved: the **Auth App** uses an **Auth Database**, while **Tenant Metadata** remains the tenant runtime source of truth.
- "Token for Incus auth" could mean a private credential or an enrollment token; resolved: **CLI Device Login** returns an **Incus Certificate Add Token** and never exposes a client private key to the **Auth App**.
- "containers" can mean user-facing runtime environments or the Incus implementation type; resolved: use **Machine** for the product concept and **Container** only for the Incus-backed Machine type.
- "default" could mean CLI shorthand or a real project; resolved: **Default Project** is a real **Project** named `default`.
- "tenant-tld" suggested that the tenant name is a public DNS top-level domain; resolved: use **Tenant DNS Suffix** for the private final hostname label.
- A bare machine name in the CLI could imply a projectless resource; resolved: it means the machine in the **Current Project**, which defaults to the **Default Project**.
- `SANDCASTLE_OWNER` would preserve old terminology; resolved: use `SANDCASTLE_TENANT` only, with no compatibility alias.
- Existing owner/project/sandbox resources and metadata do not require backward compatibility migration.
- Older command words such as `add`, `enter`, and `rm` were considered; resolved: the canonical machine CLI verbs are `list`, `create`, `connect`, `start`, `stop`, `restart`, `status`, and `delete`.
- `inspect` was considered for detailed state; resolved: `status` is the canonical detail command.
- `docs/sandcastle-v1-spec.md` previously described the superseded owner/project model; resolved domain language now lives here, in ADR-0001, and in the rewritten v1 spec.
