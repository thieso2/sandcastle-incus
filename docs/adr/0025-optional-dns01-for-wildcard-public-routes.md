# Optional DNS-01 Certificates for Wildcard Public Routes

> Status: **proposed.** Extends ADR-0013 and the wildcard Public Route design
> delivered in PR #148.

## Context

A wildcard Public Route such as `*.jot.moyn.dev` currently authorizes Caddy to
issue one ordinary certificate for each concrete SNI name on its first HTTPS
request. That preserves self-service route publication without requiring DNS
credentials, but it makes a user's first visit to every new hostname wait for
an ACME order. It also consumes the certificate authority's per-registered-
domain issuance budget once per hostname.

Services that mint unbounded hostnames need a real wildcard certificate. The
Public Route must remain shared infrastructure under ADR-0013; moving public
TLS into each Tenant or Machine would reopen that boundary and complicate the
existing SNI-front topology.

## Decision

Keep broker-gated on-demand TLS as the default. Add an operator-scoped,
install-level Cloudflare DNS-01 option for wildcard Public Routes:

- `sc-adm install` and `sc-adm auth-app deploy` accept a Cloudflare API token
  plus one or more exact `--route-dns-cloudflare-wildcard` values only when
  route ingress is enabled. For example, the operator may authorize
  `*.jot.moyn.dev` without authorizing any other Tenant-supplied wildcard.
- The appliance downloads Caddy with `github.com/caddy-dns/cloudflare` only
  when that option is configured.
- The token is stored in a dedicated `0600` Caddy environment file. Caddy does
  not receive the Auth App's OAuth or login secrets, and the Auth App does not
  receive the DNS token.
- A leading-wildcard Public Route renders Cloudflare DNS-01 TLS only when its
  normalized Hostname exactly matches the operator allowlist. It then obtains
  one wildcard certificate during Caddy reconciliation.
- Unlisted wildcard Routes retain broker-gated on-demand TLS. Possession of a
  zone-scoped token therefore does not silently broaden a Tenant's DNS
  authority through an arbitrary Route publication.
- Exact-host Public Routes continue to use the existing on-demand-TLS ask gate.
- Removing the option on redeploy restores the existing per-SNI behavior and
  clears the Caddy-only token file.

Cloudflare is the first provider because the motivating installation's zone is
already there and Caddy DNS providers are compile-time modules. The configuration
names the provider explicitly so another provider can be added without changing
Public Route semantics.

## Consequences

- A new hostname covered by a configured wildcard Public Route has no first-hit
  ACME latency and does not create another certificate.
- Operators must grant a long-lived secret with only `Zone:Read` and `DNS:Edit`
  for the relevant zone, explicitly list every authorized wildcard, and repeat
  the secret on appliance redeploy.
- Default installations remain credential-free and behavior-compatible.
- A wildcard hostname is still published to Certificate Transparency once, but
  individual generated hostnames are not.
- Existing per-host certificates may remain in Caddy storage until normal
  cleanup; the wildcard certificate is selected for subsequent handshakes.

## Rejected alternatives

- **Prewarm every generated hostname.** Moves latency away from the viewer but
  retains per-host issuance, disclosure, and rate-limit pressure.
- **Terminate public TLS in the target Machine.** Conflicts with ADR-0013 and
  requires a second L4 routing layer plus backend certificate lifecycle.
- **Make DNS-01 mandatory for wildcard routes.** Breaks existing self-service
  installs and requires every operator to delegate DNS credentials.
