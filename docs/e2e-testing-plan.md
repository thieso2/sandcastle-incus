# Sandcastle Incus End-To-End Testing Plan

The e2e suite validates Sandcastle against a real Incus instance. It should be
explicitly enabled and destructive only inside disposable resource prefixes.

## Test Environment

Required:

- A working Incus instance reachable by the admin Incus config.
- A storage pool suitable for disposable custom volumes.
- Ability to create Incus projects, networks, containers, and trusted client
  certificates.
- Sandcastle base and AI images synced or buildable for the test.

Optional but required for full network tests:

- `SANDCASTLE_E2E_TAILSCALE_AUTHKEY`, an ephemeral or reusable auth key for a
  test tailnet.
- `SANDCASTLE_E2E_TAILSCALE_TAG`, defaulting to `tag:sandcastle`.
- A tailnet policy that auto-approves the advertised test subnet route for
  `tag:sandcastle`, or a documented manual approval step for non-CI runs.
- A public test domain or delegated subdomain for infrastructure Caddy tests.

Safety:

- Every e2e run uses a unique run id.
- Every owner/project/domain/resource name includes that run id.
- Tests refuse to run unless `SANDCASTLE_E2E=1`.
- Tests refuse unsafe names that do not include the disposable prefix.
- Cleanup runs at the end and can also be invoked as a standalone command.
- Tests that mutate local DNS, trust stores, launch services, or `/etc/hosts`
  must run inside disposable test VMs, not on the developer workstation.

Suggested environment:

```text
SANDCASTLE_E2E=1
SANDCASTLE_E2E_REMOTE=local
SANDCASTLE_E2E_STORAGE_POOL=default
SANDCASTLE_E2E_CIDR_POOL=10.248.0.0/16
SANDCASTLE_E2E_TAILSCALE_AUTHKEY=tskey-auth-...
SANDCASTLE_E2E_TAILSCALE_TAG=tag:sandcastle
SANDCASTLE_E2E_DOMAIN_SUFFIX=e2e.project-tld
SANDCASTLE_E2E_PUBLIC_DOMAIN=e2e.example.com
SANDCASTLE_E2E_INFRA_HOST=203.0.113.10
SANDCASTLE_E2E_LETSENCRYPT_EMAIL=ops@example.com
SANDCASTLE_E2E_SANDCASTLE_BIN=/path/to/sandcastle
```

## Harness Shape

Use Go integration tests with explicit build tags or environment gates:

```bash
SANDCASTLE_E2E=1 go test ./internal/e2e -run TestProjectLifecycle -count=1
```

The checked-in `scripts/e2e.sh public-routes` tier fails closed unless the
broker socket, disposable image sources, delegated public route domain,
infrastructure DNS proof target, and Let's Encrypt contact email are all set.
The checked-in `scripts/e2e.sh local-vm` tier fails closed unless
`SANDCASTLE_E2E_LOCAL_VM=1` is set, keeping local resolver, trust, and hosts
mutation coverage opt-in for disposable VM runs.
The checked-in `scripts/e2e.sh restricted` tier fails closed unless a non-local
`SANDCASTLE_E2E_REMOTE` and disposable image sources are set, keeping
restricted certificate lifecycle checks on an HTTPS Incus remote.

The checked-in runner keeps common tiers reproducible:

```bash
scripts/e2e.sh unit
scripts/e2e.sh gated
scripts/e2e.sh local
SANDCASTLE_E2E=1 scripts/e2e.sh incus
SANDCASTLE_E2E=1 SANDCASTLE_E2E_LOCAL_VM=1 scripts/e2e.sh local-vm
SANDCASTLE_E2E=1 SANDCASTLE_E2E_REMOTE=remote-incus SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13 SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13 scripts/e2e.sh restricted
SANDCASTLE_E2E=1 SANDCASTLE_E2E_TAILSCALE_AUTHKEY=tskey-auth-... scripts/e2e.sh tailscale
SANDCASTLE_E2E=1 SANDCASTLE_E2E_IMAGE_BUILD=1 scripts/e2e.sh images
SANDCASTLE_E2E=1 SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET=/var/lib/incus/unix.socket SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13 SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13 scripts/e2e.sh route-broker
SANDCASTLE_E2E=1 SANDCASTLE_E2E_RUN_ID=e2e-20260520-120000 scripts/e2e.sh cleanup
```

Tier meanings:

- `unit`: all Incus-free Go tests.
- `gated`: the e2e package with default environment gates, useful for compile
  and skip behavior.
- `local`: unprivileged local e2e flows, currently local DNS
  install/forward/refresh/uninstall with temporary state.
