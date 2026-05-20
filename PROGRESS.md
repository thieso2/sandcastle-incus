# Progress

Tracking implementation of GitHub issue #1: Sandcastle v1 Incus-backed
development sandboxes.

## Current Status

- Issue #1 PRD has been reviewed.
- Planning docs exist for the v1 specification, implementation milestones, and
  e2e testing phases.
- The Tailscale automation clarification from issue #1 is reflected in the
  spec, implementation plan, e2e plan, and `.env.default`: unattended
  `tailscale up` must advertise `tag:sandcastle` by default alongside the
  project private CIDR.
- Agent repo guidance exists in `AGENTS.md`, `CLAUDE.md`, and `docs/agents/`.
- Initial Go module exists at `github.com/thieso2/sandcastle-incus`.
- Cobra CLI skeleton exists for `sandcastle` and `sc`.
- Implemented initial commands:
  - `sandcastle version`
  - `sandcastle ls`
  - `sandcastle admin version`
- Implemented global `--output text|json`.
- Added e2e config loader/gate that refuses destructive e2e unless
  `SANDCASTLE_E2E=1` and defaults `SANDCASTLE_E2E_TAILSCALE_TAG` to
  `tag:sandcastle`.
- Added `internal/naming` for owner/project references, Incus project names, and
  reserved sandbox names.
- Added `internal/meta` for Sandcastle scalar metadata keys, versioned project
  and sandbox state blobs, and unmanaged-resource detection.
- Added `internal/cidr` for deterministic IPv4 project CIDR allocation,
  occupied/live-network overlap avoidance, exhaustion reporting, and role
  address derivation.
- Added `internal/project` read-only listing over an Incus project store
  abstraction.
- Wired `sandcastle ls` to the project listing service, with current default
  behavior returning an empty list until the real Incus adapter is added.
- Added `internal/config` admin defaults/env loading for remote, storage pool,
  CIDR pool, project prefix, infrastructure project, and base/AI image aliases.
- Extended `.env.default` with the admin/default CLI configuration variables.
- Added `sandcastle admin project list` / `ls` command shape backed by the same
  project listing service as `sandcastle ls`.
- Added `internal/incusx` Incus SDK adapter for project metadata listing via
  the existing Incus CLI config and `GetProjects()`.
- Wired production `sandcastle`/`sc` execution to use the Incus-backed project
  store by default, while unit tests still inject fake stores.
- Added admin project creation planning that derives the Incus project name,
  private CIDR, `sc-private` network, durable volume names, DNS/Tailscale
  sidecar names and addresses, default template, and project metadata config.
- Added `sandcastle admin project create <owner/project> --domain ... --dry-run`
  to render the creation plan in text or JSON. Non-dry-run execution still
  fails explicitly until the Incus executor is implemented.
- Added gated real-Incus e2e smoke test for project listing:
  `TestIncusProjectListingSmoke` skips unless `SANDCASTLE_E2E=1`.
- Added Incus project creation executor for idempotent project metadata
  creation/update, `sc-private` bridge creation, and durable custom volume
  creation for home, workspace, and project CA state.
- `sandcastle admin project create` now executes through the Incus executor
  when not run with `--dry-run`; dry-run remains offline and renders the plan
  without connecting to Incus.
- Added explicit sidecar plans for `sc-tailscale` and `sc-dns`, including
  stable private addresses, base image alias, root disk, bridged NIC, metadata,
  and start intent.
- Extended the Incus project create executor to create missing sidecar
  containers and start stopped existing sidecars.
- Added `internal/dns` initial CoreDNS renderer for a minimal authoritative
  project zone with NS/SOA records and no project-wide wildcard.
- Project create plans now include rendered CoreDNS files, and the Incus
  executor writes them into the DNS sidecar under `/etc/coredns`.
- The base image now installs a pinned CoreDNS release, and project creation
  plus `sandcastle dns apply` restart CoreDNS inside `sc-dns` after writing the
  rendered Corefile and zone files.
- Added project delete planning and `sandcastle admin project delete` command
  shape with required `--yes` confirmation and optional `--purge`.
- Added Incus delete executor that stops/removes sidecars, removes the private
  network, and only deletes durable volumes plus the Incus project when purge is
  explicit.
- Extended e2e config with `SANDCASTLE_E2E_DOMAIN_SUFFIX` and safe disposable
  run id handling.
- Added gated destructive e2e `TestDisposableProjectCreateAndPurge`, which
  creates a disposable Sandcastle project, verifies it is listed, and purges it
  unless `SANDCASTLE_E2E_KEEP=1`.
- Added e2e diagnostics logging for failed disposable project runs, filtered to
  Sandcastle project summaries matching the run id.
- Extended disposable project failure diagnostics with live topology checks for
  the private network, durable volumes, and DNS/Tailscale sidecar status when an
  Incus topology store is available.
- Reused the same project topology check helper for status output and e2e
  failure diagnostics so those surfaces stay aligned.
- Added `sandcastle status <owner/project>` and
  `sandcastle admin project status <owner/project>`, backed by managed project
  metadata and basic metadata/domain/CIDR checks.
- Extended status with optional live topology checks for `sc-private`, durable
  home/workspace/CA volumes, and `sc-tailscale`/`sc-dns` sidecar presence and
  running state.
- Added `internal/incusx` topology adapter for live Incus network, volume, and
  sidecar reads.
- Added `internal/usertrust` planning for restricted Incus user certificates,
  project grants, and token requests.
- Added dry-run command shape for `sandcastle admin user create`, `grant`, and
  `token`.
- Added Incus trust executor for restricted certificate project grants and
  certificate add token creation.
- Wired non-dry-run `sandcastle admin user create`, `grant`, and `token` to the
  Incus trust executor.
- Added sandbox create planning for `sandcastle add owner/project/name`,
  including project lookup, default app port 3000, first private container IP,
  sandbox metadata, AI image alias, and home/workspace/network/root devices.
- `sandcastle add` now supports per-sandbox `--template ai|base`, defaults to
  the project's default template, and records the selected template plus image
  alias in the sandbox creation plan.
- Sandbox storage defaults now follow the v1 spec by mounting `"."` from the
  project home/workspace volumes unless `--home-dir` or `--workspace-dir` is
  provided.
- `sandcastle add` now requires `--share-home` before reusing a home
  subdirectory from another running sandbox, while workspace subdirectories
  remain freely shareable.
- Sandbox create planning now lists existing sandbox metadata and allocates the
  first free private IP in the sandbox range, preserving an existing sandbox's
  IP for idempotent replans instead of reusing `.20` for every sandbox.
- Added `sandcastle add ... --dry-run` command shape.
- Added Incus sandbox create executor that creates planned container sandboxes
  and starts stopped existing sandbox instances.
- Wired non-dry-run `sandcastle add` to the sandbox executor.
- Added sandbox lifecycle planning and commands for `start`, `stop`, `restart`,
  and `rm`.
- Added Incus sandbox lifecycle executor for start/stop/restart/remove, with
  `rm` requiring `--yes` and ignoring already-missing instances.
- Added `sandcastle port set owner/project/name <port>` planning and executor
  to update sandbox app-port scalar metadata and the versioned sandbox state
  blob.
- Added DNS rendering from sandbox metadata: exact sandbox records and
  per-sandbox wildcard records, with no project-wide wildcard.
- Added `sandcastle dns status` and `sandcastle dns apply`; apply reads
  sandbox metadata from Incus instances and writes CoreDNS files to `sc-dns`.
- Added certificate foundation for project CA generation and sandbox leaf
  certificate issuance with exact sandbox SAN, per-sandbox wildcard SAN, and
  extra SANs for future host overrides.
- Project creation now generates project CA material and writes `ca.crt` plus
  `ca.key` into the project CA custom volume. Dry-run output shows only CA file
  paths, not PEM contents.
- Sandbox creation plans now include private Caddy config for the sandbox
  hostname and can issue sandbox leaf certificate/key files when project CA
  material is supplied. The Incus sandbox executor writes the Caddyfile and TLS
  files into the sandbox instance with overwrite semantics.
- Sandbox creation, `sandcastle port set`, and host override rewrites now
  start or reload Caddy inside the sandbox after writing Caddy/TLS files, so the
  private HTTPS proxy is live instead of only configured on disk.
- Non-dry-run sandbox creation now records the project CA volume in the plan and
  reads `ca.crt`/`ca.key` from that volume when certificate files are not
  already present, so real `sandcastle add` can issue sandbox certs from stored
  project CA material.
- `sandcastle port set` plans a matching Caddyfile and the Incus executor
  overwrites the sandbox Caddyfile after metadata updates, keeping the private
  HTTPS reverse proxy aligned with the configured app port.
