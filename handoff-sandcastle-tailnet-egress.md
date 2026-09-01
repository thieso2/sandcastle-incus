# Handoff: Tailnet egress for sandcastle machines

**Target repo:** `sandcastle-incus`

**Purpose of the next session:** Design and implement *tailnet egress* — let
machines (CTs/VMs) behind a tenant sidecar open connections **to** tailnet
peers (CGNAT range `100.64.0.0/10`), the mirror image of the inbound subnet
route that already works. Nothing is built; the concept below was validated by
a live diagnosis but hand-implemented nowhere.

**Motivating case:** a Reachy Mini robot (`reachy-mini`, `100.87.148.82`,
HTTP `:8000`, SSH `:22`) on the personal tailnet must be reachable from CTs in
tenant `thieso2` on install `obelix`. Generalizes to any tailnet device a
machine wants to call (robots, printers, laptops, other tailnets' shared
nodes).

---

## 1. Problem

Machines are deliberately **not** tailnet nodes (see the rejected alternative
in §6). They get *inbound* reachability via the sidecar's advertised `/24`
subnet route. *Outbound* to a tailnet IP was never wired up:

- A machine's only route is `default via <cidr>.1` — the **host** bridge
  gateway, not the sidecar.
- The host forwards CGNAT-destined packets to its public uplink, where
  `100.64.0.0/10` is unroutable and silently dropped.

Diagnosed live on `dev.devbox.obelix` (10.123.0.58, behind sidecar
"sc-obelix"): `ping`/`curl`/`ssh` to `100.87.148.82` all time out;
TTL-stepped pings show hop 1 = `10.123.0.1` (host), hop 2+ = Hetzner public
IPs. The sidecar sits on the same bridge with a perfectly good
`tailscale0` path and never sees the traffic.

Observed detail worth fixing or documenting: the internals doc assigns role
addresses gateway `.1` / Tailscale `.2` / DNS `.3`, but on obelix the sidecar
answers ARP only at **`.3`**; `.2` is silent. The egress next-hop must be the
sidecar's *actual* bridge address — resolve from provisioning code, don't
assume `.2`.

## 2. Design (three pieces, all router-side)

Machines stay clean: no Tailscale in CTs, no per-machine config. Everything
lands on the tenant bridge and the sidecar.

### 2.1 Steer: DHCP classless static route (option 121) on the tenant bridge

```
incus network set <tenant-bridge> raw.dnsmasq='dhcp-option=121,100.64.0.0/10,<sidecar-bridge-ip>'
```

Every machine learns `100.64.0.0/10 via <sidecar>` with its lease — current
and future machines, zero per-machine setup. Caveats:

- Takes effect on lease renewal (≤ lease/2, ~20–40 min observed) or NIC
  bounce/restart; `sc fix` could force a renew.
- If the network already carries `raw.dnsmasq` content, append, don't clobber.
- Static-IP machines (if any exist) won't pick this up — acceptable, document.

### 2.2 NAT: masquerade LAN→tailnet on the sidecar

Tailscale's `--snat-subnet-routes` only covers the tailnet→subnet direction.
For subnet→tailnet the sidecar must rewrite the source to its own tailnet IP,
so the far peer replies to a normal tailnet node and needs neither
`--accept-routes` nor knowledge of the tenant CIDR:

```
nft: oifname "tailscale0" ip saddr <tenant-cidr> masquerade
```

Forwarding is already enabled (subnet router). Rule must be persistent and
must compose with whatever nftables/iptables state tailscaled manages —
prefer a dedicated sandcastle table/chain over editing tailscale's.

### 2.3 Permit: tailnet ACL

The sidecar is a tagged node; tagged nodes are strictly ACL-governed. Packets
delivered by 2.1+2.2 are silently dropped until the policy grants, e.g.:

```jsonc
{ "action": "accept", "src": ["tag:sandcastle"], "dst": ["100.87.148.82:8000,22"] }
```

This is tenant/operator policy, **out of platform scope** — but the platform
must document it, and `sc tailscale status` should surface enough identity
(sidecar tag + tailnet IP) that the operator can write the stanza without
shelling into the sidecar.

**Deliberate decision point:** on a default-open ACL, egress makes *every*
machine of the tenant able to reach *every* tailnet node, with the sidecar's
tag as the shared identity. That is the trade-off accepted in §6; the docs
must say it out loud.

## 3. Implementation notes for the repo

- **Where:** sidecar provisioning (NAT rule), tenant bridge creation
  (dnsmasq option), and a backfill path for existing tenants — `sc fix`-style
  idempotent converge, or the `sc-adm` fleet-update mechanism.
- **Surface:** decide opt-in vs default-on. Suggestion: per-tenant setting
  (`sc project`-level is too narrow — the bridge is per-tenant), default off,
  enabled via something like `sc tailscale egress on|off|status`. `sc
  tailscale status` reports egress state either way.
- **Next-hop address:** normalize the sidecar's bridge address against the
  role-address convention (`.2`) or update `docs/topology.md`/internals to
  match reality (`.3` on obelix today). Either way the dnsmasq option must be
  rendered from the real address.
- **e2e:** `docs/e2e-sc2.md` is the behavioural source of truth — a change not
  reflected there is not done. Acceptance shape: from a fresh machine,
  `curl http://<tailnet-peer>:<port>/` succeeds once egress is on and an ACL
  grant exists; fails closed when egress is off.
- **ADR:** record the decision and the rejected alternative (§6) under
  `docs/adr/`; add "tailnet egress" to `docs/glossary.md`.

## 4. Follow-up (separate, don't block on it)

**MagicDNS for machines.** With egress in place, machines can dial tailnet
IPs but not names. The sidecar's CoreDNS could forward the tailnet MagicDNS
zone (`*.ts.net` / `100.100.100.100`) so `reachy-mini` resolves inside CTs.
Small, high-leverage, but its own change.

## 5. Rollout / verification for the motivating case

1. Implement + enable for tenant `thieso2` on `obelix` (sidecar sc-obelix,
   CIDR `10.123.0.0/24`).
2. ACL grant `tag:<sidecar-tag>` → `100.87.148.82:8000,22` (+ ICMP for
   debugging).
3. From `dev.devbox.obelix`: `ip route get 100.87.148.82` shows the sidecar
   as next-hop; `curl -m8 http://100.87.148.82:8000/` returns HTTP 200;
   `ssh pollen@100.87.148.82 hostname` works.
4. Repeat for `idefix` (subnets `10.123.1.0/24`, `10.124.0.0/24`) when wanted.

## 6. Rejected alternative: machines as tailnet nodes

Considered and declined as the default: per-machine tailscaled would give
direct WireGuard and per-machine ACL identity, but costs device sprawl
(tagged ephemeral auth keys, cleanup of deleted machines), `/dev/net/tun`
in every CT, dual reachability (own node IP *and* subnet route) muddying DNS
and the Caddy story, and it dilutes the model where the sidecar is the single
tailnet touchpoint per tenant. May return later as an explicit opt-in flag on
`sc create` for machines that need node features (Tailscale SSH,
serve/funnel, per-machine ACL identity). The shared-identity limitation of
sidecar egress is the accepted trade-off.

## 7. Access note for the implementing session

None of this is doable with a tenant certificate: the sidecar lives in infra
project `<prefix>-<tenant>`, invisible to `sc incus-infra` under a restricted
cert, and the bridge lives in Incus project `default`. Work from the operator
plane (`sc-adm` / admin Incus remote on the host) — on the laptop, not inside
a devbox CT.
