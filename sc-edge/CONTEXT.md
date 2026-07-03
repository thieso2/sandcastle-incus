# CONTEXT — sc-edge

Glossary for the portable **`sc-edge`** appliance (formerly "caddy-sni-proxy"): a
single, flexible edge for apps. Domain terms only — no implementation detail. See
the parent `../CONTEXT.md` for Sandcastle-wide vocabulary.

## sc-edge

The flexible edge appliance. **One appliance, two cooperating processes:** Caddy
(the routing brain — owns all ingress modes) and `cloudflared` (a dumb outbound
pipe for the tunnel mode). Operated as a single unit. The **Caddyfile is the one
hand-edited surface** for routing across all modes — `sc-edge` is not a config
generator/DSL; flexibility means one recipe that supports every mode at once, on
any host.

## Ingress modes

`sc-edge` supports three **co-equal** ways inbound traffic reaches an internal
backend. A given hostname uses exactly **one** mode; modes never overlap on the
same hostname.

- **SNI passthrough** — the raw, still-encrypted TLS stream is forwarded to a
  backend that owns its own certificate. Caddy never decrypts. Requires the host
  to own the public `:443` socket.

- **ACME terminate** — Caddy obtains a Let's Encrypt cert, terminates TLS itself,
  and reverse-proxies cleartext HTTP to an internal backend. Requires the host to
  own public `:80`/`:443` (HTTP-01 / TLS-ALPN-01 challenges).

- **Cloudflare tunnel** *(new)* — `cloudflared` dials **outbound** to the
  Cloudflare edge; nothing inbound is published (no host public IP, no `:80`/`:443`
  device). Cloudflare **terminates TLS at its edge** with a Cloudflare-managed cert
  and forwards traffic down the tunnel to a local backend. Trade accepted:
  Cloudflare sees plaintext (unlike passthrough). This is the "no public IP" mode.

  Shape: **one tunnel per container**, fanning out by hostname to **multiple**
  internal backends. Because nothing inbound is published, the container is no
  longer a public-IP singleton — it can be replicated across as many IP-less hosts
  as wanted. Escaping the single-public-IP constraint is the motivation.

  Data path: `cloudflared` (remotely-managed, one wildcard `*.domain` route) →
  `127.0.0.1:8080` plain-HTTP Caddy listener → per-host `reverse_proxy` to an
  internal HTTP backend. **Caddy stays the sole routing brain** — `cloudflared` is
  a dumb outbound pipe. Adding a tunneled host is a **one-edit** change (a single
  `:8080` vhost in the Caddyfile); the wildcard route means no `cloudflared` or
  dashboard change is needed.

  Coexistence: additive. One container may own public `:80`/`:443` (passthrough /
  terminate) **and** run the tunnel simultaneously; each mode activates from its
  own config. On an IP-less host the public-socket devices are simply inert. A
  given hostname still uses exactly one mode — passthrough and tunnel are mutually
  exclusive per host (the tunnel decrypts at Cloudflare; passthrough must not).
