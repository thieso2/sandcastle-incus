# Installing Sandcastle (sc2) on a fresh Incus host

This guide stands up the Sandcastle **v2 (sc2)** stack on a fresh
[Incus](https://linuxcontainers.org/incus/) host: the **sc-edge** TLS edge, the
**auth-app** (login + OIDC + provisioning), and the **broker** (admin provisioning
plane). Tenants then run as native Incus projects; their machines are launched with
plain `incus launch`.

It is the distilled, install-only path. For the full end-to-end **validation**
runbook (with PASS criteria for every phase) see [`docs/e2e-sc2.md`](docs/e2e-sc2.md);
for design background see `docs/adr/0016-*` and `CONTEXT.md`.

> All commands run from a workstation whose **admin** Incus remote points at the host
> (referred to as `big:` below), or directly on the host. Admin commands use the
> global `~/.config/incus/` admin certificate — not the per-tenant restricted certs.

---

## 1. Prerequisites

- **A fresh Incus host** with a storage pool (`default`) and a bridge (`incusbr0`),
  reachable as an admin Incus remote. Verify: `incus info big:` returns server config.
- **Public DNS + reachable `:80`/`:443`.** Pick an **Auth Hostname** (e.g.
  `sc2.example.dev`) that resolves publicly to the host's public IP. `:80` and `:443`
  must be internet-reachable so Let's Encrypt (ACME HTTP-01 / TLS-ALPN-01) can issue.
  A wildcard like `*.apps.example.dev` → the host IP lets you expose tenant apps later.
- **A GitHub OAuth App** (for login). Set its **Authorization callback URL** to
  `https://<auth-hostname>/oauth/github/callback`. Note the **client id** + **secret**.
  - **Testing shortcut — no OAuth app needed.** Pass `--simulate-github-token <secret>`
    to `auth-app deploy`/`serve` to run in **simulated-GitHub mode**: the appliance
    fabricates logins offline (any username), gated by that shared secret, and
    `--github-client-id`/`--github-client-secret` become optional. Log in with
    `sc login <auth-host> --simulate-token <secret> --as <username>`. **Dev/e2e only —
    never enable in production** (it will "authenticate" anyone who has the token).
- **A Tailscale auth key** (reusable/ephemeral) — handed to tenant sidecars and to
  approved device logins so they join the tailnet non-interactively. **Optional:** if
  you omit it when creating a tenant, provisioning instead prints a `tailscale up`
  **login URL** you open in a browser to register that sidecar yourself (see §6).
- **A stock systemd base image cached on the host.** Use a **container-type** Debian
  image (`images:debian/13`), *not* an OCI/app image — appliances need systemd as PID 1:
  ```bash
  # Optional now — appliance/sidecar launches pull `images:debian/13` from the
  # public remote on demand (imageInstanceSource). Pre-cache only to avoid repeat
  # pulls / for offline hosts:
  incus image copy images:debian/13 big: --project default
  ```
  The tenant **infra** projects share this `default` image store (`features.images=false`),
  so the sidecar base must live here.
- **The fat binary, built static for Linux.** One binary provides `sc` / `sc-adm`; it is
  copied into every appliance:
  ```bash
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 BIN_DIR=bin/linux-amd64 make build
  # produces bin/linux-amd64/{sandcastle,sc,sc-adm,sandcastle-admin}
  ```

---

## 2. Deploy `sc-edge` — the TLS edge that owns `:80`/`:443`

`sc-edge` is the portable edge appliance from [`sc-edge/`](sc-edge/). It runs Caddy
natively under systemd in an Incus system container, does `http→https` redirect, SNI
passthrough, and ACME-terminating reverse-proxy. It is the **only** thing that binds the
public `:80`/`:443`; the auth-app and tenant apps sit behind it on the bridge. (It can
also front apps via a Cloudflare tunnel with no public IP — set `CLOUDFLARE_TUNNEL_TOKEN`;
see [`sc-edge/README.md`](sc-edge/README.md). Not used on this public-IP reference host.)

```bash
cd sc-edge
# Back /var/lib/caddy with a host path so issued certs survive CT deletion (avoids
# hammering Let's Encrypt rate limits on rebuild).
ACME_EMAIL=you@example.com DATA_HOST_PATH=/srv/caddy-data ./launch.sh sc-edge
cd ..
```

Move it into the `infrastructure` project if you keep appliances there (optional but
matches the reference host), and confirm Caddy is up:

```bash
incus exec big:sc-edge --project infrastructure -- systemctl is-active caddy   # active
incus exec big:sc-edge --project infrastructure -- caddy validate --config /etc/caddy/Caddyfile
```

> **Persist `/data`.** It holds issued certs. Losing it re-triggers ACME and can hit
> Let's Encrypt rate limits — that's what `DATA_HOST_PATH` is for.

---

## 3. Deploy the auth-app appliance

Creates a system container, copies **this** fat binary in, and runs `auth-app serve`
under systemd on `:9444` (no host port — fronted by sc-edge). Uses the global admin
socket for provisioning.

```bash
sc-adm auth-app deploy \
  --auth-hostname   sc2.example.dev \
  --github-client-id     "$GH_CLIENT_ID" \
  --github-client-secret "$GH_CLIENT_SECRET" \
  --admin-github-users   yourgithubname \
  --tailscale-auth-key   "$TAILSCALE_AUTH_KEY" \
  --base-image  images:debian/13 \
  --binary      bin/linux-amd64/sandcastle \
  --cidr-pool   10.250.0.0/16          # see CIDR note in §7 — do NOT leave the default
```

Verify:
```bash
incus exec big:sc2-auth-app --project infrastructure -- systemctl is-active sandcastle-auth-app  # active
```

---

## 4. Front the auth-app on `sc-edge`

Add a terminate vhost so `https://<auth-hostname>` reverse-proxies to the auth-app's
bridge IP on `:9444`, then reload Caddy (no downtime):

```bash
AUTH_IP=$(incus exec big:sc2-auth-app --project infrastructure -- \
  ip -4 -o addr show eth0 | grep -oE '10\.[0-9.]+' | head -1)
incus exec big:sc-edge --project infrastructure -- bash -c "
  grep -q 'sc2.example.dev' /etc/caddy/Caddyfile || \
    printf '\n%s {\n    reverse_proxy http://%s:9444\n}\n' 'sc2.example.dev' '$AUTH_IP' >> /etc/caddy/Caddyfile
  caddy validate --config /etc/caddy/Caddyfile && systemctl reload caddy"
```

Verify the public path (LE cert + login redirect):
```bash
curl -s https://sc2.example.dev/healthz -o /dev/null -w '%{http_code}\n'          # 200
curl -sD - https://sc2.example.dev/login/github -o /dev/null | grep -i location   # 302 → github.com …client_id=…
```

---

## 5. Deploy the broker appliance (admin provisioning plane)

The broker provisions tenants over the mounted host admin socket. It listens on host
port `:9443`.

```bash
sc-adm bootstrap \
  --hostname     sc2.example.dev \
  --base-image   images:debian/13 \
  --binary       bin/linux-amd64/sandcastle \
  --cidr-pool    10.249.0.0/16
```

Verify:
```bash
incus exec big:sc2-broker --project sc2-broker -- systemctl is-active sandcastle-broker  # active
```

---

## 6. Provision the first tenant

Two equivalent paths — both create `sc2-<tenant>` (infra + sidecar) and
`sc2-<tenant>-default` (the tenant's default project), on a per-tenant bridge:

**a) Admin, via the broker** (no browser):
```bash
SSHKEY=$(cat ~/.ssh/id_ed25519.pub)
incus exec big:sc2-broker --project sc2-broker -- \
  /usr/local/bin/sandcastle-admin tenant create-v2 alice \
  --sidecar-image images:debian/13 --ssh-key "$SSHKEY" --tailscale-authkey "$TAILSCALE_AUTH_KEY"
# prints an enrollment "token=…"
```

> **No Tailscale auth key?** Omit `--tailscale-authkey`. The sidecar starts
> `tailscale up` interactively and the command prints a login URL:
> ```
> Tailscale: no auth key was given, so the sidecar is not on a tailnet yet.
> Register it by opening this URL and approving the machine:
>   https://login.tailscale.com/a/…
> ```
> Open it, approve the machine (and approve its `--advertise-routes` subnet in the
> Tailscale admin console), and the sidecar joins the tailnet. Re-running create after
> it's registered is a no-op for Tailscale.

**b) Self-service, via login** (GitHub device flow against the auth-app):
```bash
sc login https://sc2.example.dev          # approve in the browser; provisions the caller's tenant
```

