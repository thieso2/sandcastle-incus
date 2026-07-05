# Handoff — Sandcastle e2e behind a Cloudflare tunnel (`hello.thieso2.dev`)

Goal: stand up the full **Sandcastle v2 (sc2)** stack — `sc-edge`, `auth-app`, `broker`,
first tenant — on an **IP-less host**, with the **only public surface** being the Auth
Hostname **`hello.thieso2.dev`**, served through **`sc-edge`'s Cloudflare-tunnel mode**
instead of a public-IP Let's Encrypt edge.

This is the standard [`INSTALL.md`](../INSTALL.md) flow with **one substitution**: the
public ingress. Read `INSTALL.md` for the canonical per-step detail; this doc records
only what changes for the tunnel, plus the host-specific facts for `vm-thieso`.

> **Why this shape.** No public IP, nothing inbound published — Cloudflare terminates
> TLS at its edge and forwards down an outbound tunnel to Caddy. Exposing only the
> auth/control endpoint publicly (not tenant app data) is consistent with the v2
> "no public app ingress" direction; tenant traffic still rides per-tenant tailnets.

---

## 0. Assumptions (change these if wrong)

- **Host:** `vm-thieso` = `10.248.1.14`, Incus 7.2, reachable by passwordless SSH from
  the workstation; operator `thies` is in `incus-admin`. Admin commands run **on the
  host** over SSH (local Incus admin socket), or from a workstation with an admin remote
  `vm-thieso:` — pick one and stay consistent.
- **`sc-edge` is already deployed** on this host, **tunnel-only** (`PUBLIC_PORTS=0`), and
  `https://hello.thieso2.dev` already reaches Caddy end-to-end through the tunnel (proven
  against a throwaway test backend). See [`../sc-edge/`](../sc-edge/) and the memory note
  `sc-edge-vm-thieso-deployment`. This handoff **repoints** that hostname from the test
  backend to the real **auth-app**.
- **Auth Hostname = `hello.thieso2.dev`** (first-level under `thieso2.dev` — required so
  free Cloudflare Universal SSL covers it; 2-level names need ACM — see §Gotchas).

---

## 1. Prerequisites (the human/external pieces)

1. **GitHub auth.** Two choices:
   - **Simulated (recommended for this e2e) — no OAuth app.** Pick a shared secret and
     pass `--simulate-github-token <secret>` to the auth-app deploy. The appliance then
     fabricates GitHub logins offline; `--github-client-id/secret` are not needed. Log in
     with `sc login <auth-host> --simulate-token <secret> --as <username>`. **Dev/e2e only.**
   - **Real OAuth app.** Create a GitHub OAuth App with callback URL
     `https://hello.thieso2.dev/oauth/github/callback` (note: `/oauth/…`, not `/login/…`);
     record client id + secret.
2. **Tailscale auth key** (reusable/ephemeral). Optional — omit it and provisioning
   prints an interactive `tailscale up` login URL instead (see `INSTALL.md` §6).
3. **Cloudflare tunnel** — already up on `vm-thieso` (token in `/etc/default/cloudflared`,
   `600`). You only need to ensure a **Public Hostname** exists:
   `hello.thieso2.dev → http://127.0.0.1:8080` (Type HTTP). A *specific* first-level
   hostname auto-creates its DNS + is covered by Universal SSL. **Already added.**
4. **Fat binary, static Linux**, and **base image cached on the host**:
   ```bash
   # on the workstation
   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 BIN_DIR=bin/linux-amd64 make build
   scp bin/linux-amd64/sandcastle 10.248.1.14:sandcastle           # for sc-adm --binary
   # on vm-thieso: cache a CONTAINER-type systemd image (not OCI)
   ssh 10.248.1.14 'incus image copy images:debian/13 local: --project default'
   ```

> **CIDR warning up front (this host bites here).** `vm-thieso` lives on **`10.248.1.14`**,
> so the auth-app's **default `--cidr-pool 10.248.0.0/16` WILL clash** with the host
> network and break provisioning (`dnsmasq: Address already in use`). Choose clean,
> non-overlapping `/16`s below. See §7 of `INSTALL.md`.

