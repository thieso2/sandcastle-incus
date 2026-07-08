# Sandcastle Glossary

The canonical domain vocabulary. Architecture overview in [`topology.md`](topology.md).

## Core nouns

- **Tenant** — The top-level ownership, identity, and infrastructure boundary.
  Its handle is the normalized GitHub username for login-provisioned tenants, or
  an admin-minted handle. Access, DNS, tailnet, storage, and the OIDC issuer are
  all scoped to it. Names key on the handle: infra project `sc2-<tenant>`,
  app project `sc2-<tenant>-<project>`, bridge `sc2-<tenant>`.
- **Project** — A real Incus project owned by a tenant (`sc2-<tenant>-<project>`),
  holding that project's machines, profiles, and storage. Created/deleted as an
  actual Incus project through the broker, not a metadata label. The seeded
  first project is `default`.
- **Machine** — A freeform container or VM inside a project — a vanilla `incus`
  instance with a native name (no `<project>-<machine>` mangling; `dev` can exist
  in two projects). Sits on the shared per-tenant bridge with a private IP and is
  **not** a Tailscale node. Created from a stock cloud image; the project's
  default profile supplies its login user, SSH key, and shared volumes.
- **Sidecar** — The one instance in `sc2-<tenant>` running CoreDNS (the tenant's
  DNS zone) and the Tailscale subnet-router, plus the Incus Reach proxy. One
  sidecar serves all of the tenant's projects.
- **Shared per-tenant bridge** — One bridge, `sc2-<tenant>`, created in the
  `default` Incus project; every `sc2-<tenant>-*` project references it via
  `features.networks=false`. Projects are not network-isolated from each other.
- **Per-tenant tailnet** — The Tailscale network the tenant's sidecar joins,
  using the **tenant's own** Tailscale key (supplied at `sc login`, or joined
  interactively via the printed login URL). The sidecar is its subnet-router,
  advertising the tenant's `/24`.
- **Incus Reach** — The sidecar's `tailscale serve` proxy that forwards the
  sidecar's tailnet `:8443` to the host's Incus API. The tenant's enrolled remote
  points at the sidecar's tailnet IP, so the host's TLS certificate is pinned
  end-to-end and no host port is exposed.
- **Machine hostname** — Flat: `<machine>.<suffix>` (suffix = the tenant handle).
  One CoreDNS zone per tenant; the Auth App reconciler auto-registers every
  running machine. Resolves to the machine's bridge IP, reachable over the tenant
  tailnet's subnet route.
- **Project profile** — Each project's Incus `default` profile bundles the shared
  `/home` + `/workspace` volume pair, the shared-bridge NIC, and cloud-init login
  (user + SSH key + sshd). This is how a machine "in a project" gets its shared,
  persistent home and workspace and is reachable over SSH for free. On hosts with
  idmapped-mount support the shared volumes are `security.shifted` so a CT and a
  VM see consistent ownership.
- **Per-tenant CA** — The certificate authority for private machine TLS
  hostnames, scoped to the tenant. (Leaf issuance for the private HTTPS path is
  future work; the public ingress path needs no CA install.)

## Infrastructure

- **Auth App** — The web service at the Auth Hostname: GitHub OAuth login, CLI
  device login, tenant provisioning, the Sandcastle OIDC provider for machine
  workload identity, and the DNS auto-registration reconciler. Terminates its own
  public hostname (embedded caddy + optional cloudflared).
- **Sandcastle Broker** — The appliance that authorizes and performs privileged
  tenant + project lifecycle over the host Incus socket, authenticating callers by
  their restricted client certificate.
- **Public Route** — A public HTTP(S) hostname → a machine, served by the edge
  (a caddy vhost, or a tunnel host). The host routes between `incusbr0` and the
  tenant bridge, so the edge reaches the machine's private IP directly.
- **Workload Identity / OIDC** — Short-lived Workload Identity Tokens the Auth
  App issues to machines; the OIDC issuer is per tenant.
- **Infrastructure Seed File** — The operator bootstrap bundle carrying Auth
  Hostname, Incus remote, CIDR pool, TLS material, and image references.
- **CIDR** — One `/24` per tenant, allocated from the installation's shared pool
  (auth-app + broker share it; the allocator de-conflicts across tenants). Role
  addresses: gateway `.1`, Tailscale `.2`, DNS `.3`.

## Auth

- **User Key** — The normalized GitHub username identifying a login user; for a
  personal tenant it is also the tenant handle.
- **CLI Device Login** — Browser-assisted device authorization that approves a
  login and provisions the caller's tenant; `sc login` drives it.
- **Restricted certificate** — The tenant's project-scoped Incus TLS client
  certificate, extended (never re-minted) when a new project is created.

## TLS / Machine Ingress

- **Tenant CA** — Per-tenant certificate authority generated at provisioning; its
  private key resides only on the tenant's sidecar. Trust root for all HTTPS on
  the tenant's machines.
- **Leaf cert** — A per-machine TLS certificate signed by the Tenant CA for the
  machine's own DNS names (e.g. `ct1.default.idefix`, `*.ct1.default.idefix`).
- **Machine name zone** — The DNS names the sidecar will sign for a tenant:
  everything under `*.default.<suffix>`. The sidecar signs any name in its own
  zone; it does not scope per machine.
- **caddy profile** — An Incus profile that installs Caddy on a machine to
  terminate HTTPS, force HTTP→HTTPS, reverse-proxy the app, and serve the
  built-in `/_r` and `/_w` file routes.
