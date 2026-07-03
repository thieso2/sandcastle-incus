# BYO per-tenant tailnets; the sidecar proxies the host Incus onto the tenant tailnet

Status: accepted (supersedes the Headscale direction sketched in the `v2-no-public-ports` notes)

## Context

Sandcastle v2 has **no public app ingress** and tenant hosts may have **no public IP**.
The host runs one Incus with all tenants' projects; a tenant's **restricted client
certificate scopes them to their own projects**. The open question was: how does a
tenant's client reach the host's Incus API (`:8443`) when the host is not on any tailnet?

An earlier note proposed **self-hosted Headscale** with one platform-hosted namespace per
tenant (and therefore one public coordination endpoint). That was abandoned.

## Decision

**1. BYO per-tenant tailnet.** The **Tenant Tailnet is the tenant's own Tailscale
network** — the tenant supplies their own Tailscale auth key at registration and the
tenant's **Sidecar** joins *that* network. Sandcastle hosts **no coordinator** (no
Headscale) and needs **no public endpoint of its own**. "Each tenant brings his own net."

**2. mTLS is the isolation boundary; the tailnet is reachability-only.** Incus `:8443` is
mutual TLS; the restricted per-tenant cert already isolates tenants (reaching the port
grants nothing without a trusted cert). So the host Incus stays on `:8443` on all
interfaces — we do **not** firewall it per-tenant or bind it per-bridge. The tailnet
exists solely to give a no-public-IP tenant a *path* to the API.

**3. The Sidecar proxies Incus onto the Tenant Tailnet.** The sidecar runs, natively (no
extra packages):

    tailscale serve --bg --tcp=8443 tcp://<tenant-bridge-gateway>:8443

This raw-TCP-forwards the sidecar's own tailnet address to the host's Incus (TLS passes
through, so the host server cert is pinned end-to-end). Rejected: the **subnet-router**
approach (sidecar advertises the tenant CIDR, host Incus on the gateway) — it needs route
approval and a host address per tenant bridge, and doesn't compose with per-tenant tailnets.

**4. The client's Incus remote is the Sidecar's tailnet IP.** The auth-app reads the
sidecar's `tailscale ip -4` after it joins, returns it in the CLI Login Result, and
enrollment writes `https://<sidecar-tailnet-IP>:8443` as the remote address — instead of
the host's advertised LAN/bridge addresses. (The old host-address normalization produced a
malformed multi-address URL once `core.https_address` advertised every interface; it is
dropped.)

## Consequences

- **No platform coordinator, no public control endpoint** — simpler than Headscale; the
  cost/availability of a self-hosted coordinator disappears. Coordination is Tailscale's,
  under each tenant's own account.
- **Provisioning must stop injecting one shared Tailscale key.** Each tenant supplies their
  own key (`create-v2 --tailscale-authkey` / login). The shared `auth-app
  --tailscale-auth-key` is a **dev-only** convenience.
- The client must already be on the tenant's own tailnet to enroll — true by construction
  (BYO).
- **To wire (not yet implemented):** add the sidecar `tailscale serve` to provisioning;
  capture the sidecar tailnet IP into the Login Result + enrollment; remove the
  host-address remote-URL normalization.
- Isolation rests entirely on mTLS + restricted certs. If network-layer isolation between
  tenants is ever wanted, per-tenant tailnets already provide it (a tenant's client is only
  on their own net) — this decision does not preclude it.
