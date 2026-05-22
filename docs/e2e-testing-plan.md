# Sandcastle Incus End-To-End Testing Plan

The e2e suite validates Sandcastle against real Incus resources. Destructive
tiers are explicitly enabled, use disposable run IDs, and fail closed when the
required environment is not present.

## Test Environment

Required for destructive Incus tiers:

- A working Incus instance reachable by the admin Incus config.
- A storage pool suitable for disposable custom volumes.
- Permission to create Incus projects, networks, containers, images, and trusted
  client certificates.
- Sandcastle base and AI images synced, imported, or buildable for the test.

Required only for specific external tiers:

- `restricted`: a non-local HTTPS Incus remote, plus disposable base and AI image
  sources.
- `tailscale`: `SANDCASTLE_E2E_TAILSCALE_AUTHKEY`, and optionally
  `SANDCASTLE_E2E_TAILSCALE_TAG` defaulting to `tag:sandcastle`.
- `images`: Docker or equivalent image build tooling, image build enabled, and
  pinned AI CLI versions.
- `route-broker`: host Incus socket mount path plus disposable image sources.
- `public-routes`: route broker inputs plus a delegated public route domain,
  infrastructure DNS proof target, and Let's Encrypt contact email.
- `local-vm`: local Incus VM support for the host-side disposable VM harness.

Safety:

- Every e2e run uses a unique run id.
- Every tenant, project, domain, image alias, certificate, and resource name
  includes that run id where the resource can escape the process.
- Destructive tests refuse to run unless `SANDCASTLE_E2E=1`.
- Tests refuse unsafe names that do not include the disposable prefix.
- Cleanup runs at the end and can also be invoked as a standalone tier.
- Tests that mutate local DNS, trust stores, launch services, or `/etc/hosts`
  run only inside disposable test VMs.

Suggested environment:

```text
SANDCASTLE_E2E=1
SANDCASTLE_E2E_REMOTE=local
SANDCASTLE_E2E_STORAGE_POOL=default
SANDCASTLE_E2E_CIDR_POOL=10.248.0.0/16
SANDCASTLE_E2E_TAILSCALE_AUTHKEY=tskey-auth-...
SANDCASTLE_E2E_TAILSCALE_TAG=tag:sandcastle
SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13
SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13
SANDCASTLE_E2E_IMAGE_BUILD=1
SANDCASTLE_E2E_CODEX_VERSION=...
SANDCASTLE_E2E_CLAUDE_CODE_VERSION=...
SANDCASTLE_E2E_GEMINI_CLI_VERSION=...
SANDCASTLE_E2E_SSH_PUBLIC_KEY="$(cat ~/.ssh/id_ed25519.pub)"
SANDCASTLE_E2E_PUBLIC_DOMAIN=e2e.example.com
SANDCASTLE_E2E_INFRA_HOST=203.0.113.10
SANDCASTLE_E2E_LETSENCRYPT_EMAIL=ops@example.com
SANDCASTLE_E2E_SANDCASTLE_BIN=/path/to/sandcastle
SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET=/var/lib/incus/unix.socket
```

## Runner Tiers

The checked-in runner keeps common tiers reproducible:

```bash
scripts/e2e.sh unit
scripts/e2e.sh gated
scripts/e2e.sh local
SANDCASTLE_E2E=1 SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13 SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13 scripts/e2e.sh incus
SANDCASTLE_E2E=1 SANDCASTLE_E2E_LOCAL_VM=1 scripts/e2e.sh local-vm
scripts/e2e-local-vm.sh
SANDCASTLE_E2E=1 SANDCASTLE_E2E_REMOTE=remote-incus SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13 SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13 scripts/e2e.sh restricted
SANDCASTLE_E2E=1 SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13 SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13 SANDCASTLE_E2E_TAILSCALE_AUTHKEY=tskey-auth-... scripts/e2e.sh tailscale
SANDCASTLE_E2E=1 SANDCASTLE_E2E_IMAGE_BUILD=1 SANDCASTLE_E2E_CODEX_VERSION=... SANDCASTLE_E2E_CLAUDE_CODE_VERSION=... SANDCASTLE_E2E_GEMINI_CLI_VERSION=... scripts/e2e.sh images
SANDCASTLE_E2E=1 SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET=/var/lib/incus/unix.socket SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13 SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13 scripts/e2e.sh route-broker
SANDCASTLE_E2E=1 SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET=/var/lib/incus/unix.socket SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13 SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13 SANDCASTLE_E2E_PUBLIC_DOMAIN=e2e.example.com SANDCASTLE_E2E_INFRA_HOST=203.0.113.10 SANDCASTLE_E2E_LETSENCRYPT_EMAIL=ops@example.com scripts/e2e.sh public-routes
SANDCASTLE_E2E=1 SANDCASTLE_E2E_RUN_ID=e2e-20260520-120000 scripts/e2e.sh cleanup
```

Tier meanings:

- `unit`: all Incus-free Go tests. The runner uses a sanitized environment and
  forces `SANDCASTLE_E2E=0` for this tier so it remains a unit pass even when
  invoked from a shell that has sourced a destructive e2e `.env`.
- `gated`: the e2e package with `SANDCASTLE_E2E=0`, useful for compile and
  skip behavior even from a shell with destructive e2e variables loaded.
- `local`: unprivileged local flows with temporary local DNS state.
- `local-vm`: disposable-VM local mutation flows for resolver state, user
  services, platform trust, and host overrides.
- `scripts/e2e-local-vm.sh`: host-side harness that creates a disposable local
  Incus VM, installs Go, mise, and nested Incus, seeds nested
  `sandcastle/base:latest` and `sandcastle/ai:latest`, starts root's systemd
  user service manager, copies the checkout, and runs `scripts/e2e.sh local-vm`
  inside the VM.
- `incus`: destructive real-Incus tenant, machine, DNS, trust, image sync, host
  override, and infrastructure smoke flows.
- `restricted`: restricted-client token, tenant grant, and machine lifecycle
  flows through an HTTPS Incus remote.
- `tailscale`: destructive real-Incus plus real-tailnet flow.
- `images`: real image build flows.
- `route-broker`: route broker mTLS mutation flow. Public route env is optional
  in this tier.
- `public-routes`: public route broker mutation plus public ingress validation.
- `cleanup`: standalone cleanup for managed disposable tenant projects,
  infrastructure projects, restricted certificates, image aliases, local image
  tags, and local mutation state matching an explicit run id.

For the `local` tier and destructive non-cleanup tiers, `scripts/e2e.sh`
preserves an explicit `SANDCASTLE_E2E_RUN_ID` when set and otherwise generates
one run id for the script invocation. The cleanup tier never generates a run id
because cleanup must target a known failed run explicitly.

## GitHub Actions

- `.github/workflows/ci.yml` runs only safe tiers on push and pull request:
  `unit`, `gated`, and unprivileged `local`.
- `.github/workflows/e2e-gates.yml` is manual (`workflow_dispatch`) for real
  environment gates: `incus`, `restricted`, `tailscale`, `images`,
  `route-broker`, `local-vm`, `public-routes`, and `cleanup`.
- The destructive workflow sets `SANDCASTLE_E2E=1` and relies on
  `scripts/e2e.sh` to fail closed when a selected tier's required variables are
  missing.
- Use the optional `run_id` input to set `SANDCASTLE_E2E_RUN_ID` for cleanup or
  correlated disposable runs. Cleanup always requires an explicit run id.
- When a non-cleanup destructive workflow tier fails and
  `SANDCASTLE_E2E_KEEP` is not `1`, the workflow runs cleanup as a best-effort
  follow-up using the selected run id.
- Configure non-secret values as repository or environment variables using the
  same names as the local shell environment.
- Configure `SANDCASTLE_E2E_TAILSCALE_AUTHKEY` as a repository or environment
  secret.
- Use the `runner` workflow input to target a self-hosted runner when Incus,
  host resolver mutation, or public ingress is not available on GitHub-hosted
  runners.

## Harness Contract

The harness should:

- create a run context with tenant, project, machine, domain, CIDR, certificate,
  route, and image names derived from the run id;
- call the same CLI or command-layer code users call;
- collect Incus diagnostics on failure;
- clean up even after partial failures;
- leave resources only when `SANDCASTLE_E2E_KEEP=1`.

## Phase 1: Tenant Lifecycle

Test:

1. Create a disposable tenant.
2. Verify the Incus project exists.
3. Verify tenant metadata.
4. Verify private bridge network and CIDR.
5. Verify home, workspace, CA, DNS, and Tailscale resources.
6. Verify tenant-local base and AI image aliases.
7. Re-run create and verify idempotence.
8. Delete without purge and verify durable data is preserved.
9. Delete with purge and verify durable data is removed.

Primary assertions:

- Tenant Incus project name is `sc-<tenant>`.
- CIDR is allocated from the configured pool and does not collide.
- Tenant metadata can reconstruct tenant state.

## Phase 2: Restricted User Access

Test:

1. Create a restricted user certificate/token.
2. Configure a user remote for the test with `sc remote add`.
3. Grant the user access to a disposable tenant.
4. Verify the user can list tenant metadata and machines.
5. Verify the user cannot access another disposable tenant.
6. Verify the user cannot mutate global Incus state.
7. Create, verify, stop, start, connect to, and delete a machine through the
   restricted user remote.

Primary assertions:

- Tenant scoping is enforced by Incus trust restrictions.
- Sandcastle user commands work with the restricted remote.
- Authorization is based on restricted certificate project grants, not on the
  user name matching the tenant name.

## Phase 3: Machine Lifecycle

Test:

1. Create `website/codex` from the default AI template.
2. Verify the container starts.
3. Verify metadata, app port, Linux user, home mount, and workspace mount.
4. Verify Caddy files and leaf certificate exist.
5. Start a small HTTP app on port 3000.
6. Verify private Caddy proxies to the app.
7. Change app port to 5173 and verify Caddy reconfiguration.
8. Stop, start, connect, run a command, and delete.

