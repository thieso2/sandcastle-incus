# sc-edge

*(formerly `caddy-sni-proxy`)*

A portable, self-contained **edge appliance** — one flexible edge for your apps.
**One appliance, two cooperating processes:** Caddy (the routing brain; the
`Caddyfile` is the single source of truth) and `cloudflared` (a dumb outbound pipe,
present only when you enable the tunnel).

It offers **three co-equal ingress modes** (each hostname uses exactly one):

1. **SNI-based TLS passthrough** — forwards the *raw, still-encrypted* TLS stream
   to a backend that owns its own certificate. Caddy never sees plaintext. Needs
   public `:443`.
2. **Let's Encrypt termination + reverse-proxy** — Caddy obtains/renews an ACME
   cert, terminates TLS, and reverse-proxies to a plain **HTTP** backend. Needs
   public `:80`/`:443`.
3. **Cloudflare tunnel** — `cloudflared` dials **outbound** to Cloudflare, so **no
   public IP is required**. Cloudflare terminates TLS at its edge and forwards
   cleartext down the tunnel to Caddy, which reverse-proxies to a plain **HTTP**
   backend. Trade: Cloudflare sees plaintext (so it is mutually exclusive with
   passthrough per host).

Plus, for the public-IP modes: **`http://` → `https://` redirect** for every host
(permanent 308).

Modes (1) and (2) share the single public `:443` socket. That split is only
possible with the [`caddy-l4`](https://github.com/mholt/caddy-l4) (`layer4`)
plugin, so this image is a **custom Caddy build** (see `Dockerfile`). Modes are
**additive**: one appliance can own `:80`/`:443` *and* run the tunnel at once; on a
host with no public IP, run tunnel-only (`PUBLIC_PORTS=0`).

> This is a generic recipe — it is not wired into the `big` host (where the
> `sc-caddy` container already owns 80/443). Copy the directory to whatever host
> should run it. The recommended deployment is an **Incus system container (CT)**
> running under `systemd` — see [`launch.sh`](launch.sh); Docker is provided as a
> fallback for non-Incus hosts. See `CONTEXT.md` and
> `docs/adr/0001-sc-edge-three-mode-flexible-edge.md` for the model and rationale.

---

## How it works

```
  PUBLIC-IP MODES (1 & 2)
                         :80  ──► redir https://{host}{uri}
                                  (also serves ACME HTTP-01 challenges)

 client ──TLS──► :443  ─┤ layer4 (caddy-l4): read ClientHello SNI, do NOT decrypt
                        │
                        ├─ SNI ∈ passthrough set ─► proxy raw TLS ─► backend:443
                        │                                            (backend owns cert)
                        │
                        └─ SNI ∈ terminate set   ─► proxy raw TLS ─► 127.0.0.1:8443
                                                                     │ Caddy https server
                                                                     │ (Let's Encrypt cert)
                                                                     └─► reverse_proxy
                                                                         http://backend:port

  TUNNEL MODE (3) — no public IP
 client ─TLS─► Cloudflare edge ─(QUIC, outbound)─► cloudflared ─► 127.0.0.1:8080
        (CF terminates TLS)                                       │ Caddy http server
                                                                  └─► reverse_proxy
                                                                      http://backend:port
```

The terminating vhosts listen **only** on `127.0.0.1:8443` (`bind 127.0.0.1`), so
they never fight `layer4` for the public `:443`. `auto_https disable_redirects`
keeps ACME cert automation on while stopping Caddy from opening its own `:443` or
auto-`:80` redirect.

**ACME issuance still works** even though the terminating servers are on localhost:
- *HTTP-01* arrives on `:80` → the redirect server serves the challenge automatically.
- *TLS-ALPN-01* arrives on `:443` with SNI = your domain → `layer4` routes it to
  `127.0.0.1:8443` where Caddy answers.

**The tunnel uses one wildcard route.** `cloudflared` runs a *remotely-managed*
tunnel (dashboard-configured) whose single wildcard public hostname
`*.your-domain → http://127.0.0.1:8080` delivers **all** tunneled traffic to one
plain-HTTP Caddy listener; Caddy routes by `Host`. So `cloudflared` holds no
per-host config, and adding a tunneled host is a **one-edit** change (below).

---

## Configure

Edit **`Caddyfile`**. Each ingress mode is configured as follows:

### 1. Passthrough hosts (backend terminates its own TLS)
```caddyfile
@passthrough tls {
    sni passthrough.example.com another-passthrough.example.com
}
route @passthrough {
    proxy {
        to 10.0.0.10:443     # backend must speak TLS here
    }
}
```

### 2. Terminate hosts — add the name to the SNI list
```caddyfile
@terminate tls {
    sni app.example.com api.example.com
}
```

### 3. Terminate hosts — add a vhost pointing at the internal HTTP backend
```caddyfile
https://app.example.com:8443 {
    bind 127.0.0.1
    reverse_proxy http://10.0.0.20:8080
}
```

### 4. Tunnel hosts (Cloudflare) — ONE edit
Add just a plain-HTTP vhost on `127.0.0.1:8080`. The wildcard tunnel route already
covers the name, so there is **no** `cloudflared` or dashboard change:
```caddyfile
http://tunnelapp.example.com:8080 {
    bind 127.0.0.1
    reverse_proxy http://10.0.0.30:8080
}
```
One-time setup (in the Cloudflare Zero-Trust dashboard): create a tunnel, add a
public hostname `*.your-domain` → service `http://127.0.0.1:8080`, and copy its
token. Pass that token as `CLOUDFLARE_TUNNEL_TOKEN` when launching (below).

Set your Let's Encrypt contact email via the `ACME_EMAIL` env var (or edit the
`email` line directly), and the Cloudflare tunnel token via
`CLOUDFLARE_TUNNEL_TOKEN`.