Verify the tenant came up (sidecar running, CoreDNS active, tailnet joined):
```bash
incus list big: --project sc2-alice -c ns4
incus exec big:sc2-alice --project sc2-alice -- systemctl is-active coredns   # active
incus exec big:sc2-alice --project sc2-alice -- tailscale status | head -1
```

### Enroll the client + launch machines
```bash
sc connect-v2 alice --token "<token-from-6a>"    # writes ~/.config/sandcastle/alice/incus
export INCUS_CONF=~/.config/sandcastle/alice/incus
incus remote switch alice-default

# tenant machines are plain Incus instances in the default project — use the /cloud
# image variant so cloud-init applies the profile (dev user + SSH key + sshd):
incus launch images:debian/13/cloud big:ct1 --project sc2-alice-default        # container
incus launch images:debian/13/cloud big:vm1 --project sc2-alice-default --vm   # VM
```

---

## 7. CIDR pools — the one config you must get right

Each tenant gets a `/24` cut from a `/16` pool, and its gateway becomes a real bridge
(`dnsmasq`) on the host. The allocator only skips CIDRs it can see as existing **v2
tenants** — it is blind to any other bridge on the host. So a pool that overlaps another
bridge fails provisioning with:

```
dnsmasq: failed to create listening socket for <gw>: Address already in use
```