- `local-vm`: disposable-VM local mutation flows for local DNS resolver state,
  the platform DNS forwarder service, local CA trust, and host override
  coverage. Requires `SANDCASTLE_E2E_LOCAL_VM=1`.
- `incus`: destructive real-Incus flows, requiring `SANDCASTLE_E2E=1`.
- `restricted`: restricted-client token, grant, and sandbox lifecycle flows
  through an HTTPS Incus remote, requiring `SANDCASTLE_E2E=1`, a non-local
  `SANDCASTLE_E2E_REMOTE`, `SANDCASTLE_E2E_BASE_IMAGE_SOURCE`, and
  `SANDCASTLE_E2E_AI_IMAGE_SOURCE`.
- `tailscale`: destructive real-Incus plus real-tailnet flow, requiring
  `SANDCASTLE_E2E=1`, `SANDCASTLE_E2E_BASE_IMAGE_SOURCE`,
  `SANDCASTLE_E2E_AI_IMAGE_SOURCE`, and `SANDCASTLE_E2E_TAILSCALE_AUTHKEY`.
- `images`: real image build flows, requiring `SANDCASTLE_E2E=1`,
  `SANDCASTLE_E2E_IMAGE_BUILD=1`, and pinned AI CLI versions for the AI image.
- `route-broker`: route broker mTLS mutation flow, requiring
  `SANDCASTLE_E2E=1`, `SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET`,
  `SANDCASTLE_E2E_BASE_IMAGE_SOURCE`, and `SANDCASTLE_E2E_AI_IMAGE_SOURCE`.
  Public route env is optional in this tier.
- `cleanup`: standalone cleanup for managed disposable project and
  infrastructure projects, restricted certificates, and image aliases matching
  an explicit `SANDCASTLE_E2E_RUN_ID`, requiring `SANDCASTLE_E2E=1`. Short or
  missing run IDs are rejected.

GitHub Actions:

- `.github/workflows/ci.yml` runs only safe tiers on push and pull request:
  `unit`, `gated`, and unprivileged `local`.
- `.github/workflows/e2e-gates.yml` is manual (`workflow_dispatch`) for real
  environment gates: `incus`, `restricted`, `tailscale`, `images`,
  `route-broker`, `local-vm`, `public-routes`, and `cleanup`. It sets
  `SANDCASTLE_E2E=1` and relies on
  `scripts/e2e.sh` to fail closed when a selected tier's required variables are
  missing. Use the optional `run_id` input to set `SANDCASTLE_E2E_RUN_ID` for
  cleanup or for correlated disposable runs.
- Configure non-secret values as repository or environment variables using the
  same names as the local shell environment, such as
  `SANDCASTLE_E2E_BASE_IMAGE_SOURCE`, `SANDCASTLE_E2E_AI_IMAGE_SOURCE`,
  `SANDCASTLE_E2E_PUBLIC_DOMAIN`, and
  `SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET`.
- Configure `SANDCASTLE_E2E_TAILSCALE_AUTHKEY` as a repository or environment
  secret.
- Use the `runner` workflow input to target a self-hosted runner when Incus,
  host resolver mutation, or public ingress is not available on GitHub-hosted
  runners.

The harness should:

- create a run context with owner, project, domain, and CIDR names;
- call the same CLI or command-layer code users call;
- collect Incus diagnostics on failure;
- clean up even after partial failures;
- leave resources only when `SANDCASTLE_E2E_KEEP=1`.

Use two e2e tiers:

- Core Incus-only e2e: does not require Tailscale, validates project topology,
  metadata, bridge networking, CoreDNS from inside Incus, sandbox lifecycle, and
  sandbox Caddy.
- Full network e2e: gated by `SANDCASTLE_E2E_TAILSCALE_AUTHKEY`, validates real
  Tailscale route advertisement and access through the routed private CIDR.

## Phase 1: Admin Project Lifecycle

Test:

1. Create disposable owner.
2. Create disposable project.
3. Verify Incus project exists.
4. Verify project metadata.
5. Verify private bridge network and CIDR.
6. Verify home, workspace, CA, DNS, and Tailscale state resources.
7. Re-run create and verify idempotence.
8. Delete without purge and verify durable data is preserved.
9. Delete with purge and verify durable data is removed.

Primary assertions:

- Project name is `sc-<owner>-<project>`.
- CIDR is allocated from the configured pool and does not collide.
- Metadata alone can reconstruct project state.

## Phase 2: Restricted User Access

Test:

1. Create restricted user certificate/token for the owner.
2. Configure a user remote for the test.
3. Verify user can list owned project metadata.
4. Verify user cannot access another test project.
5. Verify user cannot mutate global Incus state.
   The checked-in `TestRestrictedUserGrantAccessE2E` covers these access
   checks against an HTTPS Incus remote.