---

## Build & run

### Incus system container — recommended

Runs Caddy natively under `systemd` inside an Incus **CT** (no Docker). The
layer4-enabled binary is fetched from Caddy's official download API, so the CT
needs no Go/xcaddy toolchain. `launch.sh` provisions everything and pushes this
directory's `Caddyfile` to `/etc/caddy/Caddyfile`.

```bash
# defaults: name=sc-edge, IMAGE=images:debian/13, ACME_EMAIL=you@example.com
ACME_EMAIL=you@example.com ./launch.sh sc-edge

# certs that survive deleting/recreating the CT (rootfs already persists across
# restarts without this — see "Persisting ACME certs" below):
DATA_HOST_PATH=/srv/sc-edge/data ACME_EMAIL=you@example.com ./launch.sh sc-edge

# with the Cloudflare tunnel (mode 3). On a host with NO public IP, add
# PUBLIC_PORTS=0 so it skips binding :80/:443 and serves apps only via the tunnel:
CLOUDFLARE_TUNNEL_TOKEN=eyJ... ACME_EMAIL=you@example.com ./launch.sh sc-edge
CLOUDFLARE_TUNNEL_TOKEN=eyJ... PUBLIC_PORTS=0 ./launch.sh sc-edge
```

Apply later `Caddyfile` edits with no downtime:
```bash
incus file push Caddyfile sc-edge/etc/caddy/Caddyfile
incus exec sc-edge -- caddy validate --config /etc/caddy/Caddyfile
incus exec sc-edge -- systemctl reload caddy
```

### Docker Compose (non-Incus hosts)
```bash
# Set CLOUDFLARE_TUNNEL_TOKEN in your env/.env to enable the tunnel service;
# omit it (and the cloudflared service just idles) to run public-IP modes only.
docker compose up -d --build
docker compose logs -f caddy
```