Rules:
- Give the **broker** and the **auth-app** **distinct** `/16` pools (e.g. broker
  `10.249.0.0/16`, auth-app `10.250.0.0/16`).
- Neither may overlap `incusbr0`, any pre-existing tenant bridge, or (on a mixed host)
  legacy `sc-*` bridges.
- **Do not leave the auth-app `--cidr-pool` at its `10.248.0.0/16` default** on a host
  that already uses `10.248.x` — pick a clean, unused range.

Check what's already taken before choosing:
```bash
incus network list big: --project default -c n4 --format csv | grep bridge
```

---

## 8. Gotchas

- **Systemd image only.** Appliances must launch from a **container-type** systemd image
  (`images:debian/13`). An OCI/app image has no systemd PID 1 and `systemctl` fails.
- **`text file busy` pushing the binary.** You're overwriting the binary the appliance is
  running: `systemctl stop <svc>`, `incus file push …`, `systemctl start <svc>`.
- **`Image not provided for instance creation`.** The stock base image isn't in the
  `default` project's store — re-cache it: `incus image copy images:debian/13 big: --project default`.
- **Every tenant/app hostname must resolve to the host IP**, and `:80`/`:443` must be
  internet-reachable, or ACME can't issue.
- **Exposing a tenant app publicly:** add a vhost to the sc-edge Caddyfile pointing at the
  machine's bridge IP, then `systemctl reload caddy` (see `docs/e2e-sc2.md` Phase 7b).
- **SSH into a tenant machine** offering many agent keys? Use
  `ssh -o IdentitiesOnly=yes -i <key> dev@<ip>`.

---

## What you end up with

```
Incus host (big)
├── project infrastructure
│   ├── sc-edge        Caddy, owns public :80/:443 (TLS edge)
│   └── sc2-auth-app   auth-app :9444  (login, OIDC, provisioning)  ← fronted by sc-edge
├── project sc2-broker
│   └── sc2-broker     broker :9443  (admin provisioning plane)
└── project sc2-<tenant> / sc2-<tenant>-default
    ├── sc2-<tenant>   per-tenant sidecar (CoreDNS + Tailscale) on the tenant bridge
    └── <machines>     native Incus CTs/VMs the tenant launches
```