6. Create, verify, stop, start, and remove a sandbox through the restricted
   user remote.
   The checked-in `TestRestrictedUserSandboxLifecycleE2E` creates a full
   disposable project with real image aliases, grants a restricted certificate,
   and runs sandbox lifecycle plus private Caddy checks through that restricted
   Incus client.

Primary assertions:

- Project scoping is enforced by Incus trust restrictions.
- Sandcastle user commands work with the restricted remote.
- The normal e2e path reruns sandbox lifecycle through a restricted user remote.

## Phase 3: Container Lifecycle

Test:

1. Create `project/codex` from the default AI template.
2. Verify container starts.
3. Verify metadata, app port, user, home mount, and workspace mount.
   The checked-in CLI `add --detach` e2e path now exercises `--template base`,
   `--home-dir`, and `--workspace-dir` and verifies the resulting mount
   sources on the Incus instance.
   The checked-in CLI default `add` e2e path runs a real Sandcastle subprocess,
   feeds a marker command plus `exit` to the default login shell over stdin, and
   verifies the sandbox was created.
4. Verify Caddy files and leaf certificate exist.
5. Start a small HTTP app on port 3000.
6. Verify private Caddy proxies to the app.
7. Change app port to 5173 and verify Caddy reconfiguration.
   The checked-in `TestSandboxLifecycleE2E` covers Caddy startup, HTTPS
   proxying to a sandbox-local app, and proxy retargeting after `port set`.
8. Stop, start, enter/check command execution, and remove.

Primary assertions:

- New containers start by default.
- `--detach` avoids interactive attach.
- Default `add` enters the sandbox shell and can exit cleanly under automation.
- Home/workspace subdirs persist.
- Caddy uses project CA leaf certs.

## Phase 4: Project DNS

Test:

1. Create two containers: `codex` and `claude`.
2. Apply DNS.
3. Query CoreDNS directly on the private network.
   The checked-in `TestProjectDNSE2E` covers two sandbox exact records,
   per-sandbox wildcard records, distinct sandbox private IPs, and project-wide
   wildcard denial by querying `sc-dns` from inside the sandbox network
   namespace. It also removes one sandbox, reapplies DNS, and verifies the
   removed sandbox's record is gone.
4. Verify exact records:
   - `codex.<domain>`
   - `claude.<domain>`
5. Verify per-sandbox wildcard:
   - `test.codex.<domain>`
6. Verify project-wide wildcard does not resolve:
   - `anything.<domain>`
7. Remove one container and verify records update.

Primary assertions:

- DNS is rendered from Incus metadata.
- CoreDNS does not need Incus API access.

## Phase 5: Tailscale Routed Access

Requires `SANDCASTLE_E2E_TAILSCALE_AUTHKEY`.

Test:

1. Run `sandcastle tailscale up <project>` with the auth key and
   `tag:sandcastle` advertised.
2. Verify sidecar reaches connected state.
3. Verify project private CIDR is advertised.
   The checked-in `TestTailscaleAttachmentE2E` covers route attachment against
   a real Incus project and real Tailscale auth key when the `tailscale` tier is
   enabled.
4. From the test runner, query CoreDNS through the Tailscale-routed private IP.
5. Curl sandbox private Caddy through the Tailscale route.
   `TestTailscaleAttachmentE2E` creates a disposable sandbox, applies project
   DNS, starts a sandbox-local HTTP app, then verifies both CoreDNS A-record
   resolution and HTTPS Caddy proxying from the test runner over the routed
   private CIDR.
6. Record observed Tailscale status in project metadata.

Primary assertions:

- Tailscale auth secrets are not stored in metadata.
- Tailscale sidecars are tagged with `tag:sandcastle` during automation so
  tailnet auto-approvers can approve the advertised private CIDR route.
- DNS and HTTPS work over the advertised route.

Core e2e must still pass without this phase when no Tailscale auth key is
provided.

## Phase 6: Local DNS Forwarder

Run only inside a disposable VM. Linux is the first target, using Debian 13 or
Ubuntu 24.04 with systemd-resolved. macOS resolver and Keychain tests come later.

Test:

1. Install local DNS state for the test project.
2. Start or reload the local forwarder.
3. Verify resolver config points to loopback and stable port.
4. Resolve `codex.<domain>` through the OS resolver.
5. Refresh endpoint state and verify the forwarder reloads.
6. Uninstall and verify resolver state is removed.

Primary assertions:

- The forwarder uses local state, not live Incus lookups per query.
- Resolver installation is reversible.

## Phase 7: Trust And Host Override

Run only inside a disposable VM.

Test:

1. Install project CA trust.
   The checked-in `TestLocalTrustInstallUninstallE2E` reads the real project CA
   from a disposable Incus project and installs/uninstalls it through a
   file-backed trust store, avoiding host OS trust mutation.
   The checked-in `TestLocalTrustPlatformInstallUninstallE2E` uses the same
   disposable project CA but installs/uninstalls it through the real platform
   trust backend under the explicit `local-vm` gate.
2. Add exact host override for a disposable FQDN.
   The checked-in `TestHostOverrideE2E` redirects `/etc/hosts` writes to a
   disposable file, adds an exact override, verifies the host entry, sandbox
   Caddy routing, and the extra certificate SAN, then removes the override.
   The checked-in `TestHostOverrideHostsFileE2E` uses the default hosts manager
   against `/etc/hosts` under the explicit `local-vm` gate and verifies the
   managed block is added and removed.
3. Verify `/etc/hosts` contains a managed entry.
4. Verify sandbox certificate includes the extra SAN.
5. Curl `https://<override-host>` successfully.
6. Remove override.
7. Verify hosts entry and extra SAN are removed.
8. Uninstall CA trust.

Primary assertions:

- Host overrides are local-only.
- Wildcards are not supported in v1.
- Trust install/uninstall is explicit and reversible.

## Phase 8: Public HTTP Route Broker

Requires a public test domain and infrastructure IP/name. Configure
`SANDCASTLE_E2E_PUBLIC_DOMAIN` for delegated disposable hostnames,
`SANDCASTLE_E2E_INFRA_HOST` for the DNS proof target, and
`SANDCASTLE_E2E_LETSENCRYPT_EMAIL` for the contact email that should be passed
to infrastructure Caddy as `SANDCASTLE_LETSENCRYPT_EMAIL`.

Test:

1. Create infrastructure project and Caddy.
2. Start route broker on the private/Tailscale network.
   The checked-in infrastructure creator uploads the local `sandcastle` binary
   from `SANDCASTLE_BIN`; infrastructure e2e can use
   `SANDCASTLE_E2E_SANDCASTLE_BIN`, or build `./cmd/sandcastle` automatically.
   `TestDisposableInfrastructureCreateAndDelete` verifies the route broker
   runtime process accepts an mTLS client certificate inside the disposable
   infrastructure container.
   For local Unix-socket Incus remotes, set
   `SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET` to the host Incus socket path to
   mount it into `sc-route-broker` at `/var/lib/incus/unix.socket`; leave it
   unset when the broker should not receive host Incus access.
   When that socket is configured, `TestDisposableInfrastructureCreateAndDelete`
   also creates a disposable restricted user certificate and verifies the
   containerized route broker can map that mTLS identity through Incus and list
   routes.
   `TestRouteBrokerAuthorizedMutationE2E` goes further when the socket and base
   image sources are configured: it creates disposable infrastructure, a project,
   a sandbox, a trusted broker client certificate, and a temporary DNS proof in
   the broker container before adding, listing, and removing a route through the
   running broker. When the delegated public route env is configured, it also
   verifies a normal trusted HTTPS client can reach the sandbox app through the
   public hostname and infrastructure Caddy.
3. Create a sandbox app on port 3000.
4. Point a disposable public hostname at infrastructure.
5. As restricted user, call route broker with Incus client certificate mTLS.
6. Verify broker accepts only owned project targets.
7. Verify broker rejects unowned project targets.
8. Verify broker creates route metadata and ingress attachment.
9. Verify infrastructure Caddy obtains/serves Let's Encrypt cert.
10. Curl public hostname and verify response from sandbox app.
11. Change sandbox appPort and verify public route remains pinned to original
    route port.
12. Remove route and verify Caddy no longer serves it.

Primary assertions:

- Users do not need direct access to the infrastructure Incus project.
- Route hostnames are globally unique in route metadata.
- Public routes are HTTP/HTTPS only and proxy HTTP to the sandbox route port.

## Cleanup And Diagnostics

Every e2e test should capture on failure:

- Sandcastle command logs.
- Incus project list filtered by run id.
- Incus instance/network/volume config for disposable projects.
- CoreDNS rendered zone.
- Caddy rendered configs.
- Tailscale status output with secrets redacted.
- Local DNS forwarder state when relevant.

Cleanup should remove:

- disposable containers;
- disposable networks;
- disposable volumes when purge is enabled;
- disposable Incus projects;
- disposable restricted certificates;
- local resolver files;
- local hosts entries;
- local trust entries;
- route metadata and Caddy routes.

`scripts/e2e.sh cleanup` can be run after a failed destructive job when the
run used an explicit `SANDCASTLE_E2E_RUN_ID`. It removes matching managed
Sandcastle project and infrastructure projects with purge semantics, deletes
matching Sandcastle restricted certificates and disposable image aliases, and
refuses to run without a long explicit run id.