- Added `sandcastle enter owner/project/name` planning and Incus interactive
  exec support for `/bin/bash -l` in `/workspace`. Non-dry-run `sandcastle add`
  now enters the sandbox by default after creation and supports `--detach` for
  automation/background creation.
- `sandcastle enter owner/project/name -- command...` now supports explicit
  non-interactive command execution in `/workspace`, while the default no-command
  form remains an interactive login shell.
- Added `sandcastle tailscale up owner/project` planning, dry-run output, and
  Incus execution inside the `sc-tailscale` sidecar. Plans advertise the project
  private CIDR, default to `tag:sandcastle`, support unattended `--auth-key`,
  and redact auth keys from JSON/text dry-run output.
- Added `sandcastle tailscale status owner/project` and
  `sandcastle tailscale down owner/project`. Status executes
  `tailscale status --json` in the sidecar, parses observed tailnet, hostname,
  advertised routes, and Tailscale IPs, then writes that state into project
  metadata. Down executes `tailscale down` and records stopped state.
- Project status now includes a `tailscale:route` check derived from recorded
  Tailscale metadata, reporting whether the project private CIDR is advertised.
- Added host override planning for
  `sandcastle host override add owner/project/name hostname --dry-run`.
  Planning validates exact non-wildcard hostnames, looks up sandbox metadata,
  renders managed `/etc/hosts` entry markers, records the extra SAN intent, and
  warns that the project CA must be trusted for HTTPS.
- Host override apply now updates sandbox `extraSANs` metadata, reads project
  CA material, reissues sandbox TLS files with the override hostname, and
  refreshes sandbox Caddy to serve both the project hostname and override
  hostname. Local `/etc/hosts` mutation remains explicit follow-up work.
- Host override add now has a managed hosts-file editor that writes/replaces a
  Sandcastle-marked block and is wired after the sandbox ingress update.
  Production defaults to `/etc/hosts`; tests and automation can override with
  `SANDCASTLE_HOSTS_FILE`.
- Added host override list/remove support. List reads sandbox `extraSANs`
  metadata, while remove deletes the hostname from sandbox metadata, reissues
  sandbox TLS/Caddy without that SAN, and removes the managed hosts-file block.
- Added gated host override e2e coverage. `TestHostOverrideE2E` creates a
  disposable project and sandbox, redirects hosts-file mutation to a temp file,
  adds an exact override, verifies the managed hosts entry, sandbox Caddy
  routing, certificate SAN, list output, removal, and post-removal Caddy/cert
  cleanup.
- Added local project CA trust planning and `sandcastle trust install` /
  `sandcastle trust uninstall`. Plans resolve the managed project, CA volume,
  CA certificate path, platform, trust name, and explicit trust warning.
- Added Incus-backed local trust execution that reads the project CA
  certificate from the project CA volume and installs/removes it through a
  platform trust store. Production supports macOS Keychain and Linux
  `update-ca-certificates`; tests and disposable automation can use
  `SANDCASTLE_TRUST_DIR` for file-backed trust state.
- Added gated local trust e2e coverage. `TestLocalTrustInstallUninstallE2E`
  creates a disposable Incus project, reads its real project CA through the
  Incus-backed trust manager, installs it into a file-backed trust store, then
  uninstalls it and verifies the trust file is removed.
- Added local DNS install/refresh/uninstall planning and
  `sandcastle dns install`, `sandcastle dns refresh`, and
  `sandcastle dns uninstall`. Plans resolve the managed project domain, project
  DNS endpoint, stable loopback forwarder address, local state file, and
  resolver file path.
- Added file-backed local DNS state management. Install/refresh upsert the
  project into CLI-managed YAML forwarder state and write resolver files that
  point at `127.0.0.1:53541`; uninstall removes the project state and resolver
  file. Tests can redirect state with `SANDCASTLE_LOCAL_DNS_STATE` and
  `SANDCASTLE_RESOLVER_DIR`.
- Added a local UDP DNS forwarder and `sandcastle dns forwarder`. The
  forwarder reads CLI-managed YAML state, selects upstream project DNS by
  query-name domain suffix, proxies UDP DNS packets to the recorded endpoint,
  and reloads state on each query so `dns refresh` affects routing without live
  Incus lookups per DNS query.
- Added explicit local DNS resolver strategy planning. macOS uses resolver
  files, Linux plans systemd-resolved `resolvectl dns` and `resolvectl domain`
  commands for loopback project-domain routing, and unsupported platforms fall
  back to file-only state.
- Added local DNS forwarder service wrappers:
  `sandcastle dns service install`, `reload`, and `uninstall`. The wrappers
  plan/write a user launchd plist on macOS or a user systemd unit on Linux for
  `sandcastle dns forwarder`, support dry-run JSON/text output, and expose
  `SANDCASTLE_LOCAL_DNS_SERVICE_DIR` plus `SANDCASTLE_BIN` for test/disposable
  VM redirection.
- Added gated local DNS forwarder e2e coverage. The test installs disposable
  local DNS state and resolver files through the public manager, starts the UDP
  forwarder on loopback, verifies a sandbox hostname query reaches the
  configured upstream, refreshes state to a second upstream and verifies live
  reload behavior, then uninstalls and verifies resolver cleanup.
- Added gated real-Incus project DNS e2e coverage. `TestProjectDNSE2E` syncs
  disposable base/AI aliases, creates a disposable project plus two sandboxes
  with distinct private IPs, applies DNS, then queries `sc-dns` from inside the
  sandbox network to verify exact sandbox records, a per-sandbox wildcard
  record, and absence of a project-wide wildcard.
- Strengthened project DNS e2e coverage for sandbox removal. The same e2e now
  removes one sandbox, reapplies DNS from Incus metadata, and verifies the
  removed sandbox's record no longer resolves.
- Added `scripts/e2e.sh`, a tiered e2e runner for reproducible local, gated,
  real-Incus, Tailscale, and image-build e2e runs. Destructive tiers fail closed
  unless `SANDCASTLE_E2E=1` is set, while `unit`, `gated`, and `local` provide
  safe local verification entry points.
- Added Tailscale sidecar runtime prerequisites: the base image installs the
  Tailscale Debian 13 package from Tailscale's stable apt repository, prepares
  Tailscale state/runtime directories, and writes forwarding sysctl defaults.
  Project sidecar planning now attaches `/dev/net/tun` only to `sc-tailscale`.
- Base and AI images now default to `sleep infinity` so sidecars and sandboxes
  stay running for Incus exec workflows instead of exiting from a noninteractive
  login shell.
- `sandcastle tailscale up` now bootstraps `tailscaled` inside the sidecar
  before running `tailscale up`, so the flow does not depend on systemd being
  present in OCI-imported containers.
- Added gated real-tailnet e2e coverage for Tailscale attachment. The
  `tailscale` runner tier creates a disposable project with a disposable base
  image alias, runs `tailscale up` with `SANDCASTLE_E2E_TAILSCALE_AUTHKEY`,
  polls status until connected, asserts the project CIDR is advertised, and
  runs `tailscale down` during cleanup.
- Extended gated real-tailnet e2e coverage to the full private access path.
  `TestTailscaleAttachmentE2E` now syncs disposable base and AI image aliases,
  creates a disposable sandbox, starts a sandbox-local HTTP app, applies
  CoreDNS, runs `tailscale up`, then verifies from the test runner that the
  routed private CIDR reaches project DNS and sandbox HTTPS Caddy.
- Added public route planning and command shape for `sandcastle route add`,
  `sandcastle route list`, and `sandcastle route rm`. Route add validates exact
  public hostnames, resolves the target sandbox metadata, pins the current
  sandbox app port into the route plan, records the target IP, and marks the
  route broker plus public DNS proof as required before mutation.
- Added route metadata modeling and infrastructure Caddy rendering. Route plans
  now include the metadata config that the route broker/infrastructure executor
  must persist globally, and Caddy can render deterministic public reverse proxy
  routes from stored route metadata.
- Added Incus-backed route metadata execution. Public routes are stored as
  Sandcastle-managed profiles in the infrastructure project, giving global
  hostname uniqueness through the infrastructure project namespace. Route add
  creates/updates route profiles, route list reads managed route profiles, and
  route remove deletes the matching profile.
- Route metadata changes now refresh the infrastructure Caddyfile. After route
  add/update/remove, the Incus route manager renders all managed route profiles
  into deterministic infrastructure Caddy reverse-proxy config and writes it to
  the `sc-caddy` instance.
- Infrastructure Caddy refresh now reloads the running Caddy process after the
  Caddyfile is written by execing `caddy reload --config /etc/caddy/Caddyfile`
  in the `sc-caddy` instance.
