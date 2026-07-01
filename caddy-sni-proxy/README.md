# caddy-sni-proxy

A portable, self-contained Caddy edge container that owns `:80` + `:443` and does
three things:

1. **`http://` → `https://` redirect** for every host (permanent 308).
2. **SNI-based TLS passthrough** — forwards the *raw, still-encrypted* TLS stream
   to a backend that owns its own certificate. Caddy never sees plaintext.
3. **Let's Encrypt termination + reverse-proxy** — for other hostnames Caddy
   obtains/renews an ACME cert, terminates TLS, and reverse-proxies to a plain
   **HTTP** backend on your internal network.

Both (2) and (3) share the single public `:443` socket. That split is only
possible with the [`caddy-l4`](https://github.com/mholt/caddy-l4) (`layer4`)
plugin, so this image is a **custom Caddy build** (see `Dockerfile`).

> This is a generic recipe — it is not wired into the `big` host (where the
> `sc-caddy` container already owns 80/443). Copy the directory to whatever host
> should run it. The recommended deployment is an **Incus system container (CT)**
> running Caddy under `systemd` — see [`launch.sh`](launch.sh); Docker is
> provided as a fallback for non-Incus hosts.

---

## How it works

```
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
```

The terminating vhosts listen **only** on `127.0.0.1:8443` (`bind 127.0.0.1`), so
they never fight `layer4` for the public `:443`. `auto_https disable_redirects`
keeps ACME cert automation on while stopping Caddy from opening its own `:443` or
auto-`:80` redirect.

**ACME issuance still works** even though the terminating servers are on localhost:
- *HTTP-01* arrives on `:80` → the redirect server serves the challenge automatically.
- *TLS-ALPN-01* arrives on `:443` with SNI = your domain → `layer4` routes it to
  `127.0.0.1:8443` where Caddy answers.

---

## Configure

Edit **`Caddyfile`**. Everything you change lives in three places:

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

Set your Let's Encrypt contact email via the `ACME_EMAIL` env var (or edit the
`email` line directly).

---

## Build & run

### Incus system container — recommended

Runs Caddy natively under `systemd` inside an Incus **CT** (no Docker). The
layer4-enabled binary is fetched from Caddy's official download API, so the CT
needs no Go/xcaddy toolchain. `launch.sh` provisions everything and pushes this
directory's `Caddyfile` to `/etc/caddy/Caddyfile`.

```bash
# defaults: name=caddy-edge, IMAGE=images:debian/13, ACME_EMAIL=you@example.com
ACME_EMAIL=you@example.com ./launch.sh caddy-edge

# certs that survive deleting/recreating the CT (rootfs already persists across
# restarts without this — see "Persisting ACME certs" below):
DATA_HOST_PATH=/srv/caddy-edge/data ACME_EMAIL=you@example.com ./launch.sh caddy-edge
```

Apply later `Caddyfile` edits with no downtime:
```bash
incus file push Caddyfile caddy-edge/etc/caddy/Caddyfile
incus exec caddy-edge -- caddy validate --config /etc/caddy/Caddyfile
incus exec caddy-edge -- systemctl reload caddy
```

### Docker Compose (non-Incus hosts)
```bash
docker compose up -d --build
docker compose logs -f caddy
```

### Plain Docker
```bash
docker build -t caddy-sni-proxy .
docker run -d --name caddy-sni-proxy --restart unless-stopped \
  -p 80:80 -p 443:443 \
  -e ACME_EMAIL=you@example.com \
  -v caddy_data:/data -v caddy_config:/config \
  -v "$PWD/Caddyfile:/etc/caddy/Caddyfile:ro" \
  caddy-sni-proxy
```

### Validate config before deploying
```bash
# CT:
incus exec caddy-edge -- caddy validate --config /etc/caddy/Caddyfile
# Docker (needs the l4-enabled binary from this image):
docker run --rm -v "$PWD/Caddyfile:/etc/caddy/Caddyfile:ro" \
  caddy-sni-proxy caddy validate --config /etc/caddy/Caddyfile
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
- **HTTP (non-TLS) backends only** for the terminate path in this scaffold. To
  re-encrypt to an HTTPS backend, use `reverse_proxy https://backend` and add
  `transport http { tls }` as needed.
