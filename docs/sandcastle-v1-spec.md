# Sandcastle v1 Specification

This document captures the v1 product and architecture decisions for
Sandcastle after the project model was revised away from the older
tenant/YAML-first design.

Sandcastle gives developers Incus-backed development sandboxes that are easy to
create from a CLI, reachable over the developer's Tailscale network, named by
private project DNS, and optionally exposed through public HTTP routes.

## Goals

- Provide a user-facing CLI named `sandcastle`, with `sc` as an alias.
- Let a developer create, start, enter, stop, remove, and inspect containers in
  projects they own.
- Map each Sandcastle project to exactly one Incus project.
- Use Incus restricted client certificates as the primary security boundary for
  normal users.
- Keep v1 single-owner: each project has exactly one owning user.
- Store all authoritative Sandcastle state in Incus metadata.
- Give each project a private routed network, CoreDNS server, Tailscale sidecar,
  project CA, shared storage, and container sandboxes.
- Make the default project template an AI-focused image for Codex, Claude,
  Gemini, GitHub CLI, and related prompting workflows.
- Support optional public HTTP/HTTPS routes through an infrastructure Caddy and
  a narrow route broker.

## Non-Goals For v1

- Shared projects or multiple project users.
- VM sandboxes. The model should keep room for VMs later, but v1 implements
  containers first.
- Tailscale DNS or Tailscale DNS API integration.
- A broad Sandcastle control-plane server.
- Public TCP/UDP exposure beyond HTTP/HTTPS hostname routes.
- Project-wide DNS wildcards.
- Fully isolated certificate authority custody between members of a shared
  project. v1 has no shared projects.
- Raw `incus` usage as a supported product interface for normal users.

## Core Model

### User

A Sandcastle user owns one or more projects. In v1, a project has exactly one
owner. The owner has a restricted Incus client certificate scoped to the Incus
projects they own.

### Project

A Sandcastle project maps 1:1 to an Incus project.

Example:

```text
owner/project: alice/myproject
Incus project: sc-alice-myproject
project domain: myproject.project-tld
```

The user-facing project name is unique per owner. The private project domain is
also unique per owner. Two different owners may use the same private domain
because each project is attached to the owner's chosen tailnet, and DNS is
installed locally per developer machine.

Project domain validation uses a deny-list, not an allow-list. Sandcastle ships
with an embedded snapshot of public TLDs and special-use names, refreshed from
authoritative IANA data by an admin command. Project domains are rejected when
their final label is a current public TLD, an IANA special-use name, or an
admin-denied local suffix. Admins may add local deny-list entries and explicit
lab overrides.

Each project contains:

- one private Incus bridge network;
- one unauthenticated Tailscale sidecar container;
- one CoreDNS container;
- one project CA volume;
- one home volume;
- one workspace volume;
- zero or more user containers;
- optional ingress attachment for containers with public routes.

### Sandbox

A sandbox is the abstract runtime resource. v1 supports container sandboxes
only. VM support is a later extension.

Sandbox names are unique within a project only. Infrastructure names such as
`tailscale`, `dns`, and `ca` are reserved user-facing names.

## Incus Access Model

Sandcastle uses two Incus connection classes:

- Admin connection: unrestricted Incus access for project creation, global
  configuration, image sync, CIDR allocation, user certificate management, and
  infrastructure setup.
- User connection: restricted Incus certificate scoped to the projects owned by
  that user.

The restricted Incus certificate is the security primitive. The supported user
interface is still the Sandcastle CLI. If a user runs raw `incus` commands with
the restricted certificate, project restrictions should keep damage contained,
but Sandcastle may reconcile unsupported drift.

Admin operations create or update restricted user certificates and project
access. v1 does not require a broad Sandcastle server for this.

## Source Of Truth

All authoritative Sandcastle state is stored in Incus metadata. Local files are
only caches or machine-local installation state.

Use scalar metadata keys for searchable/indexable facts and versioned JSON
state blobs for structured data.