- Route add now ensures the target sandbox has a Sandcastle-managed route
  ingress NIC device before route metadata is stored and infrastructure Caddy
  is refreshed. The device is named from the route hostname and tagged with
  Sandcastle route metadata for later cleanup and auditing.
- Route removal now reads the stored route metadata before deleting it and
  removes the matching Sandcastle-managed route ingress NIC from the target
  sandbox, then refreshes and reloads infrastructure Caddy.
- Route add now carries and enforces public DNS proof. `SANDCASTLE_INFRA_HOST`
  configures the expected infrastructure DNS target, route plans include that
  proof requirement, and the Incus route manager resolves the public hostname
  before mutating sandbox ingress, route metadata, or infrastructure Caddy.
- Added initial route broker authorization rules. The route broker package can
  map an mTLS client certificate fingerprint to a Sandcastle owner and enforce
  that route add/remove operations only target routes owned by that principal.
- Added the route broker HTTP/mTLS serving path for authorized route mutations.
  The broker extracts the client certificate fingerprint from the mTLS
  connection, maps it to a Sandcastle owner, authorizes add/remove requests,
  plans route mutations, and delegates to the route manager.
- Broker-authorized route adds now stamp the authenticated Sandcastle owner
  into route metadata as `createdBy`, so globally stored route records preserve
  the principal that created them.
- Route metadata now also exposes `createdBy` as a scalar Incus metadata key,
  keeping route creator identity searchable without decoding the versioned JSON
  state blob.
- Added a route broker HTTP mTLS client for user route mutations.
  `SANDCASTLE_ROUTE_BROKER_URL`, `SANDCASTLE_ROUTE_BROKER_CLIENT_CERT`, and
  `SANDCASTLE_ROUTE_BROKER_CLIENT_KEY` switch `sandcastle route list/add/rm`
  from direct infrastructure mutation to broker GET/POST/DELETE requests.
- Route broker list requests now require mTLS, delegate to the route manager,
  and filter returned routes to the principal's owner prefix before responding.
- Route broker DELETE now decodes the escaped route hostname path segment before
  authorization/removal, and the broker client reports JSON error payloads as
  plain actionable messages.
- Added route broker HTTPRunner mTLS integration coverage. The test starts a
  real TLS listener, presents a client certificate from an `http.Client`, and
  verifies an authorized route add flows through TLS client-certificate
  extraction, trust mapping, authorization, and route manager delegation.
- Strengthened route broker mTLS integration coverage to use the production
  `routebroker.Client` over the live TLS listener for route add and list,
  covering the same client path used by `SANDCASTLE_ROUTE_BROKER_URL`.
- Added an Incus-backed route broker trust mapper. Broker mTLS certificate
  fingerprints can now be resolved against Incus trust state, accepting only
  Sandcastle restricted user certificates named `sandcastle-<owner>`.
- Added route broker process wiring. `sandcastle admin route-broker serve`
  now plans and starts the broker HTTPS API with mandatory mTLS client
  certificates, production dependencies are wired to Incus-backed project,
  sandbox, route, route metadata, and trust stores, and the Incus route manager
  can resolve route metadata for broker-authorized deletions.
- Added shared infrastructure creation planning and execution. `sandcastle
  admin infra create` can now create/update the configured infrastructure
  project and ensure the `sc-caddy` and `sc-route-broker` runtime containers are
  present and running.
- Infrastructure creation now writes bootstrap runtime files. `sc-caddy`
  receives a valid empty-route Caddyfile, and `sc-route-broker` receives a
  Sandcastle env file plus broker TLS material for running the mTLS broker.
- The route broker env file now includes the effective Sandcastle admin config,
  so disposable infrastructure brokers use the configured remote, storage pool,
  project prefix, infrastructure project, infrastructure DNS target, and image
  aliases instead of process defaults.
- Added opt-in `SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET` infrastructure wiring.
  When set, only `sc-route-broker` receives a disk mount of the host Incus Unix
  socket at `/var/lib/incus/unix.socket`, giving real broker-runtime route
  mutation e2e a concrete local-Incus access path without changing defaults.
- Extended disposable infrastructure e2e with an optional trusted broker probe
  when `SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET` is configured. The test creates a
  disposable Sandcastle restricted user certificate, calls the containerized
  route broker over mTLS, and expects `GET /routes` to return `200`, proving the
  runtime broker can map mTLS identity through Incus and read route state.
- Added gated broker-authorized mutation e2e coverage.
  `TestRouteBrokerAuthorizedMutationE2E` runs when real e2e, broker socket
  mounting, and base/AI image sources are configured. It creates disposable
  infrastructure, a project, a sandbox, a trusted mTLS client certificate, and a
  broker-local DNS proof, then adds, lists, removes, and verifies cleanup of a
  public route through the running broker process.
- Route add now rejects already-claimed public hostnames before DNS proof or
  target sandbox ingress mutation. Existing managed route profiles report the
  current owner/project/sandbox claimant, and unmanaged infrastructure profile
  name conflicts fail closed instead of being overwritten.
- Route broker route mutations now preserve public hostname conflicts as HTTP
  `409 Conflict` responses instead of reporting them as generic upstream
  failures.
- The route broker HTTP client now maps broker `409 Conflict` responses back to
  the typed route conflict error, so CLI and automation callers can distinguish
  claimed-hostname conflicts without string matching.
- Route broker mutation e2e now also creates a second disposable owner/project
  and verifies the trusted first-owner broker client receives `403 Forbidden`
  when attempting to add a public route to that unowned sandbox, with no route
  profile or ingress device created.
- Route broker mutation e2e now verifies public route port pinning: after a
  broker-created route is present in infrastructure Caddy, the test changes the
  sandbox app port and asserts the public Caddy route still proxies to the
  original stored route port before removing the route.
- Route broker mutation e2e now also reads the infrastructure Caddyfile after
  route removal and rejected unowned route attempts, verifying removed or
  unauthorized public hostnames are absent from rendered Caddy config.
- Added an explicit `scripts/e2e.sh public-routes` tier. It fails closed unless
  real e2e is enabled and the broker socket, disposable image sources,
  delegated public route domain, infrastructure DNS proof target, and Let's
  Encrypt contact email are configured.
- Route broker mutation e2e now consumes public route e2e settings when present,
  using `SANDCASTLE_E2E_PUBLIC_DOMAIN` for generated route hostnames,
  `SANDCASTLE_E2E_INFRA_HOST` for DNS proof, and
  `SANDCASTLE_E2E_LETSENCRYPT_EMAIL` for infrastructure Caddy.
- Infrastructure Caddy now accepts optional Let’s Encrypt contact email via
  `SANDCASTLE_LETSENCRYPT_EMAIL`. Infrastructure bootstrap Caddyfiles, route
  refreshes, and the route broker runtime env preserve that setting so public
  route TLS configuration is explicit and testable.
- The e2e harness now reads public route settings
  `SANDCASTLE_E2E_PUBLIC_DOMAIN`, `SANDCASTLE_E2E_INFRA_HOST`, and
  `SANDCASTLE_E2E_LETSENCRYPT_EMAIL` for the future delegated-domain public
  route gate.
- Added an explicit `scripts/e2e.sh local-vm` tier for local resolver, trust,
  and hosts mutation coverage. It fails closed unless `SANDCASTLE_E2E=1` and
  `SANDCASTLE_E2E_LOCAL_VM=1` are set, keeping privileged workstation-style e2e
  scoped to disposable VM runs.
- The e2e harness now exposes `SANDCASTLE_E2E_LOCAL_VM` as `Config.LocalVM`, so
  future OS-level local DNS/trust/hosts tests can share the same safety gate.
- Added disposable-VM local DNS service e2e coverage. `TestLocalDNSServiceInstallReloadUninstallE2E`
  builds or uses a real Sandcastle binary, writes disposable DNS state, installs
  the platform user service through the real `FileServiceManager`, verifies the
  service forwards DNS packets, reloads it after state refresh, and verifies
  uninstall removes the service file and stops responses.
- Extended the `local-vm` runner tier to include all `TestLocalDNS.*E2E`
  coverage so the resolver file flow and platform service flow run under the
  same explicit disposable-VM gate.
- Added disposable-VM platform trust e2e coverage.
  `TestLocalTrustPlatformInstallUninstallE2E` creates a disposable Incus
  project, reads its real project CA, installs it through the platform trust
  backend, verifies the Linux trust file or macOS Keychain entry exists, then
  uninstalls it and verifies removal. The test skips unless both
  `SANDCASTLE_E2E=1` and `SANDCASTLE_E2E_LOCAL_VM=1` are set.