---

## 2. `sc-edge` — already up; just confirm

```bash
ssh 10.248.1.14 '
  incus exec sc-edge -- systemctl is-active caddy cloudflared
  incus exec sc-edge -- journalctl -u cloudflared -n3 --no-pager | grep -i "Registered tunnel connection"'
```
Both `active`, connections `Registered`. If `sc-edge` does not exist, deploy it first:
`CLOUDFLARE_TUNNEL_TOKEN=… PUBLIC_PORTS=0 ./launch.sh sc-edge` (see `../sc-edge/README.md`).

---

## 3. Deploy the auth-app

Runs `auth-app serve` on `:9444` inside its own appliance (no host port — fronted by
`sc-edge`). **Pick a clean CIDR pool — NOT `10.248.x`.**

```bash
ssh 10.248.1.14 '
  ./sandcastle-admin auth-app deploy \
    --auth-hostname          hello.thieso2.dev \
    --simulate-github-token  "'"$SIMULATE_TOKEN"'" \
    --admin-github-users     <your-gh-username> \
    --tailscale-auth-key     "'"$TAILSCALE_AUTH_KEY"'" \
    --base-image  images:debian/13 \
    --binary      /home/thies/sandcastle \
    --cidr-pool   10.250.0.0/16'
```
> Simulated-GitHub mode: no OAuth app, so `--github-client-id/secret` are omitted.
> For a real OAuth app instead, drop `--simulate-github-token` and pass
> `--github-client-id/--github-client-secret`.
> `./sandcastle-admin` = the fat binary invoked under its admin name. Adjust project
> flags (`--project …`) only if your deploy targets a non-`default` project; `vm-thieso`
> has only `default`. Verify: `incus exec <auth-app-ct> -- systemctl is-active sandcastle-auth-app`.

---

## 4. Front the auth-app on `sc-edge` (the tunnel delta)

**This is the substitution.** Standard `INSTALL.md` adds an ACME `terminate` vhost. Here,
Cloudflare already terminates TLS, so we add a **plain-HTTP `:8080` tunnel vhost** that
reverse-proxies to the auth-app's bridge IP, and remove the throwaway test backend.

```bash
ssh 10.248.1.14 '
  AUTH_IP=$(incus exec <auth-app-ct> -- ip -4 -o addr show eth0 | grep -oE "10\.[0-9.]+" | head -1)
  # Replace the sc-edge Caddyfile app section with the auth-app front:
  cat > /tmp/edge.caddy <<EOF
{
    auto_https off
}
http://hello.thieso2.dev:8080 {
    bind 127.0.0.1
    reverse_proxy http://$AUTH_IP:9444
}
http://:8080 {
    bind 127.0.0.1
    respond "no such host" 404
}
EOF
  incus file push /tmp/edge.caddy sc-edge/etc/caddy/Caddyfile
  incus exec sc-edge -- caddy validate --config /etc/caddy/Caddyfile
  incus exec sc-edge -- systemctl reload caddy'
```
The `:9000` test backend and its vhost are intentionally dropped.

---

## 5. Verify the public path (through Cloudflare)

```bash
curl -s -o /dev/null -w '%{http_code}\n' https://hello.thieso2.dev/healthz   # 200
# simulated login mints a session (200) with no OAuth app:
curl -s -o /dev/null -w '%{http_code}\n' \
  "https://hello.thieso2.dev/oauth/github/simulate?token=$SIMULATE_TOKEN&username=<your-gh-username>"   # 200
```
- **200 on `/healthz`** proves CF edge → cloudflared → Caddy → auth-app.
- **200 on `/oauth/github/simulate`** proves simulated auth is live and gated by the token
  (a wrong token returns 403; an unset token returns 404).

Self-service login from any workstation (offline, no browser):
```bash
sc login hello.thieso2.dev --simulate-token "$SIMULATE_TOKEN" --as <your-gh-username>
```

---

## 6. Broker + first tenant

Unchanged from `INSTALL.md` §5–§6 — **just keep CIDR pools clean of `10.248.x`** and give
the broker a **distinct** `/16` from the auth-app:

