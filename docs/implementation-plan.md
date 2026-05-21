# Sandcastle Incus Implementation Plan

This is the initial implementation plan for Sandcastle v1. It is intentionally
ordered around vertical slices that can be verified against a real Incus host.

## Principles

- The Incus project is the Sandcastle project boundary.
- Incus metadata is the authoritative source of truth.
- Normal users operate through restricted Incus certificates.
- Restricted user certificates are implemented in the first phase, with
  admin-only mode only as a temporary bootstrap path.
- Admin commands own privileged setup: users, projects, CIDR allocation, images,
  and global infrastructure.
- User commands own day-to-day sandbox lifecycle.
- Containers ship first. VM support stays out of v1 implementation.
- AI is a default template/image, not a separate resource type.
- Management operations use the Incus Go SDK. Interactive `enter` may delegate
  to `incus exec` for PTY quality.
- Public routes are deferred until the private Tailscale/DNS/Caddy path works
  end to end.

## Initial Go Package Layout

```text
cmd/sandcastle/
internal/cli/
internal/incusx/
internal/meta/
internal/naming/
internal/cidr/
internal/project/
internal/usertrust/
internal/sandbox/
internal/dns/
internal/certs/
internal/tailscale/
internal/images/
internal/routebroker/
internal/e2e/
```

Feature packages own use-case orchestration. Shared infrastructure packages
such as `incusx`, `meta`, `naming`, and `cidr` stay small and stable.

## Milestone 0: Repository And Tooling

Deliverables:

- Go module.
- Cobra CLI binary named `sandcastle`.
- Install or build support for an `sc` alias.
- Basic command structure with admin/user command groups.
- Shared logging, output formatting, and `--output text|json`.
- Incus remote loading from the existing Incus config directory.
- Unit test harness and e2e test harness skeleton.

Commands:

```text
sandcastle version
sandcastle ls
sandcastle admin version
```

Exit criteria:

- CLI builds locally.
- Unit tests run without Incus.
- E2E harness can detect missing Incus/Tailscale configuration and skip/fail
  clearly.

## Milestone 1: Metadata, Naming, And Incus Access

Deliverables:

- Typed metadata model for projects, sandboxes, routes, templates, Tailscale
  status, and local DNS state references.
- Scalar metadata keys plus versioned JSON state blobs.
- Naming package for owner/project to Incus project names.
- Restricted user certificate discovery helpers.
- Admin/user remote selection.
- Read-only project and sandbox listing.

Commands:

```text
sandcastle ls
sandcastle status myproject
sandcastle admin project list
```

Exit criteria:

- Metadata round-trips through Incus project and instance config.
- Existing unmanaged Incus resources are ignored or reported without mutation.

## Milestone 2: Admin Project Creation

Deliverables:

- Admin config for storage pool, CIDR pool, image aliases, naming prefix, and
  infrastructure project name.
- CIDR allocator that scans existing Sandcastle metadata and live Incus networks.
- Project creation:
  - Incus project `sc-<owner>-<project>`;
  - project metadata;
  - dedicated `sc-private` managed bridge network;
  - home and workspace volumes;
  - CA volume and project CA generation;
  - real `sc-tailscale` sidecar at `.2`, started but unauthenticated;
  - real `sc-dns` CoreDNS sidecar at `.3`, started with a minimal zone;
  - default templates, with `ai` as default.
- Domain deny-list validation seeded by embedded IANA public TLD and
  special-use snapshots.
- Project deletion plan with data-preserving default and explicit purge.

Commands:

```text
sandcastle admin project create alice/myproject --domain myproject.project-tld
sandcastle admin project status alice/myproject
sandcastle admin project delete alice/myproject --yes
```

Exit criteria:

- Project create is idempotent.
- A second project receives a non-conflicting CIDR.
- Project metadata is sufficient for user commands without YAML.
- Deletion preserves volumes unless purge is explicit.
- `sandcastle admin tld refresh` or equivalent can refresh the deny-list
  snapshot from IANA.

## Milestone 3: User Certificates And Restrictions

Deliverables:

- Admin user creation and restricted Incus certificate/token management.
- Restricted cert scoped to all projects owned by the user.
- User remote bootstrap instructions or command.
- Verification that restricted user access can manage owned project containers
  but not global Incus state or other users' projects.

Commands:

```text
sandcastle admin user create alice
sandcastle admin user grant alice alice/myproject
sandcastle admin user token alice
```

Exit criteria:

- E2E test can create a user connection from a token/cert.
- User commands fail against projects not in the restricted certificate scope.

## Milestone 4: Container Sandbox Lifecycle

Deliverables:

- Container creation from project default template.
- Sequential next-free static private IP allocation from `.20-.199`, persisted
  in sandbox metadata.
- Home/workspace volume mounts with independent subdir configuration.
- Default Linux user creation.
- App port metadata, defaulting to 3000.
- Caddy config and project-CA leaf cert issuance.
- Container start/stop/restart/remove.
- Interactive `enter`, with `add` entering by default in TTY sessions and
  `--detach` for background creation.

Commands:

```text
sandcastle add myproject/codex
sandcastle add myproject/claude --home-dir claude --workspace-dir .
sandcastle inspect myproject/codex
sandcastle enter myproject/codex
sandcastle port set myproject/codex 5173
sandcastle stop myproject/codex
sandcastle start myproject/codex
sandcastle rm myproject/codex --yes
```