- Extended the `local-vm` runner tier to include all `TestLocalTrust.*E2E`
  coverage so file-backed and platform-backed local trust paths share the same
  explicit disposable-VM gate.
- Added disposable-VM `/etc/hosts` mutation e2e coverage.
  `TestHostOverrideHostsFileE2E` uses the production default hosts manager,
  adds a unique managed Sandcastle block to `/etc/hosts`, verifies the hostname
  and markers exist, removes the block, and verifies cleanup. The test skips
  unless both `SANDCASTLE_E2E=1` and `SANDCASTLE_E2E_LOCAL_VM=1` are set.
- Extended the `local-vm` runner tier to include all `TestHostOverride.*E2E`
  coverage so the temp-file host override flow and the real `/etc/hosts`
  mutation flow share the same explicit disposable-VM gate.
- Tightened `scripts/e2e.sh tailscale` and `scripts/e2e.sh images` so they fail
  closed in the runner when required image source, auth key, image build, and
  pinned AI CLI version environment variables are missing instead of relying on
  deeper Go test skips.
- Updated the README testing section to list the full checked-in e2e runner
  tiers and their required environment variables, including `local-vm`,
  `tailscale`, `images`, and `public-routes`.
- Added a GitHub Actions CI workflow for safe verification. It runs the checked
  in `unit`, `gated`, and unprivileged `local` e2e runner tiers on push and pull
  requests, while leaving destructive Incus/Tailscale/image/public route tiers
  gated by their explicit environments.
- Added a manual GitHub Actions `Destructive e2e gates` workflow for the real
  environment tiers: `incus`, `tailscale`, `images`, `local-vm`, and
  `public-routes`. The workflow wires repository variables/secrets into
  `scripts/e2e.sh`, supports a configurable runner label for self-hosted Incus
  environments, and still relies on the checked-in fail-closed tier guards.
- Added an explicit `scripts/e2e.sh restricted` tier for restricted-client token,
  grant, and sandbox lifecycle coverage through a configured HTTPS Incus remote.
  It fails closed unless `SANDCASTLE_E2E=1`, `SANDCASTLE_E2E_REMOTE` is set to a
  non-`local` remote name, and disposable base/AI image sources are set.
- Split restricted-client tests out of the broad `incus` runner tier so a local
  Unix socket Incus run cannot hide restricted HTTPS remote coverage behind Go
  test skips.
- Added the `restricted` choice to the manual destructive e2e GitHub Actions
  workflow and documented the required remote/image environment in the README
  and e2e plan.
- Infrastructure creation now provisions route broker TLS material and runs
  runtime activation commands inside the infrastructure containers without
  depending on systemd inside OCI-imported containers. The creator uploads the
  local `sandcastle` binary to `sc-route-broker`, starts Caddy with
  `nohup caddy run`, and starts the route broker with
  `nohup sandcastle admin route-broker serve`. Infrastructure e2e can provide
  `SANDCASTLE_E2E_SANDCASTLE_BIN`, or build `./cmd/sandcastle` automatically.
- Added infrastructure deletion and gated real-Incus e2e coverage for
  disposable infrastructure creation. `sandcastle admin infra delete --yes` now
  removes runtime containers and the infrastructure project, and the e2e test
  verifies `sc-caddy` plus `sc-route-broker` creation when `SANDCASTLE_E2E=1`.
- Strengthened disposable infrastructure e2e coverage with a route broker mTLS
  runtime probe. `TestDisposableInfrastructureCreateAndDelete` now uploads a
  disposable client certificate into `sc-route-broker` and verifies the broker
  HTTPS listener accepts mTLS by reaching its `/routes` handler inside the
  infrastructure container and receiving the expected unauthorized response for
  an untrusted client certificate.
- Added image sync planning and Incus alias execution. `sandcastle admin image
  sync <image-ref>` now resolves an imported image or source alias and creates
  or updates the configured Sandcastle base/AI image aliases.
- Added gated real-Incus e2e coverage for base and AI image alias sync. The
  tests are opt-in through `SANDCASTLE_E2E_BASE_IMAGE_SOURCE` and
  `SANDCASTLE_E2E_AI_IMAGE_SOURCE`, create disposable `sandcastle/base:<run-id>`
  and `sandcastle/ai:<run-id>` aliases, verify their targets, and clean them up.
- Added project domain deny-list validation for project creation. Domains now
  normalize through a dedicated validator, reject malformed labels, reject an
  embedded public/special-use suffix snapshot, and honor comma-separated
  `SANDCASTLE_DENIED_DOMAIN_SUFFIXES` admin policy.
- Added `sandcastle admin tld refresh`, which fetches IANA's
  `tlds-alpha-by-domain.txt`, validates and normalizes the labels, and rewrites
  the generated embedded public-TLD deny-list snapshot. The current checked-in
  snapshot was refreshed from IANA and contains 1,437 public TLDs.
- Added checked-in Sandcastle OCI image definitions for `base` and `ai`.
  `images/base` installs core sandbox prerequisites and a bootstrap helper,
  while `images/ai` extends the configured base image and requires explicit
  pinned Codex, Claude Code, and Gemini CLI versions at build time.
- Added `sandcastle admin image build base|ai`, with dry-run JSON/text planning
  and Docker/Podman-compatible command execution through the local image build
  runner. Incus import/sync remains a separate follow-up step.
- Added `sandcastle admin image import base|ai <source-ref>`, which plans and
  executes `incus image copy <source-ref> <remote>: --alias <configured-alias>
  --reuse` for importing OCI/simplestreams image sources into the configured
  Incus remote with the Sandcastle base or AI alias.
- Added gated real image build e2e coverage. `TestImageBuildBaseE2E` builds a
  disposable base image tag when `SANDCASTLE_E2E=1` and
  `SANDCASTLE_E2E_IMAGE_BUILD=1`; `TestImageBuildAIE2E` additionally requires
  pinned Codex, Claude Code, and Gemini CLI versions and builds both a
  disposable base tag and disposable AI tag before cleanup.
- Added gated sandbox lifecycle e2e coverage. `TestSandboxLifecycleE2E`
  requires `SANDCASTLE_E2E_BASE_IMAGE_SOURCE` and
  `SANDCASTLE_E2E_AI_IMAGE_SOURCE`, syncs both into disposable aliases, creates
  a disposable project and sandbox, exercises stop/start/restart/remove, and
  purges the project and aliases unless `SANDCASTLE_E2E_KEEP=1`.
- Added gated CLI-path e2e coverage for `sandcastle add --detach`.
  `TestCLIAddDetachE2E` syncs disposable base/AI aliases, creates a disposable
  project through the Incus executor, invokes the production CLI entrypoint with
  `add <owner/project/name> --detach`, and asserts the sandbox instance exists.
- Added gated CLI-path e2e coverage for default `sandcastle add` interactive
  entry. `TestCLIAddDefaultEnterE2E` builds or uses a real Sandcastle binary,
  creates a disposable project, runs `add <owner/project/name>` without
  `--detach`, feeds a marker command plus `exit` to the default login shell over
  stdin, and asserts the subprocess exits after creating the sandbox.
- Added gated CLI-path e2e coverage for deterministic sandbox command
  execution. `TestCLIEnterCommandE2E` creates a disposable project and sandbox,
  then invokes the production CLI entrypoint with `enter <owner/project/name>
  pwd` to verify non-interactive command execution without requiring a test TTY.
- Added gated restricted-user token e2e coverage. `TestRestrictedUserTokenE2E`
  creates a disposable `sandcastle-<user>` certificate add token through the
  Incus trust executor and decodes it to verify client name, secret,
  fingerprint, and server addresses without adding a trusted client cert.
- Added gated restricted-user grant/access e2e coverage.
  `TestRestrictedUserGrantAccessE2E` bootstraps a disposable restricted client
  certificate, grants it one disposable Sandcastle metadata project, connects
  back over HTTPS with that cert, verifies owned project metadata is visible,
  verifies an ungranted project is not visible, and asserts global project
  creation is denied.
- Added gated restricted-user sandbox lifecycle e2e coverage.
  `TestRestrictedUserSandboxLifecycleE2E` requires an HTTPS Incus remote plus
  disposable base/AI image sources, creates a full disposable project, grants a
  restricted client certificate, then creates, verifies, stops, starts, and
  removes a sandbox through the restricted Incus client.
- Strengthened gated sandbox creation e2e coverage. The sandbox lifecycle and
  CLI `add --detach` e2e paths now read back the sandbox private Caddyfile and
  TLS cert/key files through Incus, assert the expected hostname and
  `reverse_proxy` target, and verify the certificate SAN matches the sandbox
  hostname.
