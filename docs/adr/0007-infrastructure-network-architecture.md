# Infrastructure Network Architecture

Sandcastle infrastructure runs in a dedicated Incus project and attaches its sidecars to the host `incusbr0` bridge. The shared Caddy sidecar publishes host ports `80` and `443` through Incus proxy devices, while route broker and Auth App traffic stays on the bridge network.

## Considered Options

- Depend on DHCP and Incus IPAM leases for infrastructure sidecars.
- Use Incus network forwards on `incusbr0` for public HTTP and HTTPS ingress.
- Assign deterministic bridge addresses to infrastructure sidecars and publish public ingress with Caddy proxy devices.

## Decision

`sandcastle-admin infra create` provisions three infrastructure containers in the infrastructure project:

- `sc-caddy`: shared public reverse proxy.
- `sc-route-broker`: route mutation service.
- `sc-auth-app`: GitHub OAuth, admin UI, login allowlist, tenant access, and OIDC provider.

Each sidecar has an `eth0` bridged NIC on `incusbr0`. At creation time, Sandcastle reads the `incusbr0` IPv4 prefix from the default Incus project and derives deterministic infrastructure addresses from that prefix:

- Caddy uses host offset `20`.
- Route broker uses host offset `21`.
- Auth App uses host offset `22`.

Sandcastle writes a small systemd oneshot unit inside each sidecar to apply the static address and default route. It also writes `/etc/resolv.conf` with the `incusbr0` gateway as resolver. This avoids relying on DHCP behavior inside minimal base images while keeping DNS resolution available for Caddy ACME and service startup.

Public ingress is owned by the Caddy sidecar. The Caddy instance receives Incus proxy devices:

- Host `tcp:0.0.0.0:80` to container `tcp:127.0.0.1:80`.
- Host `tcp:0.0.0.0:443` to container `tcp:127.0.0.1:443`.

Caddy routes the configured Auth Hostname to the Auth App over the bridge network. The route broker and Auth App mount the host Incus socket by default, with `SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET` available only to override the host socket path; Caddy never receives the Incus socket.

## Consequences

- Public DNS for the Auth Hostname points at the Incus host, not at an infrastructure container address.
- No Incus network forward or IPAM reservation is required for the public Auth App path.
- Recreating infrastructure is deterministic as long as `incusbr0` keeps the same IPv4 prefix.
- Host ports `80` and `443` must be free before `infra create` can publish Caddy.
- Infrastructure sidecar addresses are implementation details and should not be used as user-facing API endpoints.