The normal-user `project/sandbox` form resolves ownership from
`SANDCASTLE_OWNER`; explicit `owner/project/sandbox` remains accepted for
automation and admin-driven tests.

Exit criteria:

- New containers are reachable on the private bridge.
- Sandbox Caddy proxies `https://<name>.<project-domain>` to the app port.
- Home/workspace mount subdirs persist across container recreation.
- Shared home conflicts require explicit confirmation when another running
  container uses the same home subdir.

## Milestone 5: Project DNS

Deliverables:

- CoreDNS zone renderer from Incus sandbox metadata.
- Exact and per-sandbox wildcard records.
- DNS apply/reconcile command.
- DNS health checks.

Commands:

```text
sandcastle dns apply myproject
sandcastle dns status myproject
```

Exit criteria:

- CoreDNS answers exact sandbox names.
- CoreDNS answers per-sandbox wildcard names.
- No project-wide wildcard is generated.
- Removing a sandbox removes its records.

## Milestone 6: Tailscale Attachment

Deliverables:

- Prepared Tailscale sidecar can run `tailscale up` from the user CLI.
- Optional auth key input for e2e/automation.
- Optional advertised tag input for e2e/automation, defaulting to
  `tag:sandcastle`.
- Tailscale status collection and metadata recording.
- Route availability checks for the project private CIDR.

Commands:

```text
sandcastle tailscale up myproject
sandcastle tailscale status myproject
sandcastle tailscale down myproject
```

Exit criteria:

- With an auth key, e2e can connect the project sidecar unattended.
- Automation connects the sidecar with `--advertise-tags=tag:sandcastle` so
  tailnet route auto-approvers can approve the advertised project subnet.
- The project private CIDR is advertised.
- Tailscale status metadata records non-secret observed state.

## Milestone 7: Local DNS Forwarder And Trust

Deliverables:

- Local DNS forwarder process that reads CLI-managed local state.
- macOS resolver installation through `/etc/resolver/<domain>` pointing to
  loopback and a stable local port.
- Linux local DNS strategy, likely systemd-resolved first.
- Local state refresh/uninstall.
- Project CA trust install/uninstall.

Commands:

```text
sandcastle dns install myproject
sandcastle dns refresh myproject
sandcastle dns uninstall myproject
sandcastle trust install myproject
sandcastle trust uninstall myproject
```

Exit criteria:

- Developer machine resolves project names through the local forwarder.
- DNS remains stable when project DNS endpoint is refreshed.
- CA trust operations are explicit and reversible.

## Milestone 8: Local Host Overrides

Deliverables:

- Exact hostname override metadata.
- Project-CA cert reissue with extra SANs.
- Managed `/etc/hosts` entries.
- Override list/remove.

Commands:

```text
sandcastle host override add myproject/codex example.com
sandcastle host override list myproject
sandcastle host override rm myproject/codex example.com
```

Exit criteria:

- Local override masks an exact public hostname on the developer machine.
- Sandbox Caddy serves a valid project-CA certificate for the overridden name.
- Removing the override removes the hosts entry and extra SAN.

## Milestone 9: Infrastructure Caddy And Route Broker

This milestone is intentionally after the private developer path is working.

Deliverables:

- Infrastructure project creation.
- Infrastructure Caddy setup with Let's Encrypt.
- Route metadata model in infrastructure project.
- Narrow route broker reachable on private/Tailscale network.
- Broker mTLS authentication using user Incus client certificates.
- Public hostname proof by DNS resolution to the infrastructure IP/name.
- Per-route ingress NIC/IP attachment for target sandboxes.
- Caddy route rendering and reload.

Commands:

```text
sandcastle admin infra create
sandcastle route add app.example.com myproject/codex
sandcastle route list
sandcastle route rm app.example.com
```

Exit criteria:

- User can create a public HTTP/HTTPS route for an owned sandbox.
- Infrastructure Caddy obtains a Let's Encrypt certificate.
- Infrastructure Caddy proxies HTTP to the sandbox ingress IP and explicit route
  port.
- Route port is pinned at creation time.

## Milestone 10: Image Build And Sync

Deliverables:

- Sandcastle base image definition.
- Sandcastle AI image definition extending base.
- Pinned versions for AI CLIs and developer tools.
- Admin image sync command from OCI to Incus alias.

Commands:

```text
sandcastle admin image sync sandcastle/base:debian-13
sandcastle admin image sync sandcastle/ai:debian-13
```

Exit criteria:

- The base image works first for lifecycle, DNS, Caddy, and certificate tests.
- The AI image is then layered on top before the default template is considered
  complete.
- New projects default to template `ai` once the AI image exists.
- Container creation does not require apt bootstrapping for Caddy or core
  sandbox prerequisites.
- Credentials are absent from images and stored only in mounted home state.

## Cross-Cutting Work

- Drift detection and repair.
- Structured errors with clear remediation.
- JSON output for automation.
- Cleanup on partial failure.
- Resource ownership tags on every managed Incus resource.
- Defensive path handling for purge operations.
- Documentation for admin setup, user setup, Tailscale auth, DNS install, and
  trust install.