Project metadata example:

```text
user.sandcastle.kind=project
user.sandcastle.version=1
user.sandcastle.owner=alice
user.sandcastle.project=myproject
user.sandcastle.domain=myproject.project-tld
user.sandcastle.private_cidr=10.88.17.0/24
user.sandcastle.default_template=ai
user.sandcastle.state={...}
```

Sandbox metadata example:

```text
user.sandcastle.kind=sandbox
user.sandcastle.version=1
user.sandcastle.name=codex
user.sandcastle.owner=alice
user.sandcastle.project=myproject
user.sandcastle.app_port=3000
user.sandcastle.state={...}
```

## Networking

Each project gets one private Incus managed bridge by default. The admin CLI
allocates its CIDR from a configured pool and records the allocation in Incus
metadata. Allocation is stable because it is persisted, not derived only from a
hash of the project name.

The bridge is named `sc-private` inside the Incus project.

Example:

```text
private CIDR: 10.88.17.0/24
tailscale:   10.88.17.2
dns:         10.88.17.53
codex:       10.88.17.21
claude:      10.88.17.22
```

Containers get exactly one private IP by default. A container gets a second
ingress IP only if it is targeted by a public route.

Sandcastle assigns static private IPs from project metadata. Infrastructure
addresses are reserved, and sandbox addresses are allocated sequentially from a
container range.

Default private address convention:

```text
gateway:    .1
tailscale:  .2
dns:        .53
containers: .20-.199
reserved:   .200-.254
```

Deleted sandbox IPs may be reused after deletion.

The private project network is routed over Tailscale by the project Tailscale
sidecar. This is the normal developer access path.

## Tailscale

Each project has one Tailscale sidecar container. The admin creates and prepares
the sidecar, but does not connect it to a tailnet.

The sidecar is created and started during project creation. Before
authentication, its status is `running-logged-out`.

The project owner connects it:

```bash
sandcastle tailscale up myproject
```

The CLI execs into the Tailscale sidecar and runs `tailscale up` with the
project private CIDR advertised. The project owner chooses the tailnet by
authenticating that Tailscale login. The project has exactly one active
Tailscale attachment.

For unattended automation and e2e, Sandcastle can pass an auth key and an
advertised tag to `tailscale up`. The default automation tag is
`tag:sandcastle`, so tailnet policies can auto-approve the advertised project
subnet route for Sandcastle sidecars.

Example automation shape:

```bash
tailscale up \
  --auth-key="$SANDCASTLE_E2E_TAILSCALE_AUTHKEY" \
  --advertise-tags=tag:sandcastle \
  --advertise-routes=10.88.17.0/24
```

Sandcastle records observed Tailscale status in project metadata:

```json
{
  "tailscale": {
    "state": "connected",
    "tailnet": "example.com",
    "hostname": "sc-myproject",
    "advertisedRoutes": ["10.88.17.0/24"],
    "tailscaleIPs": ["100.80.12.34"],
    "lastCheckedAt": "2026-05-20T12:00:00Z"
  }
}
```

Do not store auth keys, reusable secrets, or stale login URLs in metadata.

## DNS

Sandcastle does not use Tailscale DNS in v1.

Each project runs its own CoreDNS server on the private project network. The DNS
server is reached through the Tailscale-advertised private CIDR. CoreDNS is not
itself a Tailscale node.

CoreDNS is created and started during project creation with a minimal zone, even
before any user sandboxes exist.

The project DNS server is authoritative for the project domain chosen by the
user:

```text
myproject.project-tld
```

Each sandbox gets exact and per-sandbox wildcard records:

```text
codex.myproject.project-tld        A 10.88.17.21
*.codex.myproject.project-tld      A 10.88.17.21
claude.myproject.project-tld       A 10.88.17.22
*.claude.myproject.project-tld     A 10.88.17.22
```

There is no project-wide wildcard by default.

The Sandcastle CLI renders and pushes CoreDNS zone/config files after changes.
CoreDNS does not need live Incus API access.