- Strengthened gated sandbox lifecycle e2e coverage again by starting a small
  sandbox-local HTTP app, curling it through sandbox Caddy over HTTPS, changing
  the app port to 5173, and verifying Caddy is reloaded to proxy the new app
  port.
- Strengthened public route broker mutation e2e coverage.
  `TestRouteBrokerAuthorizedMutationE2E` now starts a sandbox-local HTTP app
  before adding the public route. When the delegated-domain public route env is
  set, the test uses a normal trusted HTTPS client, with SNI for the public
  hostname and a dial override to `SANDCASTLE_E2E_INFRA_HOST`, to verify
  infrastructure Caddy serves the sandbox response through an externally trusted
  certificate.
- Added an explicit `scripts/e2e.sh route-broker` tier for route broker mTLS
  mutation coverage without requiring delegated public route variables. It fails
  closed unless `SANDCASTLE_E2E=1`, `SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET`, and
  disposable base/AI image sources are set. The manual destructive e2e workflow,
  README, and e2e plan now expose the tier separately from `public-routes`.
- Added a standalone `scripts/e2e.sh cleanup` tier for failed destructive e2e
  runs. It requires `SANDCASTLE_E2E=1` and an explicit long
  `SANDCASTLE_E2E_RUN_ID`, lists managed Incus projects, and purges only
  Sandcastle project or infrastructure projects whose metadata/name contains the
  safe run token. Incus-free unit coverage verifies missing/short run IDs are
  rejected and that cleanup selection only matches the requested run id.
- Exposed the cleanup tier in the manual destructive GitHub Actions workflow and
  added an optional `run_id` workflow input that maps to `SANDCASTLE_E2E_RUN_ID`
  for cleanup or correlated disposable runs.
- Extended standalone e2e cleanup to remove matching Sandcastle restricted
  certificates and disposable `sandcastle/base:<run>` /
  `sandcastle/ai:<run>` image aliases in addition to project and infrastructure
  projects. Selection remains guarded by explicit long run IDs and Incus-free
  tests cover managed/unmanaged matching for certificates and image aliases.
- Updated the manual destructive e2e workflow to select one job-scoped
  `SANDCASTLE_E2E_RUN_ID` for non-cleanup runs when no explicit run id is
  provided, while keeping cleanup fail-closed unless a run id input or
  repository variable is present.
- Updated `scripts/e2e.sh` to select one invocation-wide `SANDCASTLE_E2E_RUN_ID`
  for destructive non-cleanup tiers when none is provided, while leaving
  standalone cleanup explicit-run-id only. This makes local runner behavior
  match the e2e plan's "every run uses a unique run id" safety rule.
- Expanded failed-project e2e diagnostics so run-id matching includes the
  project domain as well as owner, project, and Incus project name. This keeps
  diagnostics aligned with the e2e plan rule that disposable domains include the
  run id.
- Tightened failed-project e2e diagnostics so an empty or blank run id matches
  no projects instead of broadening to every managed project.
- Added a best-effort cleanup step to the manual destructive e2e workflow. When
  a non-cleanup tier fails and `SANDCASTLE_E2E_KEEP` is not `1`, the workflow
  invokes `scripts/e2e.sh cleanup` with the selected run id.
- Standalone e2e cleanup now logs each matched Sandcastle project,
  infrastructure project, restricted certificate, and image alias before
  deletion, so failed workflow cleanup output shows the exact resources it
  targeted.
- Standalone e2e cleanup now also removes matching local image-build tags for
  `sandcastle/base:<run>`, `sandcastle/base:<run>-ai-base`, and
  `sandcastle/ai:<run>` when the configured image build tool is available.

## Next Slice

- Run `scripts/e2e.sh route-broker` in regular CI/dev once disposable
  infrastructure image sources and broker Incus socket access are available in
  that environment.
- Run `scripts/e2e.sh public-routes` against a real delegated public test
  domain to exercise the checked-in externally trusted HTTPS assertion.
- Keep tests Incus-free for core logic, with e2e gated separately.

## Verification Log

- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json admin project create alice/myproject --domain myproject.project-tld --dry-run | rg 'projectCA|PRIVATE KEY|CERTIFICATE|ca.crt|ca.key'`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle`
- Passed: `go build -o bin/sc ./cmd/sc`
- Passed: `./bin/sandcastle version`
- Passed: `./bin/sc --output json ls`
- Passed: `go test ./...`
- Passed: `go test ./...`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && go build -o bin/sc ./cmd/sc`
- Passed: `./bin/sandcastle --output json ls`
- Passed: `go test ./...`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle admin project list`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && go build -o bin/sc ./cmd/sc`
- Passed: `./bin/sandcastle version && ./bin/sc version`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json admin project create alice/myproject --domain myproject.project-tld --dry-run`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json admin project create alice/myproject --domain myproject.project-tld --dry-run | rg 'dnsFiles|Corefile|db.myproject|ns IN A'`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle admin project delete alice/myproject 2>&1 || true`
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'Test(IncusProjectListingSmoke|DisposableProjectCreateAndPurge)' -count=1 -v` with expected skips when `SANDCASTLE_E2E` is not enabled.
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'Test(LogProjectDiagnostics|DisposableProjectCreateAndPurge)' -count=1 -v`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle status alice/myproject 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json host override add alice/myproject/codex example.com --dry-run 2>&1 || true` with expected local Incus connection failure on macOS before dry-run can resolve project/sandbox metadata.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle host override add alice/myproject/codex example.com 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && SANDCASTLE_HOSTS_FILE=/tmp/sandcastle-hosts-test ./bin/sandcastle host override add alice/myproject/codex example.com 2>&1 || true` with expected local Incus connection failure on macOS before hosts mutation.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json host override list alice/myproject 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && SANDCASTLE_HOSTS_FILE=/tmp/sandcastle-hosts-test ./bin/sandcastle host override rm alice/myproject/codex example.com 2>&1 || true` with expected local Incus connection failure on macOS before hosts mutation.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json admin user grant alice alice/myproject --dry-run`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json admin user grant alice alice/myproject --dry-run && ./bin/sandcastle admin user token alice 2>&1 || true` with expected local Incus connection failure on macOS for non-dry-run token creation.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle add alice/myproject/codex --dry-run 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle add alice/myproject/codex --detach 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle enter alice/myproject/codex 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json tailscale up alice/myproject --auth-key tskey-secret --dry-run 2>&1 || true` with expected local Incus connection failure on macOS before dry-run can resolve project metadata.
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle tailscale up alice/myproject 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle tailscale status alice/myproject 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json tailscale down alice/myproject --dry-run 2>&1 || true` with expected local Incus connection failure on macOS before dry-run can resolve project metadata.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle status alice/myproject 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle port set alice/myproject/codex 5173 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle add alice/myproject/codex --dry-run 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle add alice/myproject/codex 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle rm alice/myproject/codex 2>&1 || true && ./bin/sandcastle start alice/myproject/codex 2>&1 || true` with expected `--yes` guard and local Incus connection failure on macOS.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle port set alice/myproject/codex 5173 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle dns status alice/myproject 2>&1 || true && ./bin/sandcastle dns apply alice/myproject 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./...`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle status alice/myproject 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json admin project create alice/myproject --domain myproject.project-tld --dry-run`
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run TestIncusProjectListingSmoke -count=1 -v` with the expected skip when `SANDCASTLE_E2E` is not enabled.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json admin project create alice/myproject --domain myproject.project-tld --dry-run`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle add alice/myproject/codex --dry-run 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./internal/localtrust ./internal/incusx ./internal/cli`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json trust install alice/myproject --dry-run 2>&1 || true` with expected local Incus connection failure on macOS before dry-run can resolve project metadata.
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && SANDCASTLE_TRUST_DIR=/tmp/sandcastle-trust-test ./bin/sandcastle trust uninstall alice/myproject 2>&1 || true` with expected local Incus connection failure on macOS before local trust mutation.
- Passed: `go test ./internal/localdns ./internal/cli`
- Passed: `go test ./...`
- Passed: `go test ./internal/route ./internal/cli`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json route add app.example.com alice/myproject/codex --dry-run 2>&1 || true` with expected local Incus connection failure on macOS before dry-run can resolve project/sandbox metadata.
- Passed: `go test ./internal/incusx ./internal/route ./internal/cli`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle route rm app.example.com 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./internal/config ./internal/route ./internal/incusx ./internal/cli`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && SANDCASTLE_INFRA_HOST=203.0.113.10 ./bin/sandcastle --output json route add app.example.com alice/myproject/codex --dry-run 2>&1 || true` with expected local Incus connection failure on macOS before dry-run can resolve project/sandbox metadata.
- Passed: `go test ./internal/routebroker ./internal/route ./internal/incusx`
- Passed: `go test ./...`
- Passed: `go test ./internal/routebroker ./internal/route ./internal/cli`
- Passed: `go test ./...`
- Passed: `go test ./internal/incusx ./internal/routebroker`
- Passed: `go test ./...`
- Passed: `go test ./internal/routebroker ./internal/incusx ./internal/cli`
- Passed: `go test ./internal/routebroker -run 'Test(HTTPRunnerServesAuthorizedRouteOverMTLS|PlanServe|Server)' -count=1 -v`
- Passed: `go test ./internal/infra ./internal/incusx ./internal/cli`
- Passed: `go test ./...`
- Passed: `go test ./internal/caddy ./internal/infra ./internal/incusx ./internal/cli`
- Passed: `go test ./...`
- Passed: `go test ./internal/infra ./internal/incusx ./internal/cli ./internal/certs`
- Passed: `go test ./...`
- Passed: `go test ./internal/certs ./internal/infra ./internal/incusx ./internal/cli`
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run TestDisposableInfrastructureCreateAndDelete -count=1 -v` with the expected skip when `SANDCASTLE_E2E` is not enabled.
- Passed: `go test ./internal/infra ./internal/incusx ./internal/cli ./internal/e2e`
- Passed: `go test ./...`
- Passed: `go test ./internal/images ./internal/incusx ./internal/cli`
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'Test(ImageSyncAliasE2E|LoadConfig)' -count=1 -v` with the expected image sync skip when `SANDCASTLE_E2E` is not enabled.
- Passed: `go test ./internal/e2e -run 'Test(ImageSync.*AliasE2E|LoadConfig)' -count=1 -v` with the expected base/AI image sync e2e skips when real e2e is unset.
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle route rm app.example.com --dry-run`
- Passed: `go test ./internal/route ./internal/meta ./internal/caddy`
- Passed: `go test ./...`
- Passed: `go test ./internal/route ./internal/incusx ./internal/cli`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle route list 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle route rm app.example.com 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./internal/incusx ./internal/route ./internal/cli`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle route rm app.example.com 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./internal/incusx ./internal/route`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle route list 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle route rm app.example.com 2>&1 || true` with expected local Incus connection failure on macOS.
- Passed: `go test ./internal/incusx ./internal/route ./internal/cli`
- Passed: `go test ./...`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json route add app.example.com alice/myproject/codex --dry-run 2>&1 || true` with expected local Incus connection failure on macOS before dry-run can resolve project/sandbox metadata.
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && SANDCASTLE_LOCAL_DNS_STATE=/tmp/sandcastle-dns.yaml SANDCASTLE_RESOLVER_DIR=/tmp/sandcastle-resolver ./bin/sandcastle --output json dns install alice/myproject --dry-run 2>&1 || true` with expected local Incus connection failure on macOS before dry-run can resolve project metadata.
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && SANDCASTLE_LOCAL_DNS_STATE=/tmp/sandcastle-dns.yaml SANDCASTLE_RESOLVER_DIR=/tmp/sandcastle-resolver ./bin/sandcastle dns uninstall alice/myproject 2>&1 || true` with expected local Incus connection failure on macOS before local DNS mutation.
- Passed: `go test ./internal/localdns -run 'TestForwarder|TestQuestion' -count=1 -v`
- Passed: `go test ./internal/localdns ./internal/cli`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle dns forwarder --help`
- Passed: `go test ./...`
- Passed: `go test ./internal/localdns ./internal/cli`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && SANDCASTLE_LOCAL_DNS_STATE=/tmp/sandcastle-dns.yaml SANDCASTLE_LOCAL_DNS_SERVICE_DIR=/tmp/sandcastle-services SANDCASTLE_BIN=/tmp/sandcastle ./bin/sandcastle --output json dns service install --dry-run`
- Passed: `go test ./internal/localdns ./internal/cli`
- Passed: `go test ./internal/e2e -run 'Test(LocalDNSInstallForwardRefreshUninstallE2E|LoadConfig)' -count=1 -v` with the expected local DNS e2e skip when real e2e is unset.
- Passed: `SANDCASTLE_E2E=1 go test ./internal/e2e -run 'TestLocalDNSInstallForwardRefreshUninstallE2E' -count=1 -v`
- Passed: `bash -n scripts/e2e.sh && scripts/e2e.sh --help`
- Passed: `scripts/e2e.sh gated`
- Passed: `scripts/e2e.sh local`
- Passed: `scripts/e2e.sh incus >/tmp/sandcastle-incus-runner-incus.out 2>&1; rc=$?; cat /tmp/sandcastle-incus-runner-incus.out; test "$rc" -eq 2` with the expected fail-closed e2e guard when `SANDCASTLE_E2E` is unset.
- Passed: `scripts/e2e.sh unit`
- Passed: `go test ./...`
- Passed: `go test ./internal/domain ./internal/project ./internal/config`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json admin project create alice/myproject --domain myproject.project-tld --dry-run`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle admin project create alice/myproject --domain myproject.com --dry-run` with expected denied-suffix error.
- Passed: `go test ./...`
- Passed: `go test ./internal/domain ./internal/cli`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle admin tld refresh --output-file internal/domain/tld_snapshot_generated.go`
- Passed: `go test ./internal/domain ./internal/cli ./internal/project`
- Passed: `go test ./internal/localdns -run TestForwarderRoutesByStateAndReloads -count=1 -v && go test ./...` after one transient UDP local-DNS refusal on the first full-suite run.
- Passed: `go test ./internal/images ./internal/cli`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json admin image build base --tag sandcastle/base:debian-13 --dry-run`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json admin image build ai --tag sandcastle/ai:debian-13 --codex-version 1.2.3 --claude-version 2.3.4 --gemini-version 3.4.5 --dry-run`
- Passed: `go test ./internal/images ./internal/cli`
- Passed: `go build -o bin/sandcastle ./cmd/sandcastle && ./bin/sandcastle --output json admin image import base oci:sandcastle/base:debian-13 --dry-run`
- Passed: `go test ./internal/e2e -run 'Test(ImageBuild|LoadConfig)' -count=1 -v` with the expected image build skips when real e2e/build gates are unset.
- Passed: `go test ./internal/e2e -run 'Test(SandboxLifecycleE2E|LoadConfig)' -count=1 -v` with the expected sandbox lifecycle skip when real e2e is unset.
- Passed: `go test ./internal/e2e -run 'Test(CLIAddDetachE2E|LoadConfig)' -count=1 -v` with the expected CLI add skip when real e2e is unset.
- Passed: `go test ./internal/sandbox ./internal/incusx ./internal/cli ./internal/e2e -run 'Test(PlanEnter|SandboxEnterer|EnterCommand|CLIEnterCommandE2E|LoadConfig)' -count=1 -v` with the expected CLI enter e2e skip when real e2e is unset.
- Passed: `go test ./internal/e2e -run 'Test(RestrictedUserTokenE2E|LoadConfig)' -count=1 -v` with the expected restricted-user token e2e skip when real e2e is unset.
- Passed: `go test ./internal/e2e -run 'Test(RestrictedUser(Token|GrantAccess)E2E|LoadConfig)' -count=1 -v` with the expected restricted-user e2e skips when real e2e is unset.
- Passed: `go test ./internal/e2e -run 'TestRestrictedUser(Token|GrantAccess|SandboxLifecycle)E2E|TestLoadConfig' -count=1 -v` with the expected restricted-user e2e skips when real e2e is unset.
- Passed: `bash -n scripts/e2e.sh && go test ./...`
- Passed: `go test ./internal/e2e -run 'TestDisposableInfrastructureCreateAndDelete|TestLoadConfig' -count=1 -v` with the expected infrastructure e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'Test(SandboxLifecycleE2E|CLIAddDetachE2E|LoadConfig)' -count=1 -v` with the expected sandbox e2e skips when real e2e is unset.
- Passed: `go test ./internal/project ./internal/tailscale ./internal/incusx ./internal/e2e`
- Passed: `scripts/e2e.sh --help`
- Passed: `SANDCASTLE_E2E=1 scripts/e2e.sh tailscale` with the expected
  `SANDCASTLE_E2E_BASE_IMAGE_SOURCE` skip when no real base image source is
  configured.
- Passed: `scripts/e2e.sh tailscale` with the expected fail-closed e2e guard
  when `SANDCASTLE_E2E` is unset.
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'TestTailscaleAttachmentE2E|TestLoadConfig' -count=1 -v` with the expected Tailscale e2e skip when real e2e is unset.
- Passed: `go test ./internal/infra ./internal/incusx ./internal/e2e`
- Passed: `go test ./internal/infra -run TestPlanCreate -count=1 -v`
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'TestDisposableInfrastructureCreateAndDelete|TestLoadConfig' -count=1 -v` with the expected infrastructure e2e skip when real e2e is unset.
- Passed: `go test ./internal/incusx -run TestInfrastructureCreatorCreatesMissingResources -count=1 -v`
- Passed: `go test ./internal/incusx ./internal/e2e ./internal/dns`
- Passed: `go test ./internal/e2e -run 'TestProjectDNSE2E|TestLoadConfig' -count=1 -v` with the expected project DNS e2e skip when real e2e is unset.
- Passed: `go test ./internal/incusx -run 'Test(DNSManagerApply|ProjectCreatorCreatesMissingResources)' -count=1 -v`
- Passed: `bash -n scripts/e2e.sh && go test ./...`
- Passed: `go test ./internal/sandbox ./internal/cli ./internal/incusx ./internal/e2e`
- Passed: `go test ./internal/sandbox -run 'TestPlanCreate(AllocatesNextFreeSandboxIP|ReusesExistingSandboxIP|$)' -count=1 -v`
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'TestProjectDNSE2E|TestLoadConfig' -count=1 -v` with the expected project DNS e2e skip when real e2e is unset.
- Passed: `go test ./internal/incusx ./internal/e2e`
- Passed: `go test ./internal/e2e -run 'TestSandboxLifecycleE2E|TestLoadConfig' -count=1 -v` with the expected sandbox lifecycle e2e skip when real e2e is unset.
- Passed: `go test ./internal/incusx -run 'Test(SandboxCreatorCreatesInstance|SandboxPortSetterUpdatesMetadata|HostOverrideManagerAddUpdatesMetadataAndWritesFiles)' -count=1 -v`
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'TestTailscaleAttachmentE2E|TestLoadConfig' -count=1 -v` with the expected Tailscale e2e skip when real e2e is unset.
- Passed: `go test ./internal/e2e`
- Passed: `bash -n scripts/e2e.sh && scripts/e2e.sh --help`
- Passed: `go test ./...`
- Passed: `SANDCASTLE_E2E=1 scripts/e2e.sh tailscale` with the expected
  base/AI image source skip when no real image sources are configured.
- Passed: `go test ./internal/sandbox -run 'TestPlanCreate' -count=1 -v`
- Passed: `go test ./internal/cli -run 'TestAdd' -count=1 -v`
- Passed: `go test ./internal/e2e -run 'TestCLIAddDetachE2E|TestLoadConfig' -count=1 -v` with the expected CLI add e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'TestProjectDNSE2E|TestLoadConfig' -count=1 -v` with the expected project DNS e2e skip when real e2e is unset.
- Passed: `go test ./internal/routebroker ./internal/cli -run 'Test(Client|RouteManagerFromEnv)' -count=1 -v`
- Passed: `go test ./...`
- Passed: `go test ./internal/routebroker -run 'Test(Client|Server)' -count=1 -v`
- Passed: `go test ./internal/routebroker ./internal/cli -run 'Test(Client|Server|RouteManagerFromEnv)' -count=1 -v`
- Passed: `go test ./...`
- Passed: `go test ./internal/routebroker -run 'TestHTTPRunnerServesAuthorizedRouteOverMTLS|TestClient|TestServer' -count=1 -v`
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'TestHostOverrideE2E|TestLoadConfig' -count=1 -v` with the expected host override e2e skip when real e2e is unset.
- Passed: `bash -n scripts/e2e.sh && go test ./...`
- Passed: `go test ./internal/e2e -run 'TestLocalTrustInstallUninstallE2E|TestLoadConfig' -count=1 -v` with the expected local trust e2e skip when real e2e is unset.
- Passed: `bash -n scripts/e2e.sh && go test ./...`
- Passed: `go test ./internal/routebroker -run 'TestServerAddsAuthorizedRoute|TestHTTPRunnerServesAuthorizedRouteOverMTLS' -count=1 -v`
- Passed: `go test ./...`
- Passed: `go test ./internal/meta ./internal/routebroker -run 'Test(RouteConfigRoundTrip|ServerAddsAuthorizedRoute)' -count=1 -v`
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'Test(LogProjectDiagnostics|ProjectDiagnosticLines|DisposableProjectCreateAndPurge)' -count=1 -v` with the expected disposable project e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `go test ./internal/project ./internal/e2e -run 'Test(GetStatus|LogProjectDiagnostics|ProjectDiagnosticLines|DisposableProjectCreateAndPurge)' -count=1 -v` with the expected disposable project e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `go test ./internal/routebroker -run 'Test(Client|Server)' -count=1 -v`
- Passed: `go test ./...`
- Passed: `go test ./internal/infra -run TestPlanCreate -count=1 -v`
- Passed: `go test ./internal/e2e -run 'TestDisposableInfrastructureCreateAndDelete|TestLoadConfig' -count=1 -v` with the expected infrastructure e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `go test ./internal/config ./internal/infra -run 'Test(LoadAdminFromEnv|PlanCreate)' -count=1 -v`
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'Test(LoadConfig|DisposableInfrastructureCreateAndDelete)' -count=1 -v` with the expected infrastructure e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'Test(RouteBrokerAuthorizedMutationE2E|LoadConfig)' -count=1 -v` with the expected route broker mutation e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `bash -n scripts/e2e.sh && scripts/e2e.sh --help`
- Passed: `scripts/e2e.sh public-routes` with the expected fail-closed e2e guard when `SANDCASTLE_E2E` is unset.
- Passed: `go test ./internal/e2e -run 'Test(RouteBrokerAuthorizedMutationE2E|LoadConfig)' -count=1 -v` with the expected route broker mutation e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `go test ./internal/routebroker -run 'TestClient' -count=1 -v`
- Passed: `go test ./...`
- Passed: `bash -n scripts/e2e.sh && scripts/e2e.sh --help`
- Passed: `scripts/e2e.sh local-vm` with the expected fail-closed e2e guard when `SANDCASTLE_E2E` is unset.
- Passed: `go test ./internal/e2e -run 'Test(LoadConfig|LocalDNSInstallForwardRefreshUninstallE2E|LocalTrustInstallUninstallE2E|HostOverrideE2E)' -count=1 -v` with the expected local e2e skips when real e2e is unset.
- Passed: `go test ./...`
- Passed: `bash -n scripts/e2e.sh && scripts/e2e.sh --help`
- Passed: `scripts/e2e.sh tailscale` with the expected fail-closed e2e guard when `SANDCASTLE_E2E` is unset.
- Passed: `scripts/e2e.sh images` with the expected fail-closed e2e guard when `SANDCASTLE_E2E` is unset.
- Passed: `SANDCASTLE_E2E=1 scripts/e2e.sh tailscale` with the expected missing `SANDCASTLE_E2E_BASE_IMAGE_SOURCE` guard.
- Passed: `SANDCASTLE_E2E=1 scripts/e2e.sh images` with the expected missing `SANDCASTLE_E2E_IMAGE_BUILD` guard.
- Passed: `go test ./...`
- Passed: `git diff --check`
- Passed: `bash -n scripts/e2e.sh && go test ./...`
- Passed: `scripts/e2e.sh local`
- Passed: `git diff --check`
- Passed: `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/e2e-gates.yml"); YAML.load_file(".github/workflows/ci.yml")'`
- Passed: `bash -n scripts/e2e.sh && scripts/e2e.sh --help`
- Passed: `scripts/e2e.sh restricted` with the expected fail-closed e2e guard
  when `SANDCASTLE_E2E` is unset.
