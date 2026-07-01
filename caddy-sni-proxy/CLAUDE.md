# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`caddy-sni-proxy` is a **self-contained, portable Caddy edge container**. It is a
generic recipe, **not wired into any host** — it is copied to whatever host should
own `:80`/`:443`. It lives inside the `sandcastle-incus` repo but is independent of
the Go project the parent `CLAUDE.md` documents; nothing here builds or imports Go.

Files: `Caddyfile` (the shared, portable config — single source of truth),
`launch.sh` + `caddy.service` (the recommended **Incus system container** path),
and `Dockerfile` + `compose.yaml` (Docker fallback for non-Incus hosts).

**Primary deployment = Incus CT.** `launch.sh` provisions an Incus system container
running Caddy natively under `systemd` (matching how the parent project runs its
`sc-caddy` infra container — see ADR-0016's native-Incus direction), rather than as
a Docker/OCI application container. It fetches the layer4-enabled binary from Caddy's
official download API (`caddyserver.com/api/download?...&p=github.com/mholt/caddy-l4`),
so the CT needs no Go/xcaddy toolchain. The same `Caddyfile` drives both the CT and
Docker paths.

It owns the public `:80` + `:443` sockets and does three things:
1. `http://` → `https://` redirect for every host (308).
2. **SNI passthrough** — forwards the raw, still-encrypted TLS stream to a backend
   that owns its own cert. Caddy never decrypts.
3. **ACME termination + reverse-proxy** — for other hostnames, terminates TLS with a
   Let's Encrypt cert and reverse-proxies to a plain-HTTP backend.

## Commands

Incus CT (primary):
- Provision: `ACME_EMAIL=you@example.com ./launch.sh [name]` (defaults: name `caddy-edge`, `IMAGE=images:debian/13`). Add `DATA_HOST_PATH=…` to back `/var/lib/caddy` with a host disk that survives CT deletion.
- Apply Caddyfile edits (no downtime): `incus file push Caddyfile <name>/etc/caddy/Caddyfile && incus exec <name> -- systemctl reload caddy`
- Validate: `incus exec <name> -- caddy validate --config /etc/caddy/Caddyfile`

Docker (fallback):
- Build + run: `docker compose up -d --build`
- Hot reload: `docker exec caddy-sni-proxy caddy reload --config /etc/caddy/Caddyfile`

Set the ACME contact via the `ACME_EMAIL` env var in either case.

## Architecture — the load-bearing constraint

Both the passthrough and terminate paths share the **single public `:443` socket**.
That split is only possible with the [`caddy-l4`](https://github.com/mholt/caddy-l4)
(`layer4`) plugin, which is why the `Dockerfile` does a **custom `xcaddy` build**
(`--with github.com/mholt/caddy-l4`) rather than using the stock `caddy:2` image. Any
change that touches SNI routing depends on this plugin being present.

The `layer4 { :443 }` block owns the public socket and routes by SNI:
- **passthrough SNIs** → `proxy` raw TLS to the external backend (which presents its own cert).
- **terminate SNIs** → `proxy` raw TLS to `127.0.0.1:8443`, where ordinary Caddy
  `https://…:8443` vhosts (each `bind 127.0.0.1`) decrypt and `reverse_proxy` to an
  internal HTTP backend.

`auto_https disable_redirects` is essential: it keeps ACME automation ON while
stopping Caddy from opening its own `:443` (which would fight `layer4`) or its own
auto-`:80` redirect (we do the redirect in the explicit `:80` block). The terminating
vhosts `bind 127.0.0.1` so they never contend for the public `:443`.

ACME still works despite the terminators being on localhost: HTTP-01 arrives on `:80`
(served by the redirect block automatically); TLS-ALPN-01 arrives on `:443` and
`layer4` routes it to `127.0.0.1:8443`.

## Editing the Caddyfile

**Adding a terminate host requires TWO edits** — this is the easiest thing to get
wrong: (1) add the name to the `@terminate` `sni` list inside the `layer4` block, AND
(2) add a matching `https://<name>:8443 { bind 127.0.0.1; reverse_proxy http://backend }`
vhost. Missing either one breaks that host silently.

Adding a passthrough host is one edit: add the name to the `@passthrough` `sni` list
and point its `route`'s `proxy { to … }` at a backend that speaks TLS.

Unmatched SNIs fall through and the connection closes; add a catch-all `route` in the
`layer4` block if you want a default backend.

## Gotchas

- **Persist `/data`** — it holds issued certs. Losing it re-triggers ACME issuance and
  can hit Let's Encrypt rate limits.
- Every terminate/passthrough hostname must resolve to this host's public IP, and
  `:80` + `:443` must be internet-reachable for ACME.
- Passthrough backends must present a valid cert themselves — Caddy cannot issue for them.
- The terminate path in this scaffold assumes **HTTP backends**; to re-encrypt to an
  HTTPS backend use `reverse_proxy https://backend` with `transport http { tls }`.
