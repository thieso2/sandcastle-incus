# Sandcastle — Features & Scenarios

> Extracted from the codebase on 2026-07-01. A map of *what the system does* (features, by subsystem) and *how people use it* (end-to-end scenarios, by actor). Domain vocabulary is canonical in [`CONTEXT.md`](../CONTEXT.md); design rationale lives in [`docs/adr/`](adr/). File references point at the authoritative implementation.

Sandcastle provisions Incus-backed development environments scoped by **tenant** and **project**, with a user CLI (`sandcastle`/`sc`), an admin CLI (`sandcastle-admin`/`sc-adm`), and a browser-facing **Auth App**. The isolation boundary is the tenant (= one Incus project); everything else — DNS, Tailscale, CA, storage, routes — is tenant-scoped infrastructure.

---

## 1. Domain glossary (condensed)

*Citations to `CONTEXT.md`.*

| Term | Meaning |
|---|---|
| **Tenant** | Admin-created top-level namespace owning projects, DNS naming, and access boundaries; the true resource owner (L8–9, L522). |
| **Personal Tenant** | Auto-created tenant scoped to one allowlisted user, named from their GitHub username. |
| **Project** | Named namespace inside a tenant grouping runtime resources; a machine belongs to exactly one (L164–166, L355–356). |
| **Default Project** | The real `default` project present in every tenant from creation; cannot be deleted (L256–258). |
| **Machine** | A tenant/project runtime environment (Incus container/VM) users list/create/connect/delete (L212–214). |
| **Incus Project Mapping** | Each Sandcastle tenant = exactly one Incus project; Sandcastle projects are lightweight namespaces, not Incus projects (L168–169). |
| **Incus Instance Name** | `{project}-{machine}`, so two projects can reuse a machine name (L172–173, L370). |
| **Machine hostname** | `machine.project.tenant` (tenant = Tenant DNS Suffix); exact + per-machine wildcard private records (L404–405). |
| **Tenant DNS Suffix** | Tenant name as the final hostname label; must not be a public TLD / IANA special-use / admin-denied suffix (L16–17). |
| **Tenant Network** | Private bridge shared by all projects and machines in a tenant (L184–185). |
| **Tenant Infrastructure** | The DNS, Tailscale, and CA services shared by all projects in a tenant (L188–189). |
| **Tenant Tailnet** | The Tailscale network dedicated to exactly one tenant (L192–193). |
| **Tenant CA** | Certificate authority for private machine TLS hostnames; trust install is tenant-scoped (L196–197). |
| **Tenant Storage / Share** | Persistent tenant volumes partitioned by project/machine; a Share exposes a source tenant's workspace read-only at `/shared/<source-tenant>/<source-project>/<share-name>` (L200–205, L292). |
| **Public Route** | A public HTTP(S) hostname forwarding to a machine; globally registered with tenant/project/machine target (L248–249). |
| **Route Broker** | Narrow service that authorizes user route requests and mutates global route infra; authenticates via the Sandcastle Incus client cert (L252–253). |
| **Workload Identity (Token)** | Short-lived (15 min) OIDC token identifying User + Machine for external cloud trust, via a per-tenant OIDC issuer (L76–77, L458–464). |
| **Infrastructure Seed File** | Operator bootstrap bundle `~/.config/sandcastle/<deployment>.seed.yml` carrying infra/auth/routing/image config + reusable TLS material (L100–101, L322–327). |
| **Auth Hostname** | Public HTTPS hostname for the Auth App and OIDC issuer; reserved infra routing, not a user route (L108–109). |

---

## 2. Architecture decisions (ADRs)

