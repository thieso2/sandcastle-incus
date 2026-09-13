# Cloudflare Tunnel per tenant: API, token scope, limits, edge certificates, cloudflared on the sidecar (2026-09-13)

Research for [#180](https://github.com/thieso2/sandcastle-incus/issues/180), part of the
Hostname Reach map [#179](https://github.com/thieso2/sandcastle-incus/issues/179).
Design under test: one **Tenant Tunnel** per tenant in the admin's Cloudflare
account, `cloudflared` in the tenant sidecar (`images:debian/13`, systemd), remotely
managed ingress, origin = the machine's Caddy over HTTPS :443 with its Let's
Encrypt Machine Certificate (ADR-0027/0028), public names of the shapes
`web12.tc42.uk`, `*.web12.tc42.uk`, `web12.e2e.sc.tc42.uk`.

Sources are Cloudflare's developer docs, the API reference, and the
`cloudflare/cloudflared` source, as read on 2026-09-13. Anything not confirmed
from those is listed under *Gaps / unverified*.

## TL;DR

1. **Edge certificates decide the name shapes.** Universal SSL covers only the
   zone apex and *first-level* names (`tc42.uk`, `web12.tc42.uk`). The wildcard
   below a hostname (`foo.web12.tc42.uk`) and any deeper name
   (`web12.e2e.sc.tc42.uk`) are **not covered and fail the TLS handshake**
   (`ERR_SSL_VERSION_OR_CIPHER_MISMATCH`), not "served with a mismatched cert".
   Covering them needs **Advanced Certificate Manager** (paid, per zone; ~$10/month
   at launch). **Total TLS is explicitly unavailable for Tunnel hostnames.**
2. The API does everything the design needs with **one** token: account-scoped
   `Cloudflare Tunnel: Edit` plus the zone-scoped `DNS: Edit` + `Zone: Read` that
   DNS-01 already holds.
3. Limits are comfortable: 1,000 tunnels/account, 25 replicas/tunnel, no
   documented ingress-rule cap; the tight one is **200 DNS records per Free zone**.
4. `cloudflared` runs remotely managed from a single connector token in a
   systemd unit (what `authapp_ingress.go` already does); config changes push over
   the tunnel with no restart.
5. Origin TLS is verified against the system trust store by default;
   `originServerName` fixes SNI + verification name; a Let's Encrypt **staging**
   origin fails verification (502) unless the staging root is supplied via
   `caPool` (a file path on the sidecar) or the rule sets `noTLSVerify`.

## What already exists in the repo

- `internal/cli/cloudflare_tunnel.go` — hand-rolled client: `findZone` (walks
  `/zones?name=` candidates, returns zone + account id), `ensureTunnel`
  (`GET /accounts/{a}/cfd_tunnel?name=&is_deleted=false`, else `POST` with
  `config_src: "cloudflare"`), `setTunnelIngress` (`PUT …/configurations` with one
  hostname rule → `http://localhost:8080` and the `http_status:404` catch-all),
  `ensureDNSRecord` (proxied CNAME to `<id>.cfargotunnel.com`, create or `PUT`),
  `tunnelToken` (`GET …/token`). No delete, no connections cleanup, no
  `originRequest`, one hostname per tunnel.
- `internal/incusx/authapp_ingress.go` + `authapp_bootstrap.go` — downloads the
  static `cloudflared-linux-<arch>` from GitHub releases onto the appliance,
  writes `/etc/default/cloudflared` (`TUNNEL_TOKEN=…`, mode 0600) and a unit
  running `/usr/bin/cloudflared tunnel --no-autoupdate run` as root with
  `Restart=on-failure`.
- Sidecar: `images:debian/13`, packages installed by `installV2SidecarPackages`
  (`internal/incusx/tenant_create_v2.go`) — Tailscale via its apt repo keyed to
  the container's codename. Either pattern (apt repo or pushed static binary)
  fits cloudflared.
- ADR-0027: the Auth App is the ACME client and the only holder of the DNS token
  (`Zone > DNS > Edit`); ADR-0028: a hostname reserves itself plus its wildcard
  subtree; one certificate per hostname carries `name` + `*.name`.

## 1. API and token scope

Base `https://api.cloudflare.com/client/v4`; index of tunnel endpoints:
<https://developers.cloudflare.com/api/resources/zero_trust/subresources/tunnels/subresources/cloudflared/>.

| Operation | Endpoint | Notes |
|---|---|---|
| Create tunnel | `POST /accounts/{account_id}/cfd_tunnel` `{"name", "config_src": "cloudflare", "tunnel_secret"?}` | OpenAPI default for `config_src` is `local` — send `"cloudflare"` explicitly for a remotely-managed tunnel. `tunnel_secret` (≥32 bytes, base64) optional; Cloudflare generates one if omitted. Response carries `id` and `token`. `PATCH` with a new `tunnel_secret` rotates it. Status: `inactive|degraded|healthy|down`. [create](https://developers.cloudflare.com/api/resources/zero_trust/subresources/tunnels/subresources/cloudflared/methods/create/), [create-remote-tunnel-api](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/get-started/create-remote-tunnel-api/) |
| List | `GET /accounts/{a}/cfd_tunnel?name=&is_deleted=&per_page=` | as used by `ensureTunnel` |
| Connector token | `GET /accounts/{a}/cfd_tunnel/{id}/token` | returns a string; "treat as a secret". It is `base64(JSON{a: accountTag, s: secret, t: tunnelID[, e]})` (cloudflared `connection/connection.go`). Same value on every replica. |
| Ingress config | `PUT /accounts/{a}/cfd_tunnel/{id}/configurations` `{"config": {"ingress": [...], "originRequest": {...}}}` | **Full replace**, not a merge — the client must own the whole rule list per tunnel. ≥1 rule; last rule must be a catch-all (`{"service": "http_status:404"}`). `warp-routing` is deprecated/ignored — do not send. [configurations](https://developers.cloudflare.com/api/resources/zero_trust/subresources/tunnels/subresources/cloudflared/subresources/configurations/) |
| Connections | `GET …/{id}/connections`; `DELETE …/{id}/connections[?client_id=]` | delete "recommended after rotating tokens"; also the way to clear stale connectors before deleting the tunnel |
| Delete | `DELETE /accounts/{a}/cfd_tunnel/{id}` | fails with error `1022` while connections are active; `?cascade=true` is what cloudflared's own `tunnel delete -f` sends but is undocumented. Irreversible. Safe order: stop connector → `DELETE …/connections` → `DELETE` tunnel → delete CNAMEs. |

Ingress rule fields (remote config uses the same schema as the local file,
[configuration-file](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/configuration-file/),
[origin-parameters](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/cloudflared-parameters/origin-parameters/)):

- `hostname` — exact or wildcard; wildcards are supported at the **leftmost label
  only** (`*.web12.tc42.uk`), per the local-config page; the remote-config page
  does not repeat this (see gaps). `path` optional regex.
- `service` — `http://`, `https://`, `tcp://`, `ssh://`, `unix://`, `http_status:NNN`.
- `originRequest` (per rule, or top-level default): `originServerName` (default
  `""` = hostname from the `service` URL), `caPool` (**a file path on the
  cloudflared host**, even when set via remote config), `noTLSVerify` (default
  false), `http2Origin` (default false), `connectTimeout` (30s), `tlsTimeout`
  (10s), `matchSNItoHost` (false), `httpHostHeader`, `keepAliveConnections`, …

DNS ([routing-to-tunnel/dns](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/routing-to-tunnel/dns/)):
a **proxied** CNAME to `<UUID>.cfargotunnel.com`. "The cfargotunnel.com subdomain
only proxies traffic for DNS records in the same Cloudflare account" — the
same-account constraint that put tunnel ownership with the admin in #179. A
stopped tunnel leaves the record in place; visitors get Cloudflare error 1016.
Proxied wildcard CNAMEs are allowed on all plans
([wildcard-dns-records](https://developers.cloudflare.com/dns/manage-dns-records/reference/wildcard-dns-records/)),
so `*.web12.tc42.uk → <tunnel>.cfargotunnel.com` is one record.

### Token permissions

[Permissions reference](https://developers.cloudflare.com/fundamentals/api/reference/permissions/);
each endpoint page lists its `x-api-token-group`:

- Tunnel endpoints: any of **Cloudflare Tunnel Write** (dashboard name
  *Account › Cloudflare Tunnel › Edit*), *Cloudflare One Connectors Write*,
  *Cloudflare One Connector: cloudflared Write*.
- DNS record writes: **Zone › DNS › Edit**. `GET /zones` (zone/account lookup):
  **Zone › Zone › Read**. `Account › Account Settings › Read` only if the account
  id is resolved via `/accounts` rather than from the zone object (the existing
  client reads `account.id` off the zone, so it is not needed).
- A token holds several policies at once (account-scoped + zone-scoped)
  ([create-via-api](https://developers.cloudflare.com/fundamentals/api/how-to/create-via-api/)).
  The zone policy is exactly what DNS-01 (ADR-0027) already needs, so **one
  token can serve DNS-01 and tunnels**. Tokens cannot be scoped below zone
  level (already noted in ADR-0027).

## 2. Limits

[Zero Trust account limits](https://developers.cloudflare.com/cloudflare-one/account-limits/),
[API limits](https://developers.cloudflare.com/fundamentals/api/reference/limits/):

| Limit | Value |
|---|---|
| cloudflared tunnels per account | 1,000 |
| Active replicas (connectors) per tunnel | 25 |
| Outbound connections per connector | 4 (2 data centres × 2), `--ha-connections` |
| Ingress rules per tunnel / config size | **not documented** |
| API requests | 1,200 per 5 minutes per token (429, then 5-minute block) |
| DNS records per zone | Free zones created after 2024-09-01: **200**; Pro/Business 3,500 |
| Pricing | Tunnel available on all plans; no tunnel-specific charge |

Consequence: 1,000 tenants per account is far away; the DNS-record budget on a
Free zone is the number to watch — one `name` + one `*.name` CNAME per
internet-reach name, so ~100 names per Free zone.

## 3. Edge certificates (decisive)

### Universal SSL coverage

"Universal SSL certificates … cover the zone apex and first-level subdomains"
— `tc42.uk`, `*.tc42.uk`; deeper names "will not serve a valid certificate"
([universal-ssl](https://developers.cloudflare.com/ssl/edge-certificates/universal-ssl/),
[limitations](https://developers.cloudflare.com/ssl/edge-certificates/universal-ssl/limitations/)).

| Name | Universal SSL |
|---|---|
| `web12.tc42.uk` | covered (first-level) |
| `foo.web12.tc42.uk` (the `*.web12.tc42.uk` wildcard) | **not covered** |
| `web12.e2e.sc.tc42.uk` | **not covered** |
| `<machine>.<Project Domain>` where the Project Domain is `baum.hase.de` | **not covered** (second level under `hase.de`) |

### What an uncovered proxied hostname does

It **fails the handshake** — no certificate is presented for that SNI; browsers
show `ERR_SSL_VERSION_OR_CIPHER_MISMATCH` / "uses an unsupported protocol"
([version-cipher-mismatch](https://developers.cloudflare.com/ssl/troubleshooting/version-cipher-mismatch/)).
It does not serve a mismatched certificate. The Tunnel docs say it directly:
"If you add a multi-level subdomain, you must order an Advanced Certificate for
the hostname"
([create-remote-tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/get-started/create-remote-tunnel/),
[common-errors](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/troubleshoot-tunnels/common-errors/#i-see-this-site-cant-provide-a-secure-connection)).

### Total TLS

Would issue a certificate per proxied hostname automatically (requires ACM),
but is **explicitly not available for Cloudflare Tunnel hostnames**
([total-tls](https://developers.cloudflare.com/ssl/edge-certificates/additional-options/total-tls/)).
Not an option here.

### Advanced Certificate Manager (ACM)

[advanced-certificate-manager](https://developers.cloudflare.com/ssl/edge-certificates/advanced-certificate-manager/),
[api-commands](https://developers.cloudflare.com/ssl/edge-certificates/advanced-certificate-manager/api-commands/),
[certificate_packs create](https://developers.cloudflare.com/api/resources/ssl/subresources/certificate_packs/methods/create/):

- Paid per-zone add-on. Price **$10/month per zone** at launch
  ([blog, 2021](https://blog.cloudflare.com/advanced-certificate-manager/)); the
  current docs print no price (see gaps).
- One certificate pack = the apex plus up to **49 more hosts/wildcards** (50
  total). **Multi-level names are supported; one wildcard per level** —
  `*.web12.tc42.uk` covers `foo.web12.tc42.uk` only, not `a.b.web12.tc42.uk`.
- `POST /zones/{zone_id}/ssl/certificate_packs/order` with `type: "advanced"`,
  `hosts: [...]`, `certificate_authority: google|lets_encrypt|ssl_com`,
  `validation_method: txt|http|email`, `validity_days: 14|30|90|365`.
  Status flow `initializing → pending_validation → pending_issuance →
  pending_deployment → active`.
- DCV is automatic on a full-setup zone (Cloudflare writes the TXT records
  itself), including wildcards
  ([changing-dcv-method](https://developers.cloudflare.com/ssl/edge-certificates/changing-dcv-method/)).
- Token permission for ordering packs: *Zone › SSL and Certificates › Edit*
  (adds a third policy to the token if used).

### Cloudflare for SaaS

Custom hostnames get per-hostname certificates at any depth, but they front a
fallback origin in the SaaS zone, not a Tunnel public hostname — not a drop-in
substitute.

## 4. cloudflared on the sidecar

### Packaging on Debian 13 (trixie)

- Official apt repo: <https://pkg.cloudflare.com/> — key
  `https://pkg.cloudflare.com/cloudflare-main.gpg` → `/usr/share/keyrings/cloudflare-main.gpg`,
  line `deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main`.
  There is **no `trixie` suite for cloudflared** (`dists/trixie/Release` is a
  404; the "Debian 13" entry on that page is for gokeyless). The `any` suite
  serves cloudflared **2026.9.1** (2026-09-11) for amd64/arm64/…; the `.deb`
  (~19 MB, amd64) declares no `Depends` and installs a **static**
  `/usr/bin/cloudflared` (~40 MB, no dynamic section). Unlike the Tailscale
  install in `installV2SidecarPackages`, the repo line must *not* be keyed to
  the container's codename.
- Alternative: GitHub release assets `cloudflared-linux-<arch>` /
  `cloudflared-linux-<arch>.deb`
  ([downloads](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/downloads/)),
  the pattern `fetchIngressBinaries` already uses for the auth-app appliance.

### systemd unit

`cloudflared service install <TOKEN>`
(source `cmd/cloudflared/linux_service.go`) writes the token to
`/etc/cloudflared/<tokenfile>` (0600) and
`/etc/systemd/system/cloudflared.service` with `Type=notify`,
`ExecStart=/usr/bin/cloudflared --no-autoupdate tunnel run --token-file /etc/cloudflared/…`,
`Restart=on-failure`, `RestartSec=5s`, `After=/Wants=network-online.target`;
runs as **root**, no hardening; also installs `cloudflared-update.service`/`.timer`
unless `--no-update-service`. Run parameters
([run-parameters](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/cloudflared-parameters/run-parameters/)):
`--token`/`TUNNEL_TOKEN`, `--token-file`/`TUNNEL_TOKEN_FILE` (2025.4.0+),
`--no-autoupdate`, `--protocol` (auto = QUIC, falls back to http2). The
`authapp_ingress.go` unit (`EnvironmentFile` + `TUNNEL_TOKEN`) is equivalent;
`Type=notify` is the one improvement worth copying.

### Remote-managed config propagation

"A remotely-managed tunnel only requires the tunnel token to run. Anyone with
access to the token will be able to run the tunnel."
([remote-tunnel-permissions](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/remote-tunnel-permissions/)).
The docs do not describe the propagation mechanism or its delay. In the source
the edge **pushes** new configuration over the existing tunnel connection
(QUIC RPC `UpdateConfiguration` in `connection/quic_connection.go`; HTTP/2
`ConfigurationUpdateBody` in `connection/http2.go`) into
`orchestration/orchestrator.go` `UpdateConfig`, which ignores versions ≤ the
current one, hot-swaps the ingress rules and logs "Updated to new
configuration". **No restart**, no file on the sidecar changes.

### Replicas / HA

[tunnel-availability](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-availability/),
[deploy-replicas](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/deploy-tunnels/deploy-cloudflared-replicas/),
[tunnel-tokens](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-tokens/):
each connector opens 4 outbound connections to ≥2 data centres; up to 100
connections = **25 replicas** per tunnel, all with the same token and the same
config; the nearest replica wins, no traffic steering. After token rotation the
old token cannot open new connections but running connectors stay up until
restarted. For a one-sidecar tenant, one replica is the design; a second
sidecar would just be a second `cloudflared` with the same token.

### Footprint and egress

[tunnel-with-firewall](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-with-firewall/):
egress **7844 TCP+UDP** to `region1.v2.argotunnel.com` / `region2.v2.argotunnel.com`
(SNI `cftunnel.com`, `h2.cftunnel.com`, `quic.cftunnel.com`); optionally 443 to
`api.cloudflare.com` and `update.argotunnel.com`. The sidecar already has
outbound Internet (Tailscale). The "4 GB RAM / 4 cores" system-requirements
page is sizing for WARP-client scale (8,000 users), not a minimum; Cloudflare's
product page says it runs on a Raspberry Pi. No official per-tunnel CPU/RAM
number exists; expect tens of MB RSS idle (unverified).


## 5. Origin TLS

Docs: [origin-parameters](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/cloudflared-parameters/origin-parameters/);
source: `ingress/origin_service.go` (`newHTTPTransport`), `tlsconfig/origin_ca.go`,
`ingress/ingress.go` (`matchHost`).

- **Verification is on by default**: `noTLSVerify` defaults to false and the
  origin certificate is verified against the system trust store plus
  Cloudflare's roots (`x509.SystemCertPool`).
- `caPool` is a **local file path** on the sidecar and **appends** to that
  pool; it never replaces it.
- `originServerName` is Go's `tls.Config.ServerName`: it is **both the SNI sent
  and the name verified against the certificate's SANs**. When empty, Go uses
  the dial host from `service:`. So either
  `service: https://10.250.0.14` + `originServerName: web12.tc42.uk`, or
  `service: https://web12.tc42.uk` — cloudflared is a static Go binary, so it
  resolves through the pure-Go resolver using the container's
  `/etc/resolv.conf`, i.e. the tenant CoreDNS, which already answers the public
  name with the bridge address (ADR-0027 A records / tenant DNS). The latter
  gives SNI + verification name for free and needs no `originServerName`.
- Go ignores the CN; the certificate must carry a **matching SAN** (Let's
  Encrypt certificates do; the Machine Certificate carries `name` + `*.name`).
  A `*.web12.tc42.uk` ingress rule with the request Host `foo.web12.tc42.uk`
  forwarded to `service: https://web12.tc42.uk` verifies against the
  `web12.tc42.uk` SAN — fine; with `matchSNItoHost: true` the SNI would be the
  request Host and verify against the wildcard SAN instead — also fine.
- **Untrusted root (Let's Encrypt STAGING origin cert, e2e)**: verification
  fails, cloudflared logs "Unable to reach the origin service … x509:
  certificate signed by unknown authority" and the visitor gets **502 Bad
  Gateway** ([common-errors](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/troubleshoot-tunnels/common-errors/)).
  Fixes: install the staging root ("(STAGING) Pretend Pear X1") into the
  sidecar's system trust store (`/usr/local/share/ca-certificates/` +
  `update-ca-certificates`) or point `caPool` at it, or set `noTLSVerify: true`
  on the rules for e2e only.
- `--origin-ca-pool` / `--no-tls-verify` CLI flags apply only to `--url`
  single-origin mode; with ingress rules use `originRequest`.
- `http2Origin: true` speaks HTTP/2 to Caddy (optional). The Host header is
  passed unchanged unless `httpHostHeader` is set.
- Wildcard matching (`matchHost`): exact match, or a `*.` rule matches by
  suffix (`HasSuffix(".web12.tc42.uk")`); at most one wildcard, leftmost only;
  first matching rule wins; the last rule must be the catch-all.


## Gaps / unverified

- ACM price is only from the 2021 launch blog; verify in the dashboard before
  writing it into a spec. Certificate-pack count per zone for non-Enterprise
  plans and ACM issuance latency are undocumented.
- `DELETE …/cfd_tunnel/{id}?cascade=true` is used by cloudflared but not in the
  API docs; the error-1022 wording is from a Terraform provider issue, not docs.
- Wildcard `hostname` in **remote** config is inferred from the local-config
  page; not explicitly documented for `PUT …/configurations`.
- No documented per-tunnel ingress-rule limit or config-size limit.
- Zero Trust seat limits are unrelated to tunnels as far as the docs say, but
  no page states that explicitly.
- Config-propagation delay is undocumented (source-only reading); local→remote
  tunnel conversion undocumented; whether the `any` apt suite keeps serving
  trixie long-term is unstated; the 25-replica limit enforcement is unverified;
  no official cloudflared CPU/RAM footprint.
- Nothing here was exercised against a live account; all of it is doc/source
  reading.

## Recommendation

**Token scope — one token, three policies.** Extend the Public DNS Zone token of
ADR-0027 rather than adding a second secret: *Account › Cloudflare Tunnel › Edit*
(account-scoped) + *Zone › DNS › Edit* + *Zone › Zone › Read* (the DNS-01 pair,
scoped to the Sandcastle zones), plus *Zone › SSL and Certificates › Edit* if
ACM packs are managed by the reconciler. The Auth App already holds this token
and already runs the 30s reconciler that watches machines and names; it is the
natural owner of tunnels and rules. The account id comes from the zone object,
so no account-read permission is needed. `sc-adm public-dns-zone add` should
verify the account policy up front (`GET /accounts/{a}/cfd_tunnel?per_page=1`),
the way it presumably verifies the DNS policy today.

**Config management — remote-managed, reconciler-owned, full replace.** Create
each Tenant Tunnel with `config_src: "cloudflare"` at tenant creation (or
lazily on the first `internet` name), fetch `…/token` once and push it to the
sidecar as `TUNNEL_TOKEN` in `/etc/default/cloudflared` — the exact pattern of
`authapp_ingress.go`; the sidecar never sees the API token. Because
`PUT …/configurations` replaces the whole list, the reconciler derives the full
rule set from the Auth Database on every change (one rule per internet-reach
name: `hostname: name` and `hostname: *.name`, `service: https://<bridge-addr>:443`,
`originServerName: name`, `http2Origin: true`, catch-all `http_status:404`),
compares with `GET …/configurations`, and writes only on drift. Delete order on
tenant delete: stop the sidecar unit → `DELETE …/connections` → `DELETE` tunnel
→ delete CNAMEs. Keep the existing hand-rolled client; add delete, connections,
`originRequest` and multi-rule support to it rather than pulling in the SDK.

**Edge certificates — the design must pick one of two.**

- *A. First-level names only on Universal SSL (no cost).* `internet` reach is
  accepted only for names directly under a zone (`web12.tc42.uk`) and exposes
  **only that name, not its wildcard**, unless the zone has ACM. `*.web12.tc42.uk`
  and deep names (`web12.e2e.sc.tc42.uk`, `<machine>.<Project Domain>`) are
  refused at `set-reach`/`--reach internet` with a message pointing at ACM.
  This contradicts the #179 settlement that internet reach "exposes the name and
  its one-level wildcard"; that line needs amending or option B.
- *B. ACM per zone, managed by the reconciler (~$10/month/zone).* The reconciler
  keeps one advanced certificate pack per Public DNS Zone containing the apex,
  `*.<zone>`, and for every internet-reach name `name` + `*.name`; ≤50 hosts per
  pack, so 24 internet-reach names per pack, more packs as needed (pack count per
  zone unverified). Ordering is asynchronous (`pending_*` states) — the name is
  not serviceable until `active`, so `set-reach internet` must be a two-phase
  operation with a `PENDING` reach shown in the `REACH` column. This is the only
  route that honours the settled "name + one-level wildcard" contract and the
  Project Domain shape; Total TLS cannot substitute (Tunnel hostnames excluded).

Recommendation: implement **A as the base behaviour and B as an opt-in per
zone** (`sc-adm public-dns-zone add --acm`), and amend #179 so the wildcard
exposure under `internet` reach is conditional on the zone's ACM flag. The
e2e stage on a Free zone then exercises A; a Pro/ACM zone exercises B.

**DNS-record budget.** On a Free zone (200 records) internet reach costs two
CNAMEs per name (`name`, `*.name`); today's tailnet reach costs one A record.
The reconciler should count records per zone and refuse with a clear message
before Cloudflare does.