### Plain Docker
```bash
docker build -t sc-edge .
docker run -d --name sc-edge --restart unless-stopped \
  -p 80:80 -p 443:443 \
  -e ACME_EMAIL=you@example.com \
  -v caddy_data:/data -v caddy_config:/config \
  -v "$PWD/Caddyfile:/etc/caddy/Caddyfile:ro" \
  sc-edge
# For the tunnel, also run cloudflared sharing this container's network:
docker run -d --name sc-edge-tunnel --restart unless-stopped \
  --network "container:sc-edge" \
  -e TUNNEL_TOKEN=eyJ... \
  cloudflare/cloudflared:latest tunnel --no-autoupdate run
```

### Validate config before deploying
```bash
# CT:
incus exec sc-edge -- caddy validate --config /etc/caddy/Caddyfile
# Docker (needs the l4-enabled binary from this image):
docker run --rm -v "$PWD/Caddyfile:/etc/caddy/Caddyfile:ro" \
  sc-edge caddy validate --config /etc/caddy/Caddyfile
```

---

## Requirements & gotchas

- **DNS**: every terminate/passthrough hostname must resolve to this host's
  public IP.
- **Public reachability**: ports **80 and 443** must be reachable from the
  internet for ACME to succeed.
- **Passthrough backends own their certs** — Caddy does not and cannot issue
  certs for passthrough hosts; the backend on `:443` must present a valid cert.
- **Persisting ACME certs** — losing issued certs re-triggers issuance and can
  hit Let's Encrypt [rate limits](https://letsencrypt.org/docs/rate-limits/).
  - *Incus CT*: Caddy stores certs under `/var/lib/caddy`, which lives on the
    CT's persistent rootfs — they already survive restarts and reboots. They are
    only lost if you **delete** the CT; pass `DATA_HOST_PATH=…` to `launch.sh` to
    back `/var/lib/caddy` with a host disk device that outlives the CT.
  - *Docker*: persist the `/data` volume (compose already declares `caddy_data`).
- **Unmatched SNIs** fall through and the connection closes. Add a catch-all
  `route` in the `layer4` block if you want a default backend.
- **HTTP (non-TLS) backends only** for the terminate and tunnel paths in this
  scaffold. To re-encrypt to an HTTPS backend, use `reverse_proxy https://backend`
  and add `transport http { tls }` as needed.
- **Tunnel mode** does *not* need public `:80`/`:443` or DNS pointing at this host
  — Cloudflare fronts it. It *does* require: a remotely-managed tunnel + a wildcard
  `*.domain → http://127.0.0.1:8080` public-hostname route created once in the
  Cloudflare dashboard, and the tunnel token passed as `CLOUDFLARE_TUNNEL_TOKEN`
  (a secret — stored `600` at `/etc/default/cloudflared`, never committed).
  Cloudflare terminates TLS, so a tunneled host **cannot** also be a passthrough
  host and does not use ACME.
- **Tunnel edge-cert depth (learned the hard way).** Because Cloudflare terminates
  TLS, the *edge* needs a cert for your hostname — the origin/Caddy cert is
  irrelevant. Cloudflare's free **Universal SSL only covers the apex and the
  first level** of subdomains (`example.com`, `*.example.com` / `app.example.com`).
  A **2-level** name like `app.sc2.example.com` — or a wildcard `*.sc2.example.com`
  — is **not** covered: the TLS handshake at the edge fails (`SSL handshake
  failure`) even though DNS resolves and the tunnel is healthy. Options for deeper
  names: (1) keep names **first-level** (`app.example.com`) — free; (2) buy
  **Advanced Certificate Manager** (~$10/mo) + Total TLS to cover deeper wildcards;
  (3) an Enterprise-only **subdomain zone**. Also note: adding a **wildcard**
  public hostname does **not** auto-create its DNS record (you must add the proxied
  `*.sub` CNAME → `<tunnel-id>.cfargotunnel.com` manually), whereas a **specific**
  public hostname **does** auto-create DNS.
