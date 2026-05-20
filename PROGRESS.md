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
- Added local project CA trust planning and `sandcastle trust install` /
  `sandcastle trust uninstall`. Plans resolve the managed project, CA volume,
  CA certificate path, platform, trust name, and explicit trust warning.
- Added Incus-backed local trust execution that reads the project CA
  certificate from the project CA volume and installs/removes it through a
  platform trust store. Production supports macOS Keychain and Linux
  `update-ca-certificates`; tests and disposable automation can use
  `SANDCASTLE_TRUST_DIR` for file-backed trust state.
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

## Next Slice

- Add sandbox lifecycle e2e coverage for create/start/stop/restart/remove once
  disposable image prerequisites are available.
- Add real-Incus e2e coverage for `sandcastle add` default enter behavior and
  `--detach` once disposable images can support interactive exec safely.
- Add gated full-network Tailscale e2e when an auth key is available.
- Add local DNS service install/reload wrappers.
- Add route broker authorization and ingress cleanup on route removal.
- Add sandbox lifecycle e2e assertions for private Caddy config and issued
  sandbox certificate files once disposable image prerequisites are available.
- Add restricted-user e2e path for certificate/token grant verification after
  token bootstrap can be exercised safely.
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
- Passed: `go test ./...`

## Open Scope

- Restricted certificates, project creation, sandbox lifecycle, DNS,
  certificates, Tailscale execution, local DNS service installation, host
  overrides, public routes, and full real-Incus e2e remain to be implemented.