| ADR | Decision |
|---|---|
| **0001 — Tenant as the Incus Project Boundary** | Each tenant maps to one Incus project; Sandcastle projects are lightweight namespaces inside it. Access/network/DNS/Tailscale/CA/storage all align on the tenant boundary. |
| **0002 — Go Auth App with SQLite** | The Auth App is a single Go service on SQLite (persistent infra storage) — control plane in one runtime, no Rails/Postgres. |
| **0003 — GitHub Username Identity for Personal Tenants** | The normalized GitHub username is the User Key, Personal Tenant name, and DNS suffix (accepting rename risk for friendly hostnames). |
| **0004 — CLI Login Provisions Personal Tenant** | The personal tenant is provisioned idempotently during `sandcastle login`, not at allowlisting, so the CLI can show progress. |
| **0005 — Sandcastle OIDC Provider for Machine Workload Identity** | Sandcastle is an OIDC provider issuing 15-min tokens identifying User+Machine, replacing long-lived cloud creds via federation. |
| **0006 — Tenant Tailnet per Tenant** | Each tenant gets its own tailnet (not one shared tailnet) for per-tenant SSH/DNS/network isolation. |
| **0007 — Infrastructure Network Architecture** | Shared infra runs in a dedicated Incus project with three sidecars (`sc-caddy`, `sc-route-broker`, `sc-auth-app`) on `incusbr0` at deterministic offsets (.20/.21/.22); Caddy publishes host :80/:443 via proxy devices. |
| **0008 — Secret-Bearing Infrastructure Seed Files** | `infra create` bootstraps from a portable YAML seed keyed by Deployment Name that may embed secrets + reusable ACME/TLS material; precedence flag > env > seed > default. |
| **0009 — Tenant Storage Shares use a privileged broker** | Cross-tenant shares are authorized against Tenant Access, then applied by a narrow privileged broker (restricted creds never attach another tenant's storage). |
| **0010 — Image Builder Appliance** | Images are built by an admin-managed appliance (rootless podman in an unprivileged nested container in its own `sc-build` project) and published to GHCR. |

---

## 3. Cross-cutting infrastructure features

- **Per-tenant private bridge + CIDR** — Each tenant gets a `/24`; role addresses derived from the prefix: gateway `.1`, Tailscale `.2`, DNS `.3`. `internal/cidr/roles.go`, `internal/cidr/allocator.go` (`RoleAddress`, `DefaultTenantPrefixBits=24`).
- **CoreDNS per-tenant zone + freeform-launch delegation** — Renders `db.<suffix>` with SOA/NS + static A records for managed machines (exact + wildcard each), with a `file`-plugin `fallthrough` + `forward` delegating unknown names to the bridge's built-in dnsmasq, so freeform `incus launch` instances resolve as `<name>.default.<suffix>`. `internal/dns/render.go` (`RenderTenant`), `internal/dns/apply.go` (`PlanApply`).
- **Per-tenant Tailscale tailnet + subnet router** — A tenant Tailscale sidecar advertises the tenant's private subnet so the laptop reaches machines over the tailnet. `internal/tailscale/up.go`, `internal/meta` (`Tailscale`).
- **Per-tenant CA + leaf certs served by Caddy** — `GenerateCA` (ECDSA P256, 10y); `IssueMachineLeaf` SANs include `<machine>.<domain>` + `*.<machine>.<domain>`; Caddy serves the private hostname over HTTPS and reverse-proxies the App Port. `internal/certs/certs.go`, `internal/caddy/render.go`.
- **Public HTTP routes via the Route Broker** — A cert-authenticated (mTLS) service maps client-cert fingerprint → Principal, scopes routes to the tenant's Incus project, and mutates global route metadata. `internal/routebroker/server.go`, `authorize.go`.
- **Cross-tenant storage shares (read-only)** — Mounted at `/shared/<source-tenant>/<source-project>/<share-name>` with `readonly` disk options, applied via the privileged broker (ADR-0009). `internal/share/share.go`, `internal/meta`.
- **Workload identity / OIDC** — Per-tenant OIDC issuer at the Auth Hostname mints 15-min tokens with tenant/project/machine/user claims; machines authenticate with per-machine runtime secrets (only verifiers stored); signing keys encrypted at rest, public keys via JWKS. ADR-0005, `internal/authapp/oidc.go`, `workload.go`.

---

## 4. Features by subsystem

### 4.1 User CLI (`sandcastle` / `sc`)

Command tree in `internal/cli/root.go`. Global flags `--output text|json` / `--json`. Runs against the per-remote restricted Incus config (`INCUS_CONF`).

- **Device login / sign-in** — Browser OAuth device flow; generates an SSH key, enrolls an Incus remote from the returned token, then auto-runs DNS + trust + Tailscale setup. `sc login <auth-host>` (`--ssh-public-key`, `--skip-setup`, `--tailscale-auth-key`). `internal/cli/login.go`.
- **Machine create** — Creates a machine from a template, optionally injecting workload identity, refreshing DNS/known-hosts, then connecting. `sc create [tenant/][project:]machine` (`--template`, `--app-port`, `--home-dir`, `--workspace-dir`, `--container-tools`, `--cloud-identity`, `--detach`, `--dry-run`). `internal/cli/create.go`.
- **List machines** — Managed + unmanaged machines with FQDN/IP/state. `sc list [project]` (`ls`, `-a/--all-projects`). `internal/cli/list.go`.
- **Status** — Tenant status (DNS suffix, CIDR, routes, Tailscale, share health) or machine status. `sc status [machine|tenant]`. `internal/cli/status.go`.
- **Machine lifecycle** — Start/stop/restart/delete (delete needs `--yes`; invalidates connect cache + refreshes DNS). `sc start|stop|restart|delete …`. `internal/cli/machine_lifecycle.go`.
- **Connect (SSH)** — SSH (or `-- command`) with connect-plan caching, stale-cache retry, auto-start-if-stopped, auto-create-if-missing, optional workload-identity injection, mosh. `sc connect … [-- cmd]` (`c`, `--mosh`, `--cloud-identity`). `internal/cli/connect.go` + `incusx.ConnectCache`.
- **Port set** — Set a machine's proxied app port. `sc port set … <port>`. `internal/cli/port.go`.
- **Password set** — Set the tenant owner's Unix + Samba password across all machines. `sc password set [password]`. `internal/cli/password.go`.
- **Project management** — Create/list/status/delete project namespaces + per-project defaults. `sc project list|create|status|delete --yes|set-cloud-identity|unset-cloud-identity|set-docker-autostart <on|off>`. `internal/cli/project.go`.
- **SSH-key set (tenant)** — Set the tenant's authorized SSH public key (propagates to machines). `sc ssh-key set [key]` (`--file`). `internal/cli/ssh_key.go`.
- **Public HTTP routes** — Create/list/status/delete public routes via the broker (with DNS-proof output on create). `sc route create <hostname> …|list|status|delete`. `internal/cli/route.go`.
- **Tenant DNS + local resolver** — Apply tenant CoreDNS records and install/refresh/uninstall a local resolver (auto-sudo re-exec). `sc dns setup|teardown|apply|status|install|refresh|uninstall`. `internal/cli/dns.go`, `dns_refresh.go`.
- **Local host overrides** — Manage exact `/etc/hosts` entries. `sc host override create … <hostname>|list|delete`. `internal/cli/host.go`.
- **Local tenant CA trust** — Install/uninstall the tenant CA into the OS trust store. `sc trust install|uninstall`. `internal/cli/trust.go`.
- **Tailscale attachment** — Bring the tenant Tailscale sidecar up/down + status. `sc tailscale up|status|down` (`--auth-key`, `--advertise-tag`). `internal/cli/tailscale.go`.
- **Workload identity enable** — Enable OIDC workload identity (e.g. GCP) on a machine, minting token-helper files. `sc workload enable …` (`--cloud-identity`). `internal/cli/workload.go`.
- **Config** — Show/set/unset CLI defaults (`tenant`, `project`, `remote`, `auth.hostname`, `admin_remote`). `sc config show|set|unset`. `internal/cli/config_cmd.go`.
- **Remote add** — Add a Sandcastle remote from an Incus join token. `sc remote add <name> <join-token>` (`--tenant`). `internal/cli/remote.go`.
- **Incus passthrough** — Raw `incus` scoped to a project: `sc incus` (main), `sc incus-native` (freeform), `sc incus-infra` (sidecars). `internal/cli/incus_cmd.go`.
- **Connect cache** — Clear cached SSH connect plans. `sc cache clear`. `internal/cli/cache.go`.
- **Known-hosts (implicit)** — Auto-maintains a per-tenant `known_hosts` on create/connect. `internal/cli/known_hosts.go`.

### 4.2 Admin CLI (`sandcastle-admin` / `sc-adm`)

Command tree in `internal/cli/admin_root.go` (subcommands promoted to top level). Planners under `internal/tenant`, `internal/infra`, `internal/images`, `internal/cidr`; execution in `internal/incusx`.

**Tenant lifecycle**
- **Tenant create** — Provisions a full tenant (projects, network, storage, CA, sidecars, profiles). `sc-adm tenant create <tenant>` (`--ssh-key`, `--tailscale-auth-key`, `--dry-run`). `internal/tenant/create_plan.go`, `internal/incusx/tenant_create.go`.
- **Tenant delete/purge** — Tears down runtime resources, or all durable state with `--purge`. `sc-adm tenant delete <tenant>` (`--yes`, `--purge`). `internal/incusx/tenant_delete.go`.
- **Tenant list / status** — All tenants (or resources in one); metadata/CIDR/DNS/Tailscale checks + live topology + share health. `sc-adm tenant list|status`. `internal/tenant/list.go`, `status.go`, `internal/incusx/topology.go`.
- **Tenant SSH key set** — `sc-adm tenant set-ssh-key <tenant> <key>`.
- **Tenant access grant/revoke/list** — Restricted-user access management. `sc-adm tenant grant|revoke <tenant> <user>`, `tenant users`. `internal/usertrust`.
- **Aux-project recovery** — Recreate infra/native projects + sidecars for an incomplete tenant. `EnsureAuxProjects` (via the Auth App provisioner).

**Machine admin ops (any tenant)**
- **Machine list/create/connect/status/delete** — Full lifecycle across any tenant via `tenant[/project]/machine`. `internal/cli/admin_machine.go`.
- **Machine workload identity** — `sc-adm machine workload enable [project:]machine`.

**Shared infrastructure**
- **Infra create** — Create/update the shared infra project + three sidecars (Caddy, route broker, Auth App); push files/binaries, restore Caddy ACME, start services. `sc-adm infra create` (`--seed`, `--name`, `--username`, `--debug-device-user`, …). `internal/infra/plan.go`, `internal/incusx/infrastructure.go`.
- **Infra delete/purge** — Delete infra project (and with `--purge`, all prefixed tenant projects/pools/volumes). `sc-adm infra delete`.
- **Seed file generation** — `sc-adm infra gen-seed`. `internal/infra/seed.go`.
- **Caddy ACME cert export** — Export working ACME storage for reuse. `sc-adm infra cert export` (`--archive`). `internal/infra/caddy_data.go`.
- **Infra debug-TLS trust** — Install/remove the infra debug CA locally (internal TLS mode). `sc-adm infra trust install|uninstall`.

**Images**
- **Local image build** — `sc-adm image build base|ai`. mise: `image:base:build`, `image:ai:build-upload`.
- **Remote build (appliance)** — Build base/ai/all in the `sc-build` appliance (rootless podman) → GHCR. `sc-adm image build-remote base|ai|all`. mise: `image:all:build-remote`.
- **Appliance lifecycle** — `sc-adm image builder provision|status|destroy`.
- **Image import / sync** — Import an OCI ref and set the base/ai alias (`import`), or re-point an alias (`sync`). `sc-adm image import|sync`.

**Services & other**
- **Route broker serve** / **Auth App serve** — `sc-adm route-broker serve`, `sc-adm auth-app serve`.
- **Restricted user create/delete/token** — `sc-adm user create|delete|token <user>`.
- **TLD deny-list refresh** — `sc-adm tld refresh` (public-TLD / special-use snapshots for DNS validation).

### 4.3 Auth App (`internal/authapp`)

HTTPS service at the Auth Hostname, SQLite-backed (`internal/authapp/app.go`).

- **GitHub OAuth web login + sessions** — `/login/github` → `/oauth/github/callback`; 24h `sandcastle_session` cookie; allowlist enforced at callback. `login.go`, `github.go`, `store.go`.
- **Admin allowlist management** — List/add/remove allowlisted GitHub users (add verifies via GitHub API; remove revokes restricted user + machine SSH keys). `/admin/allowlist`. `allowlist.go`.
- **Admin tenant access management** — Grant/revoke/list a user's tenant access (revoke also revokes machine SSH access). `/admin/access`. `access.go`.
- **CLI Device Login** — Device code + human `user_code` + verification URI; browser approve; poll returns an Incus Certificate Add Token. `/api/device/start|poll`, `/device`. `device.go`.
- **Personal tenant provisioning** — On first approved poll, provisions the personal tenant (projects, Unix user) and mints the cert add token + remote name. `provision.go`.
- **CLI bearer token issuance** — 90-day `cli_auth_token` (stored as SHA-256 verifier) for later CLI API calls. `cli_token.go`.
- **User SSH key registration** — Poll accepts/validates an SSH key and reconciles it into personal-tenant machines + metadata. `store.go`, `device.go`.
- **Sandcastle OIDC Provider** — Global + per-tenant discovery/JWKS (`/.well-known/…`, `/t/{tenant}/…`); RSA RS256 keys auto-generated, AES-GCM-encrypted at rest, per tenant. `oidc.go`.
- **Workload identity enable + token minting** — `/api/workload/enable` generates a Machine Runtime Secret; `/internal/workload/token` verifies it and mints a 15-min RS256 JWT with tenant/project/machine/user claims. `workload_api.go`, `workload.go`.
- **Cloud identity configs (e.g. GCP)** — Store per-tenant workload-identity-federation configs. `/cloud-identities`, `/api/cloud-identities`. `cloud_identity.go`.
- **Machines web UI + tenants API** — Mobile-first `/machines` list with tap-to-connect `ssh://` links; `/api/tenants` for the CLI. `machines_web.go`, `tenants.go`.

---

## 5. End-to-end scenarios

### 5.1 User scenarios

**First-time device login + personal tenant provisioning** — *A new developer signs in and gets a ready-to-use tenant on their laptop.*
1. `sc login big.example.dev` prepares/generates an SSH key, starts the device flow, opens the browser, polls.
2. The user approves in the browser (`/device`, allowlisted session required); the Auth App provisions the personal tenant + mints an Incus Certificate Add Token + 90-day CLI token, and stores the SSH key.
3. The CLI enrolls the Incus remote from the token, sets the default tenant, then `RunPostLoginSetup` applies tenant DNS, installs the local resolver (sudo-elevating if needed), installs the tenant CA into local trust, and brings up Tailscale.
4. Result: enrolled remote, resolving DNS, trusted CA, tailnet-connected. `internal/cli/login.go`, `internal/authapp/{device,provision}.go`.

**Create & connect to a machine** — *Developer spins up a dev box and drops into a shell.*
1. `sc create webapp --template ai --app-port 3000` → `machine.PlanCreate` + `CreateMachine`; ensures the tenant Unix user matches the local user; refreshes DNS + known_hosts; then SSHes in.
2. `sc connect webapp` reuses the cached connect plan, probes :22, auto-starts if stopped, retries on stale cache, auto-creates if missing. `-- cmd` runs one-off; `--mosh` switches transport. `internal/cli/{create,connect}.go`.

**Expose a public HTTP route** — *Developer publishes a machine's app on a public hostname.*
1. `sc route create app.example.dev webapp` → `route.PlanCreate`; prints CNAME/target for DNS proof if required.
2. The plan goes to the Route Broker (mTLS), which wires the hostname → machine app port.
3. `sc route list|status` confirm; `sc route delete` tears down. `internal/cli/route.go`, `internal/routebroker`.

**Local DNS so machine hostnames resolve on the laptop** — 
1. `sc dns setup` applies tenant CoreDNS records + installs/refreshes the local resolver (auto-`sudo` re-exec for the privileged edit).
2. `sc dns refresh` re-syncs after adding machines; `sc dns teardown` removes it. (Runs automatically during `sc login`.) `internal/cli/dns.go`.

**Create a project and place machines in it** — 
1. `sc project create backend` writes the namespace into tenant metadata.
2. `sc project set-cloud-identity backend gcp` / `set-docker-autostart backend on` set project defaults.
3. `sc create backend:api` creates a machine in the project (defaults flow into create/connect); `sc list backend`; `sc project delete backend --yes` removes the empty namespace. `internal/cli/project.go`.

**Manage SSH keys across machines** — 
1. `sc ssh-key set --file ~/.ssh/id_ed25519.pub` stores the authorized key in tenant metadata; machines pick it up.
2. New `sc create` provisions with the key; `sc connect` uses the local private key + auto-maintained per-tenant `known_hosts`.
3. `sc password set` additionally sets the Unix + Samba password across all machines. `internal/cli/{ssh_key,password}.go`.

### 5.2 Admin / operator scenarios

**Bootstrap a new deployment from a seed file** — 
1. `sc-adm infra create --name <deployment>` resolves `~/.config/sandcastle/<deployment>.seed.yml` (synthesizing + saving one if absent); config resolves as `defaults ⊕ seed ⊕ env`.
2. Ensures base + AI images exist on the remote (build+upload unless already present).
3. `infra.PlanCreate` builds the infra Incus project + three sidecars (Caddy `sc-inf-caddy`, `sc-route-broker`, `sc-auth-app`) on `incusbr0`, runtime files/units, and the `sandcastle-admin` binary; Caddy gets :80/:443 proxy devices; broker/auth-app get the host Incus socket + `security.privileged`.
4. `CreateInfrastructure` derives static addresses (.20/.21/.22), launches instances, pushes files/binaries, restores Caddy ACME data if seeded, starts services.
5. `internal` TLS mode loads/creates a persistent debug CA and installs it locally; `acme` mode exports the working ACME storage back into the seed so re-creates skip re-issuance. `internal/infra`, `internal/incusx/infrastructure.go`.

**Create a tenant (what infra gets created)** — 
1. `sc-adm tenant create <ref>`; CLI computes occupied CIDRs.
2. `PlanCreate` validates the ref + DNS suffix (allow/deny policy), allocates a `/24`, derives gateway/.2/.3 addresses, renders initial CoreDNS zone, generates the tenant CA, builds metadata.
3. `CreateTenant` idempotently ensures: three Incus projects (main / `-infra` / `-native`); a per-tenant storage pool; project images copied from admin aliases; the private bridge (`dns.domain=<default-project>.<suffix>`, NAT); durable volumes `sc-home`/`sc-workspace`/`sc-ca`; the tenant CA in `sc-ca` (never rotated on re-run); `container` + `default` profiles (root disk + `eth0`) in main and native; two infra sidecars (Tailscale + CoreDNS `sc-dns`) pinned to static addresses; CoreDNS config written + restarted.
4. If a Tailscale auth key is configured, `tailscale up` runs; caches invalidated. `internal/incusx/tenant_create.go`.

**Build & publish the base/AI images (remote)** — 
1. `sc-adm image build-remote all` (mise `image:all:build-remote` resolves latest npm versions) → `[base, ai]`.
2. `PlanRemoteBuild` derives version/dirty, computes `ghcr.io/<owner>/sandcastle-<tpl>:<version>` + `:latest`, and pins AI's `FROM sandcastle-base:<same version>`.
3. Ensures the `sc-build` appliance (Debian, rootless podman, cache volume); logs into GHCR from a tmpfs token file; `podman build` against the warm cache; pushes both tags.
4. Unless `--no-import`, copies the published tag back into the host Incus alias. `internal/images/remote.go`.

**Import an image into a tenant** — 
1. `sc-adm image import base|ai <src>` → `incus image copy <src> <remote>: --alias <alias> --copy-aliases --reuse`.
2. `sc-adm image sync <ref>` re-points an alias; mise `image:tenant:sync` propagates into image-enabled tenant projects.
3. On the next `tenant create`/`EnsureAuxProjects`, `ensureProjectImages` copies the aliased image into the tenant's main/infra projects. `internal/images/plan.go`.

**Delete / purge a tenant** — 
1. `sc-adm tenant delete <ref> [--purge]` (prompts without `--yes`); first cleans up outbound/inbound storage shares + reconciles recipients.
2. `PlanDelete` computes projects/network/pool/volumes/sidecars.
3. `DeleteTenant` purges the infra + native projects, deletes main-project instances + non-default profiles, clears the private NIC from the default profile, deletes the network.
4. `--purge` also deletes `sc-home`/`sc-workspace`/`sc-ca`, project images, the main project, and the per-tenant pool. `internal/incusx/tenant_delete.go`.

**Recover / recreate shared infrastructure** — 
1. Re-run `sc-adm infra create` — every step is idempotent (repairs files/binaries/stopped services); seeded ACME / persistent internal CA is reapplied (no cert churn).
2. mise `infra:redeploy` pushes a new admin binary into broker + auth-app and restarts them.
3. `sc-adm infra delete --purge` for a clean-slate rebuild.
4. `EnsureAuxProjects` recreates a half-provisioned tenant's infra/native projects, network, profiles, images, and sidecars from the stored reference + CIDR. `internal/incusx/{infrastructure,tenant_create}.go`.

### 5.3 Machine / workload scenarios

**New user registers via GitHub OAuth and is allowlisted** — 
1. An admin adds the GitHub username at `/admin/allowlist` (verified against the GitHub API).
2. The user signs in via `/login/github` → `/oauth/github/callback`; a non-allowlisted user is rejected (403). The allowlisted user gets a 24h session and the onboarding page (with the CLI login command). `internal/authapp/{allowlist,login,github}.go`.

**Machine obtains a short-lived OIDC workload identity token to access GCP** — 
1. (Setup) `POST /api/cloud-identities` stores the GCP config (audience, subject token type, SA impersonation URL).
2. `POST /api/workload/enable` (bearer/device auth) ensures a per-tenant OIDC signing key, generates a Machine Runtime Secret (only the SHA-256 verifier is stored), and returns the token endpoint + issuer + TTL.
3. On the machine, `POST /internal/workload/token` verifies the runtime secret and mints a 15-min RS256 JWT (`iss`=tenant issuer, `sub`=`machine:<tenant>/<project>/<machine>`, `aud`=requested audience, plus tenant/project/machine/user claims).
4. GCP Workload Identity Federation validates the JWT against the tenant's OIDC discovery/JWKS and impersonates the service account — the machine gets short-lived credentials with no static key. `internal/authapp/{workload_api,workload,oidc,cloud_identity}.go`.

---

## Appendix — notes & drift

- **Project settings drift vs ADR-0001.** ADR-0001 says projects have "no v1 settings beyond their names," but `meta.Project` now carries `CloudIdentity` and `DockerAutostart` defaults (`internal/meta/meta.go`), surfaced via `sc project set-cloud-identity` / `set-docker-autostart`. The ADR is stale on this point.
- **Two sidecar layers.** *Shared* infra sidecars (ADR-0007): `sc-caddy`/`sc-route-broker`/`sc-auth-app` on `incusbr0`. *Per-tenant* sidecars: Tailscale + CoreDNS (`sc-dns`) on the tenant bridge. Don't conflate them.
- **Freeform vs managed DNS.** Managed machines get static CoreDNS records `<machine>.<project>.<tenant>`; freeform `sc incus launch` instances get `<name>.default.<tenant>` via the bridge dnsmasq + CoreDNS fallthrough (see §3; `internal/dns/render.go`, `internal/incusx/tenant_create.go`).
</content>
</invoke>