## Local DNS Installation

Developer machines use a local Sandcastle DNS forwarder. The forwarder is one
local service managing all installed project domains.

On macOS, the CLI should install resolver files that point to loopback and a
stable local port rather than directly to a Tailscale-routed IP:

```text
/etc/resolver/myproject.project-tld
nameserver 127.0.0.1
port 53541
```

The local forwarder reads CLI-managed local state:

```yaml
projects:
  - owner: alice
    project: myproject
    domain: myproject.project-tld
    dnsEndpoint:
      ip: 10.88.17.53
      port: 53
    resolver:
      listen: 127.0.0.1:53541
```

The forwarder should not perform live Incus lookups for every DNS query.
Commands such as `sandcastle dns install`, `sandcastle dns refresh`, and
`sandcastle dns uninstall` manage local state.

## Project CA And TLS

Each project has one project CA. The CA private key is stored in a dedicated
project CA volume, not mounted into every sandbox.

In v1, the project owner is trusted to access the project CA key through their
restricted project access. This is acceptable because v1 has no shared projects.

The CLI uses the project CA to issue leaf certificates for each sandbox Caddy.
Sandbox certificates cover:

```text
codex.myproject.project-tld
*.codex.myproject.project-tld
```

Short names such as `codex.myproject` may be included as best-effort SANs, but
the canonical supported DNS names are the FQDNs under the project domain.

Trust installation is explicit:

```bash
sandcastle trust install myproject
```

The CLI must warn that trusting a project CA means trusting that project to mint
certificates trusted by the local machine.

## Sandbox Caddy

Every sandbox gets Caddy by default. Sandbox Caddy listens on ports 80 and 443
on the private project IP and proxies to the sandbox app port on localhost.

Default:

```text
appPort = 3000
private HTTPS -> sandbox Caddy -> http://127.0.0.1:3000
```

Changing a sandbox app port updates private Caddy behavior:

```bash
sandcastle port set myproject/codex 5173
```

The CLI renders and pushes Caddy config and certificates. The sandbox starts
Caddy from the last rendered config on boot.

## Local Host Overrides

The owner can mask a real hostname locally for testing:

```bash
sandcastle host override add myproject/codex example.com
```

This is a local developer-machine override, not project DNS and not public DNS.

The command:

- adds `example.com` as an extra SAN on the sandbox leaf certificate;
- reissues/reloads sandbox Caddy config;
- adds a managed `/etc/hosts` entry mapping `example.com` to the sandbox private
  IP;
- warns if the project CA is not trusted locally.

v1 supports exact hostnames only. Wildcard host overrides are out of scope.

## Storage

Each project has durable home and workspace storage.

When creating a container, the user may choose separate subdirectories for home
and workspace mounts:

```bash
sandcastle add myproject/codex --home-dir codex --workspace-dir .
sandcastle add myproject/claude --home-dir claude --workspace-dir .
```

The default subdirectory is `"."`, meaning no subdirectory: mount the volume
root.

Workspace subdirectories may be shared freely. Sharing the same home subdirectory
with another running container requires explicit confirmation or a flag such as
`--share-home`, because concurrent writes to one Linux home can corrupt tool
state.

The Linux user inside a sandbox defaults to the Sandcastle user who created it.

## Images And Templates

v1 requires Sandcastle-maintained base images.

Images are published as OCI images and synced into Incus as managed aliases by
admin setup:

```bash
sandcastle admin image sync sandcastle/base:debian-13
sandcastle admin image sync sandcastle/ai:debian-13
```

The minimal base image contains:

- Caddy;
- OpenSSH server;
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

New projects default to template `ai`. Users can override per container:

```bash
sandcastle add myproject/codex
sandcastle add myproject/minimal --template base
```

AI is not a separate resource type or command group in v1. AI sandboxes are
normal containers that use the AI template/image.

Container build/run capability is opt-in per sandbox. The AI image may include
client tooling, but privileged Docker-in-Docker or equivalent nesting is not
enabled by default.