- Passed: `SANDCASTLE_E2E=1 SANDCASTLE_E2E_REMOTE=local scripts/e2e.sh restricted`
  with the expected non-local HTTPS remote guard.
- Passed: `SANDCASTLE_E2E=1 SANDCASTLE_E2E_REMOTE=remote-incus scripts/e2e.sh restricted`
  with the expected missing `SANDCASTLE_E2E_BASE_IMAGE_SOURCE` guard.
- Passed: `go test ./internal/e2e -run 'TestRestrictedUser(Token|GrantAccess|SandboxLifecycle)E2E|TestLoadConfig' -count=1 -v`
  with the expected restricted-user e2e skips when real e2e is unset.
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'Test(CLIAddDetachE2E|CLIAddDefaultEnterE2E|LoadConfig)' -count=1 -v`
  with the expected CLI add e2e skips when real e2e is unset.
- Passed: `go test ./internal/cli ./internal/e2e -run 'Test(Add|CLIAdd|LoadConfig)' -count=1 -v`
  with the expected CLI add e2e skips when real e2e is unset.
- Passed: `bash -n scripts/e2e.sh && scripts/e2e.sh --help`
- Passed: `go test ./...`
- Passed: `git diff --check`
- Passed: `scripts/e2e.sh local-vm` with the expected fail-closed e2e guard
  when `SANDCASTLE_E2E` is unset.
- Passed: `SANDCASTLE_E2E=1 scripts/e2e.sh local-vm` with the expected
  disposable-VM guard when `SANDCASTLE_E2E_LOCAL_VM` is unset.
- Passed: `go test ./internal/e2e -run 'Test(LocalDNS.*E2E|LoadConfig)' -count=1 -v`
  with the expected local DNS e2e skips when real e2e is unset.
- Passed: `SANDCASTLE_E2E=1 go test ./internal/e2e -run 'TestLocalDNSServiceInstallReloadUninstallE2E|TestLoadConfig' -count=1 -v`
  with the expected local DNS service e2e skip when `SANDCASTLE_E2E_LOCAL_VM`
  is unset.
- Passed: `bash -n scripts/e2e.sh && scripts/e2e.sh --help`
- Passed: `go test ./...`
- Passed: `git diff --check`
- Passed: `go test ./internal/e2e -run 'Test(LocalTrust.*E2E|LoadConfig)' -count=1 -v`
  with the expected local trust e2e skips when real e2e is unset.
- Passed: `SANDCASTLE_E2E=1 go test ./internal/e2e -run 'TestLocalTrustPlatformInstallUninstallE2E|TestLoadConfig' -count=1 -v`
  with the expected platform trust e2e skip when `SANDCASTLE_E2E_LOCAL_VM` is
  unset.
- Passed: `bash -n scripts/e2e.sh && scripts/e2e.sh --help`
- Passed: `go test ./...`
- Passed: `git diff --check`
- Passed: `go test ./internal/e2e -run 'Test(HostOverride.*E2E|LoadConfig)' -count=1 -v`
  with the expected host override e2e skips when real e2e is unset.
- Passed: `SANDCASTLE_E2E=1 go test ./internal/e2e -run 'TestHostOverrideHostsFileE2E|TestLoadConfig' -count=1 -v`
  with the expected `/etc/hosts` e2e skip when `SANDCASTLE_E2E_LOCAL_VM` is
  unset.
- Passed: `bash -n scripts/e2e.sh && scripts/e2e.sh --help`
- Passed: `go test ./...`
- Passed: `git diff --check`
- Passed: `go test ./internal/e2e -run 'Test(RouteBrokerAuthorizedMutationE2E|LoadConfig)' -count=1 -v` with the expected route broker mutation e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `go test ./internal/incusx -run 'TestRouteManager' -count=1 -v`
- Passed: `go test ./internal/routebroker ./internal/incusx -run 'Test(Server|RouteManager)' -count=1 -v`
- Passed: `go test ./...`
- Passed: `go test ./internal/route ./internal/incusx ./internal/routebroker -run 'Test(Conflict|RouteManager|Server)' -count=1 -v`
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'Test(RouteBrokerAuthorizedMutationE2E|LoadConfig)' -count=1 -v` with the expected route broker mutation e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `go test ./internal/config ./internal/caddy ./internal/infra ./internal/incusx ./internal/e2e -run 'Test(LoadAdminFromEnv|RenderInfrastructure|PlanCreate|RouteManager|LoadConfig)' -count=1 -v`
- Passed: `go test ./...`
- Passed: `go test ./internal/e2e -run 'Test(RouteBrokerAuthorizedMutationE2E|LoadConfig)' -count=1 -v` with the expected route broker mutation e2e skip when real e2e is unset.
- Passed: `go test ./internal/e2e -run 'TestRouteBrokerAuthorizedMutationE2E|TestLoadConfig' -count=1 -v` with the expected route broker mutation e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `git diff --check`
- Passed: `bash -n scripts/e2e.sh && scripts/e2e.sh --help`
- Passed: `scripts/e2e.sh route-broker` with the expected fail-closed e2e guard
  when `SANDCASTLE_E2E` is unset.