```bash
ssh 10.248.1.14 './sandcastle-admin bootstrap \
  --hostname hello.thieso2.dev --base-image images:debian/13 \
  --binary /home/thies/sandcastle --cidr-pool 10.249.0.0/16'

# first tenant (admin path, no browser):
ssh 10.248.1.14 './sandcastle-admin tenant create alice \
  --sidecar-image images:debian/13 --ssh-key "<pubkey>" --tailscale-authkey "$TAILSCALE_AUTH_KEY"'
# prints token=… ; then on the workstation:  sc enroll alice --token <token>
```
Tenant machines: `incus launch images:debian/13/cloud <name> --project sc2-alice-default`.

---

## 7. Adding a public app later (tunnel workflow)

Each extra public hostname is **2 edits**, all free, all first-level:
1. **Cloudflare** → tunnel → Public Hostname → Add `NAME.thieso2.dev → http://127.0.0.1:8080`
   (auto-creates DNS; Universal SSL covers first-level).
2. **`sc-edge` Caddyfile** → add
   `http://NAME.thieso2.dev:8080 { bind 127.0.0.1; reverse_proxy http://<backend-ip>:<port> }`,
   then `incus exec sc-edge -- systemctl reload caddy`.

Deep names (`x.sub.thieso2.dev`) and wildcards (`*.sub.thieso2.dev`) need **ACM (~$10/mo) +
Total TLS** — free Universal SSL is first-level only.

---

## 8. Gotchas (tunnel-specific, on top of `INSTALL.md` §8)

- **Edge-cert depth.** Cloudflare terminates TLS, so the *edge* needs the cert. Free
  Universal SSL = apex + **first level only**. 2-level / wildcard names fail the TLS
  handshake at the edge even when DNS + tunnel are healthy. Keep public names first-level.
- **CIDR vs host `10.248.x`.** The host is `10.248.1.14`; never leave a `--cidr-pool` in
  `10.248.0.0/16`. Broker + auth-app must use distinct, clean `/16`s (e.g. `10.249`,
  `10.250`). Check: `incus network list --project default -c n4 --format csv | grep bridge`.
- **Forwarded scheme.** The CF→Caddy→auth-app internal hops are plain HTTP. Confirm the
  auth-app emits `https://hello.thieso2.dev/...` in OAuth redirects (from the Auth
  Hostname config, not request scheme). Verify in §5.
- **No public `:80`/`:443`.** ACME HTTP-01/TLS-ALPN-01 do **not** apply here — issuance is
  Cloudflare's job. Don't add `terminate`/`passthrough` vhosts on this IP-less host.
- **Test backend removed.** The `:9000` scaffolding from the initial tunnel proof is gone
  after §4; if you kept it, delete its vhost to avoid confusion.

---

## What you end up with

```
vm-thieso (10.248.1.14, NO public IP)
├── sc-edge        Caddy + cloudflared (tunnel-only); Caddyfile routes :8080 by Host
│                    └─ cloudflared ⇄ Cloudflare edge  (hello.thieso2.dev, TLS at edge)
├── <auth-app-ct>  auth-app :9444   ← fronted by sc-edge :8080 tunnel vhost
├── <broker-ct>    broker :9443     (admin provisioning plane)
└── sc2-<tenant> / sc2-<tenant>-default
    ├── sidecar    CoreDNS + Tailscale on the per-tenant bridge
    └── <machines> native Incus CTs/VMs

Public surface = exactly one hostname (hello.thieso2.dev) via the outbound tunnel.
```

## Open items for the operator

- Fill in the concrete **appliance CT names** (`<auth-app-ct>`, `<broker-ct>`) — the deploy
  commands print them; this doc leaves them as placeholders.
- Decide **workstation-remote vs on-host** operation and make it uniform.
- Confirm the **auth-app https-scheme** behaviour in §5 (only real integration unknown).
- If you later want the `*.sc2.thieso2.dev` wildcard / one-edit workflow, enable ACM and
  switch `sc-edge` back to the wildcard route (see `sc-edge` README + ADR-0001).
```