## Public HTTP Routes

Public routes expose HTTP/HTTPS hostnames through global infrastructure Caddy.
They are HTTP/HTTPS only in v1.

Public TLS terminates at infrastructure Caddy using Let's Encrypt. Infrastructure
Caddy proxies HTTP to the target sandbox's ingress IP and route port.

Example:

```bash
sandcastle route add app.example.com myproject/codex
```

Default target port resolution:

```text
route port -> current sandbox appPort -> project template appPort -> 3000
```

The resolved route port is stored explicitly at creation time. Later changes to
the sandbox app port do not silently change existing public routes.

Routes are global infrastructure metadata because hostname uniqueness and Caddy
configuration are global host concerns. The target project may store backlinks
for display, but the authoritative route table lives in the infrastructure
project.

Normal users can create public routes, but they must go through a narrow route
broker rather than receiving broad access to the infrastructure project.
When `SANDCASTLE_ROUTE_BROKER_URL` is configured, `sandcastle route add` and
`sandcastle route list`, `sandcastle route add`, and `sandcastle route rm` call
the broker over HTTPS mTLS using
`SANDCASTLE_ROUTE_BROKER_CLIENT_CERT` and
`SANDCASTLE_ROUTE_BROKER_CLIENT_KEY`.

The route broker:

- is reachable only over the private/Tailscale network in v1;
- authenticates users with mTLS using their Incus client certificate;
- maps the certificate fingerprint to Incus trust state;
- verifies the caller owns the target project;
- verifies the target sandbox exists and is Sandcastle-managed;
- verifies the requested public hostname is unclaimed;
- verifies public DNS points at the Sandcastle infrastructure IP/name;
- attaches/ensures target ingress networking as needed;
- updates global route metadata;
- regenerates/reloads infrastructure Caddy.

The route broker should be narrow in v1. It does not create projects, manage
users, allocate CIDRs, or manage Tailscale.

## CLI Shape

The product command is `sandcastle`. Install `sc` as a symlink/alias to the same
binary.

The CLI is implemented in Go with Cobra from the start.

Normal user resource addresses use `project/container`:

```bash
sandcastle add myproject/codex
sandcastle enter myproject/codex
sandcastle start myproject/codex
sandcastle stop myproject/codex
sandcastle rm myproject/codex
sandcastle port set myproject/codex 5173
sandcastle dns install myproject
sandcastle tailscale up myproject
sandcastle host override add myproject/codex example.com
sandcastle route add app.example.com myproject/codex
```

Admin commands use `owner/project` where needed:

```bash
sandcastle admin project create alice/myproject --domain myproject.project-tld
sandcastle admin user create alice
sandcastle admin user grant alice alice/myproject
sandcastle admin user token alice
```

`sandcastle add` creates and starts the container by default. In an interactive
TTY, it enters the container after successful setup:

```bash
sandcastle add myproject/codex
```

Use `--detach` or `--background` to create/start without entering:

```bash
sandcastle add myproject/codex --detach
```

`sandcastle enter` uses Incus exec by default. Management operations should use
the Incus Go SDK. `enter` may delegate to `incus exec` as an implementation
detail if that gives materially better PTY behavior.

Command output defaults to human-readable text. JSON is opt-in:

```bash
sandcastle ls --output json
sandcastle status myproject --json
```

## Implementation Notes

- The existing older docs describe one Incus project per tenant and YAML specs
  as desired state. That is superseded by this v1 spec.
- The first implementation starts with the Go CLI, not scripts.
- Management operations use the official Incus Go SDK wherever practical.
- Project creation is admin-driven in v1 because it allocates CIDRs, creates
  Incus projects, prepares infrastructure, and manages restricted trust.
- User day-to-day operations use restricted Incus access and Incus metadata.
- The local DNS forwarder and local host overrides are machine-local state, not
  Incus source of truth.
- Public route mutation is the one v1 user operation that crosses into global
  infrastructure; it is mediated by the route broker.