Primary assertions:

- New machines start by default.
- `--detach` avoids interactive attach.
- Default `create` connects to the machine shell and can exit cleanly under
  automation.
- Home/workspace subdirectories persist.
- Machine Caddy uses tenant CA leaf certificates.

## Phase 4: Tenant DNS

Test:

1. Create two machines in one project: `codex` and `claude`.
2. Apply tenant DNS.
3. Query CoreDNS directly on the private network.
4. Verify exact records:
   - `codex.<project>.<tenant-suffix>`
   - `claude.<project>.<tenant-suffix>`
5. Verify per-machine wildcard:
   - `test.codex.<project>.<tenant-suffix>`
6. Verify tenant-wide wildcard does not resolve.
7. Delete one machine, reapply DNS, and verify its records are gone.

Primary assertions:

- DNS is rendered from Incus machine metadata.
- CoreDNS does not need Incus API access.
- Tenant-wide wildcards are not generated.

## Phase 5: Tailscale Routed Access

Requires `SANDCASTLE_E2E_TAILSCALE_AUTHKEY`.

Test:

1. Run `sandcastle tailscale up` with the auth key and `tag:sandcastle`
   advertised, or `sandcastle tailscale up <tenant>` for an explicit tenant.
2. Verify the sidecar reaches connected state.
3. Verify the tenant private CIDR is advertised.
4. From the test runner, query CoreDNS through the Tailscale-routed private IP.
5. Curl machine private Caddy through the Tailscale route.
6. Record observed Tailscale status in tenant metadata.

Primary assertions:

- Tailscale auth secrets are not stored in metadata.
- Tailscale sidecars are tagged with `tag:sandcastle` during automation so
  tailnet auto-approvers can approve the advertised private CIDR route.
- DNS and HTTPS work over the advertised route.

Core e2e must still pass without this phase when no Tailscale auth key is
provided.

## Phase 6: Local DNS Forwarder

Run only inside a disposable VM. Linux is the first target, using Debian 13 or
Ubuntu 24.04 with systemd-resolved. macOS resolver tests come later.

Test:

1. Install local DNS state for the test tenant.
2. Start or reload the local forwarder.
3. Verify resolver config points to loopback and a stable port.
4. Resolve a machine hostname through the OS resolver.
5. Refresh endpoint state and verify the forwarder reloads.
6. Uninstall and verify resolver state is removed.

Primary assertions:

- The forwarder uses local state, not live Incus lookups per query.
- Resolver installation is reversible.

## Phase 7: Trust And Host Override

Run only inside a disposable VM.

Test:

1. Install tenant CA trust.
2. Add an exact host override for a disposable FQDN.
3. Verify `/etc/hosts` contains a managed entry.
4. Verify the machine certificate includes the extra SAN.
5. Curl `https://<override-host>` successfully.
6. Delete the override.
7. Verify the hosts entry and extra SAN are removed.
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
2. Start route broker with mTLS enabled.
3. Create a machine app on port 3000.
4. Point a disposable public hostname at infrastructure.
5. As a restricted user, call route broker with Incus client certificate mTLS.
6. Verify broker accepts only tenant targets granted to that certificate.
7. Verify broker rejects targets outside the certificate grant set.
8. Verify broker creates route metadata and ingress attachment.
9. Verify infrastructure Caddy obtains or serves the expected certificate.
10. Curl public hostname and verify response from the machine app.
11. Change machine app port and verify public route remains pinned to original
    route port.
12. Delete route and verify Caddy no longer serves it.

Primary assertions:

- Users do not need direct access to the infrastructure Incus project.
- Route hostnames are globally unique in route metadata.
- Public routes are HTTP/HTTPS only and proxy HTTP to the machine route port.

## Cleanup And Diagnostics

Every e2e test should capture on failure:

- Sandcastle command logs.
- Incus project list filtered by run id.
- Incus instance/network/volume config for disposable tenant and infrastructure
  projects.
- CoreDNS rendered zone.
- Caddy rendered configs.
- Tailscale status output with secrets redacted.
- Local DNS forwarder state when relevant.

Cleanup should remove:

- disposable containers;
- disposable networks;
- disposable volumes when purge is enabled;
- disposable Incus tenant projects;
- disposable infrastructure projects;
- disposable restricted certificates;
- disposable image aliases;
- disposable local image-build tags;
- local resolver files;
- local hosts entries;
- local trust entries;
- route metadata and Caddy routes.

`scripts/e2e.sh cleanup` can be run after a failed destructive job when the run
used an explicit `SANDCASTLE_E2E_RUN_ID`. It removes matching managed
Sandcastle tenant and infrastructure projects with purge semantics, deletes
matching restricted certificates, disposable image aliases, and local
image-build tags, and refuses to run without a long explicit run id.
