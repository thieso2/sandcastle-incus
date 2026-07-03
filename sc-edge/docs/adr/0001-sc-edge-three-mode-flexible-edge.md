# sc-edge: a three-mode flexible edge (SNI passthrough + ACME terminate + Cloudflare tunnel)

Status: accepted

## Context

This recipe began as `caddy-sni-proxy`: a Caddy edge whose defining property was
**"Caddy never decrypts"** — SNI passthrough of raw TLS to backends that own their
certs, plus an ACME-terminate path, both sharing the single public `:443` via the
`caddy-l4` plugin. Both modes require the host to own public `:80`/`:443`, which
makes the edge a **public-IP singleton**.

We want an edge that can also serve apps on hosts with **no public IP**, using
Cloudflare's network to forward HTTPS inbound. We are renaming the recipe to
**`sc-edge`** and making it a single flexible edge with **three co-equal ingress
modes**: SNI passthrough, ACME terminate, and **Cloudflare tunnel**.

## Decision

Add a Cloudflare tunnel mode and rebrand the recipe as `sc-edge`.

- **Cloudflare terminates TLS.** For tunneled hosts, `cloudflared` dials
  **outbound** to Cloudflare (no public `:80`/`:443` needed); Cloudflare decrypts
  at its edge and forwards cleartext down the tunnel. This **deliberately breaks
  the "never decrypt" invariant for tunneled hosts** — it is the price of "no
  public IP," and is accepted. Passthrough and tunnel are therefore mutually
  exclusive per hostname.

- **Caddy stays the sole routing brain (`cloudflared` is a dumb pipe).**
  `cloudflared` uses one wildcard route (`*.domain → 127.0.0.1:8080`) to a
  plain-HTTP Caddy listener; **all** hostname→backend routing for **all three
  modes** lives in the one Caddyfile. Rejected: letting `cloudflared`'s own
  `config.yml` route straight to backends — it would split routing across two
  config brains and break single-source-of-truth.

- **Remotely-managed (token) tunnel.** The container needs only a
  `CLOUDFLARE_TUNNEL_TOKEN` (symmetric with `ACME_EMAIL`); the tunnel object and
  its single wildcard route are created once in the Cloudflare dashboard. Rejected:
  locally-managed `config.yml` + credentials file — more secret-handling and moving
  parts for no benefit, since the wildcard makes local ingress config trivial.

- **One appliance, two processes.** Caddy has no native tunnel support, so
  `sc-edge` runs Caddy + `cloudflared` as two cooperating processes (two systemd
  units in the CT; two services in Docker with `network_mode: "service:caddy"` so
  the tunnel reaches Caddy's `bind 127.0.0.1` listener). Operated as one unit.

- **Additive / hybrid (ii).** One container may own public `:80`/`:443` **and** run
  the tunnel simultaneously; each mode activates from its own config, and the tunnel
  installs only when a token is present. On an IP-less host the public-socket
  devices are inert.

- **Caddyfile-as-surface, no generator.** "Flexible" means one recipe supporting
  every mode at once on any host, hand-edited in the Caddyfile — **not** a
  declarative per-app config DSL. A generator, if ever wanted, belongs in the
  parent `sc`/`route` tooling, not in this portable recipe.

- **Scope.** Ship both deploy paths (Incus CT + Docker) at parity. Rename at the
  "directory + defaults" level (`caddy-sni-proxy → sc-edge`, default container name
  `sc-edge`).

## Consequences

- Tunneled hosts get a **one-edit** add (a single `:8080` vhost); the wildcard route
  means no `cloudflared` or dashboard change — unlike the terminate path's two-edit
  tax (SNI list + vhost).
- The recipe is no longer 100% file-complete: the tunnel + its wildcard route are
  bootstrapped once in the Cloudflare dashboard (a paste-the-token step).
- **Deferred (separate decision):** whether `sc-edge` supersedes the v1 `sc-caddy`
  infra container, and how the Cloudflare tunnel relates to the v2 "no public app
  ingress via per-tenant Headscale tailnets" plan — Cloudflare Tunnel is arguably a
  *rival* ingress strategy to Headscale, and reconciling them is out of scope here.