- Passed: `SANDCASTLE_E2E=1 scripts/e2e.sh route-broker` with the expected
  missing `SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET` guard.
- Passed: `SANDCASTLE_E2E=1 SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET=/var/lib/incus/unix.socket scripts/e2e.sh route-broker`
  with the expected missing `SANDCASTLE_E2E_BASE_IMAGE_SOURCE` guard.
- Passed: `SANDCASTLE_E2E=1 SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET=/var/lib/incus/unix.socket SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13 SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13 scripts/e2e.sh public-routes`
  with the expected missing `SANDCASTLE_E2E_PUBLIC_DOMAIN` guard.
- Passed: `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/e2e-gates.yml"); YAML.load_file(".github/workflows/ci.yml")'`
- Passed: `go test ./internal/e2e -run 'TestRouteBrokerAuthorizedMutationE2E|TestLoadConfig' -count=1 -v`
  with the expected route broker mutation e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `git diff --check`
- Passed: `bash -n scripts/e2e.sh && scripts/e2e.sh --help`
- Passed: `scripts/e2e.sh cleanup` with the expected fail-closed e2e guard
  when `SANDCASTLE_E2E` is unset.
- Passed: `SANDCASTLE_E2E=1 scripts/e2e.sh cleanup` with the expected missing
  `SANDCASTLE_E2E_RUN_ID` guard.
- Passed: `SANDCASTLE_E2E=1 SANDCASTLE_E2E_RUN_ID=short scripts/e2e.sh cleanup`
  with the expected too-short run id rejection before cleanup mutation.
- Passed: `go test ./internal/e2e -run 'Test(Cleanup|LoadConfig)' -count=1 -v`
  with the expected cleanup e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `git diff --check`
- Passed: `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/e2e-gates.yml"); YAML.load_file(".github/workflows/ci.yml")'`
- Passed: `bash -n scripts/e2e.sh && scripts/e2e.sh --help`
- Passed: `go test ./...`
- Passed: `git diff --check`
- Passed: `go test ./internal/e2e -run 'Test(Cleanup|LoadConfig)' -count=1 -v`
  with the expected cleanup e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `git diff --check`
- Passed: `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/e2e-gates.yml"); YAML.load_file(".github/workflows/ci.yml")'`
- Passed: `bash -n scripts/e2e.sh && scripts/e2e.sh --help`
- Passed: `scripts/e2e.sh tailscale` with the expected fail-closed e2e guard
  before run-id generation when `SANDCASTLE_E2E` is unset.
- Passed: `SANDCASTLE_E2E=1 scripts/e2e.sh tailscale` with generated
  `SANDCASTLE_E2E_RUN_ID` and the expected missing
  `SANDCASTLE_E2E_BASE_IMAGE_SOURCE` guard.
- Passed: `SANDCASTLE_E2E=1 SANDCASTLE_E2E_RUN_ID=e2e-explicit-run scripts/e2e.sh images`
  with the explicit run id preserved and the expected missing
  `SANDCASTLE_E2E_IMAGE_BUILD` guard.
- Passed: `SANDCASTLE_E2E=1 scripts/e2e.sh cleanup` with the expected missing
  `SANDCASTLE_E2E_RUN_ID` guard.
- Passed: `go test ./...`
- Passed: `git diff --check`
- Passed: `go test ./internal/e2e -run 'Test(ProjectDiagnostic|LogProjectDiagnostics)' -count=1 -v`
- Passed: `go test ./...`
- Passed: `git diff --check`
- Passed: `go test ./internal/e2e -run 'Test(ProjectDiagnostic|LogProjectDiagnostics)' -count=1 -v`
- Passed: `go test ./...`
- Passed: `git diff --check`
- Passed: `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/e2e-gates.yml"); YAML.load_file(".github/workflows/ci.yml")'`
- Passed: `go test ./...`
- Passed: `git diff --check`
- Passed: `go test ./internal/e2e -run 'Test(Cleanup|LoadConfig)' -count=1 -v`
  with the expected cleanup e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `git diff --check`
- Passed: `go test ./internal/e2e -run 'Test(Cleanup|LoadConfig)' -count=1 -v`
  with the expected cleanup e2e skip when real e2e is unset.
- Passed: `go test ./...`
- Passed: `git diff --check`

## Open Scope

- Running the real image build gates in CI/dev, sandbox lifecycle e2e with
  disposable images in CI/dev, restricted HTTPS-remote e2e in CI/dev,
  local-VM privileged mutation gates in a disposable VM, route broker mutation
  e2e in regular CI/dev, public delegated-domain/Let’s Encrypt route e2e, and
  broader real-Incus coverage remain open.
