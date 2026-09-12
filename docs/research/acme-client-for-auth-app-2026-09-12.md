# ACME client for the Auth App — primary-source research

**Date:** 2026-09-12
**Ticket:** [#156](https://github.com/thieso2/sandcastle-incus/issues/156) (map: [#155](https://github.com/thieso2/sandcastle-incus/issues/155))
**Question:** The Auth App will run ACME centrally — one Let's Encrypt certificate per machine covering `<m>.<project domain>` and `*.<m>.<project domain>`, validated by Cloudflare DNS-01. Which Go ACME client should it embed, and what are the hard operational facts (Let's Encrypt limits, Cloudflare token scope and propagation, wildcard + base in one order)?

All claims cite a URL or a file path. Library facts were read from pkg.go.dev, the upstream repos' `go.mod`/source at the tips noted below, and the GitHub API on 2026-09-12. Let's Encrypt and Cloudflare facts come from `letsencrypt.org/docs` (rate-limits page dated 2026-08-05), RFC 8555, and `developers.cloudflare.com`.

---

## TL;DR

- **Embed `github.com/caddyserver/certmagic`** (which brings `github.com/mholt/acmez/v3` as the ACME core) with **`github.com/libdns/cloudflare`** as the DNS-01 provider — but use it as an *issuer library*, not through `ManageSync`/`ManageAsync`: build one CSR with both SANs, call `ACMEIssuer.Issue`, keep the certificate in the Auth Database, and drive renewal from the existing 30 s reconciler using `ACMEIssuer.GetRenewalInfo` (ARI).
- Reason: `ManageSync`/`ManageAsync` issue **one certificate per name** (`generateCSR(privKey, []string{name}, false)`), which would spend two of the 50-per-week budget per machine. `Issue(csr)` accepts an arbitrary SAN set, so one machine = one order = one certificate, while still reusing certmagic's account handling, DNS-01 solver with propagation check, retries, staging fallback, and ARI.
- `golang.org/x/crypto/acme` is already in `go.mod` and dependency-free, but it has no solver, storage, renewal, or ARI support (and `autocert` cannot do DNS-01 at all). lego v5 does the SAN order natively but carries 109 direct dependencies and had a v4→v5 API break in May 2026.
- Hard limits: 50 new certificates per registered domain per 7 days (override obtainable); 5 per exact identifier set per 7 days (no override); 300 new orders per account per 3 h; 5 failed validations per identifier per hour. ARI-timed renewals are exempt from all limits. Staging: `https://acme-staging-v02.api.letsencrypt.org/directory`.
- Cloudflare: the token needs `Zone > DNS > Edit` on the zone, plus `Zone > Zone > Read` only if the Auth App resolves zone name → id itself. Tokens **cannot** be scoped below zone level. Cloudflare propagates "within 5 minutes, usually much less"; Let's Encrypt validates from multiple vantage points, so the client must wait for global visibility (certmagic's solver does this by querying authoritative nameservers).
- Wildcard + base in one order is allowed and requires DNS-01; both authorizations read `_acme-challenge.<m>.<project domain>`, so **two TXT values coexist at the same name** during validation.

---

## 1. Candidate libraries

### 1.1 Summary table

| | certmagic (+ acmez/v3, libdns/cloudflare) | lego v5 | `golang.org/x/crypto/acme` (+ autocert) |
|---|---|---|---|
| Latest version | v0.25.4, tag 2026-06-09 ([pkg.go.dev](https://pkg.go.dev/github.com/caddyserver/certmagic), [tags](https://api.github.com/repos/caddyserver/certmagic/tags?per_page=5)) | v5.4.1, 2026-08-31 ([pkg.go.dev](https://pkg.go.dev/github.com/go-acme/lego/v5), [release](https://api.github.com/repos/go-acme/lego/releases/latest)); last v4 = v4.35.2, 2026-04-24 | x/crypto v0.57.0, 2026-09-08 ([pkg.go.dev](https://pkg.go.dev/golang.org/x/crypto/acme)); repo pins v0.49.0 |
| Last commit | 2026-09-09 (`722bda8`, checked via shallow clone) | 2026-09-10 ([commits](https://api.github.com/repos/go-acme/lego/commits?per_page=3)) | `acme/` dir 2026-08-14 ([commits](https://api.github.com/repos/golang/crypto/commits?path=acme&per_page=3)) |
| License | Apache-2.0 (acmez Apache-2.0; libdns/cloudflare MIT) | MIT | BSD-3-Clause |
| `go` directive | `go 1.25.0` ([go.mod](https://raw.githubusercontent.com/caddyserver/certmagic/master/go.mod)) | `go 1.26.0` ([go.mod v5.4.1](https://raw.githubusercontent.com/go-acme/lego/v5.4.1/go.mod)) | `go 1.26.0` ([go.mod](https://raw.githubusercontent.com/golang/crypto/master/go.mod)) |
| Direct deps | 10 (zerossl, cpuid, libdns v1.1.1, acmez/v3 v3.1.6, miekg/dns, blake3, zap, zap/exp, x/crypto, x/net) | 109 incl. AWS/Azure/Google SDKs (Go module pruning keeps the *build* small; `go.sum` and vuln-scanner noise are real) | 3 (x/net, x/sys, x/term) — already present in this repo |
| DNS-01 | yes; `DNS01Solver` with propagation check against authoritative NS | yes; propagation pre-check + CNAME following | primitives only (`DNS01ChallengeRecord`, `Accept`, `WaitAuthorization`); **autocert: no** |
| Cloudflare solver | `libdns/cloudflare` v0.2.2 — stdlib `net/http`, sole dep `libdns/libdns` ([go.mod](https://raw.githubusercontent.com/libdns/cloudflare/master/go.mod)) | `providers/dns/cloudflare` — own `net/http` client ("The official client is huge and still growing", [client.go](https://raw.githubusercontent.com/go-acme/lego/master/providers/dns/cloudflare/internal/client.go)) | none |
| Storage abstraction | `certmagic.Storage` (Locker + Store/Load/Delete/Exists/List/Stat) ([storage.go](https://raw.githubusercontent.com/caddyserver/certmagic/master/storage.go)) | none — returns PEM in `certificate.Resource`; docs say "SAVE THESE TO DISK" ([library docs](https://go-acme.github.io/lego/library/)) | `autocert.Cache` (Get/Put/Delete); none for `acme` |
| Renewal management | background loop, `DefaultRenewCheckInterval` 10 min, `DefaultRenewalWindowRatio` 1/3, ARI-driven ([maintain.go](https://raw.githubusercontent.com/caddyserver/certmagic/master/maintain.go)) | none; `GetRenewalInfo` + `ShouldRenewAt` helpers ([certificate](https://pkg.go.dev/github.com/go-acme/lego/v5/certificate)) | autocert only (no DNS-01); `acme` none |
| ARI (RFC 9773) | full: fetch, renewal timing, `Replaces` on orders ([acmeissuer.go](https://raw.githubusercontent.com/caddyserver/certmagic/master/acmeissuer.go) `ctxKeyARIReplaces`) | `GetRenewalInfo`, `ShouldRenewAt`, `ObtainRequest.ReplacesCertID` | **none** — no `RenewalInfo`/`Replaces` symbols in [`acme.go`](https://raw.githubusercontent.com/golang/crypto/master/acme/acme.go), [`types.go`](https://raw.githubusercontent.com/golang/crypto/master/acme/types.go), [`rfc8555.go`](https://raw.githubusercontent.com/golang/crypto/master/acme/rfc8555.go) |
| Wildcard + base in **one** order | `ManageSync`/`ManageAsync`: **no** (one cert per name); `ACMEIssuer.Issue(csr)` / `acmez.Client.ObtainCertificateForSANs`: **yes** | yes: `ObtainRequest.Domains = []string{"m.x", "*.m.x"}` ("first domain becomes the CommonName; others are added as SANs") | yes: `AuthorizeOrder(ctx, DomainIDs("m.x", "*.m.x"))`, then solve two authorizations by hand |
| OCSP | stapling + refresh in the maintenance loop (moot: Let's Encrypt ended OCSP, §2.4) | `GetOCSP` helper | none |

### 1.2 certmagic — details that matter for the Auth App

- **Solver.** `DNS01Solver{DNSManager}`; `DNSManager{DNSProvider (required), TTL, PropagationDelay (default 0), PropagationTimeout (default 2 min; -1 disables), Resolvers, OverrideDomain, Logger}`. `DNSProvider` is just `libdns.RecordAppender + libdns.RecordDeleter` ([solvers.go](https://raw.githubusercontent.com/caddyserver/certmagic/master/solvers.go)). `Wait()` polls the authoritative nameservers found by SOA walk (or the configured resolvers) until the TXT is visible ([dnsutil.go](https://raw.githubusercontent.com/caddyserver/certmagic/master/dnsutil.go)). Two TXT values at the same `_acme-challenge` name (base + wildcard) are explicitly handled — the solver distinguishes records "by the value of their TXT records" (caddy#3474, see solvers.go).
- **Issuer API.** `Issuer` interface = `Issue(ctx, *x509.CertificateRequest) (*IssuedCertificate, error)` + `IssuerKey()` (`certmagic.go:316`, tip `722bda8`). `ACMEIssuer.Issue` (`acmeissuer.go:380`) takes any CSR — this is the entry point that gives a single SAN order. `ACMEIssuer.GetRenewalInfo(ctx, Certificate) (acme.RenewalInfo, error)` (`acmeclient.go:285`) exposes ARI. `ACMEIssuer{CA, TestCA, Email, Agreed, DNS01Solver, DisableHTTPChallenge, DisableTLSALPNChallenge, CertObtainTimeout, PreferredChains, ...}`; construct with `NewACMEIssuer(cfg *Config, template)` (panics without a config). `DefaultACME.TestCA` is Let's Encrypt staging, used automatically on retry after a failure against production ([acmeissuer.go](https://raw.githubusercontent.com/caddyserver/certmagic/master/acmeissuer.go)).
- **Why not `ManageSync`.** `manageAll` → `manageOne` per name → `ObtainCertSync(ctx, name string)` → `generateCSR(privKey, []string{name}, false)` (`config.go:416/456/585/688`). Passing `[]string{"m.x", "*.m.x"}` yields two orders, two certificates, two DNS-01 rounds. `SubjectTransformer` (`config.go:189`, "EXPERIMENTAL") rewrites a single subject; it does not add SANs.
- **Storage.** `Storage` is 8 methods; built-in `FileStorage{Path}` (default `$HOME/.local/share/certmagic`). It must be "safe for concurrent use and honor context cancellations". Keys: `certificates/<issuerKey>/<domain>/<domain>.{crt,key,json}` via `StorageKeys` ([storage.go](https://raw.githubusercontent.com/caddyserver/certmagic/master/storage.go)). With the `Issue` path the Auth App only needs `Storage` for the ACME **account** key and locks; `FileStorage` under the appliance's data dir is sufficient, and a SQLite implementation is a small follow-up if desired.
- **Without a TLS listener.** Yes — README documents `ManageSync`/`ManageAsync` as the no-listener path and `HTTPS()` as the listener helper; the `Issue` path never touches a listener. Set `DisableHTTPChallenge` and `DisableTLSALPNChallenge` so only DNS-01 is offered ([README](https://raw.githubusercontent.com/caddyserver/certmagic/master/README.md)).
- **Caveats.** Hard dependency on `go.uber.org/zap` for logging; `Config.DisableARI` is marked "TEMPORARY: Will likely be removed" ([config.go](https://raw.githubusercontent.com/caddyserver/certmagic/master/config.go)); `OverrideDomain` writes the record elsewhere but does not itself follow CNAME/NS for placement (only the propagation check follows CNAME). README still says "Go 1.21 or newer" while `go.mod` says 1.25.0 — the module is what counts, and this repo is on `go 1.25.6`.
- **libdns/cloudflare.** `Provider{APIToken, ZoneToken, HTTPClient}`; README: single token "Zone:Read, Zone.DNS:Write" or dual tokens (`ZoneToken` = Zone:Read, `APIToken` = Zone.DNS:Write scoped to the zone); "Do NOT use API keys, which are globally-scoped" ([README](https://raw.githubusercontent.com/libdns/cloudflare/master/README.md)). Implements `RecordAppender/Deleter/Getter/Setter/ZoneLister`; last commit 2026-06-27 ([commits](https://api.github.com/repos/libdns/cloudflare/commits?per_page=3)).

### 1.3 lego v5

- v5.0.0 (2026-05-11) changed every API to take `context.Context`, changed `GetRenewalInfo`/`CertificateService` signatures and registration returns ([CHANGELOG](https://raw.githubusercontent.com/go-acme/lego/main/CHANGELOG.md)). The default branch is `main`; `master` still serves a v4 `go.mod` — do not read it ([repo](https://api.github.com/repos/go-acme/lego)).
- Custom solver: `challenge.Provider{Present(ctx, domain, token, keyAuth); CleanUp(...)}`, optional `ProviderTimeout` ([challenge](https://pkg.go.dev/github.com/go-acme/lego/v5/challenge)). DNS-01 follows CNAMEs by default (`LEGO_DISABLE_CNAME_SUPPORT`), pre-checks authoritative NS, `PropagationWait`, `DisableAuthoritativeNssPropagationRequirement` ([dns01](https://pkg.go.dev/github.com/go-acme/lego/v5/challenge/dns01)).
- Cloudflare provider env: `CLOUDFLARE_DNS_API_TOKEN`, `CLOUDFLARE_ZONE_API_TOKEN`, `CLOUDFLARE_TTL` 120, `CLOUDFLARE_PROPAGATION_TIMEOUT` 120 s, `CLOUDFLARE_POLLING_INTERVAL` 2 s; docs ask for "Zone / Zone / Read" + "Zone / DNS / Edit", or a split zone-read token + DNS-edit token ([docs](https://go-acme.github.io/lego/dns/cloudflare/)).
- Library mode gives no storage or renewal loop: the Auth App would persist `acme.ExtendedAccount` + key + `Resource` PEMs and own the timer, wiring `GetRenewalInfo`/`ShouldRenewAt` into it ([library docs](https://go-acme.github.io/lego/library/)).

### 1.4 `golang.org/x/crypto/acme`

- Manual DNS-01 only: `AuthorizeOrder`, `GetAuthorization`, `DNS01ChallengeRecord(token)`, `Accept`, `WaitAuthorization`, `WaitOrder`, `CreateOrderCert`; `Authorization.Wildcard` flag ([pkg.go.dev](https://pkg.go.dev/golang.org/x/crypto/acme)). No provider, storage, renewal, propagation check or ARI. Everything the Auth App needs beyond the wire protocol would be written from scratch.
- `autocert` supports only `tls-alpn-01` (+ `http-01` when `tryHTTP01`): `supportedChallengeTypes()` returns `[]string{"tls-alpn-01"}` and appends `"http-01"` conditionally; otherwise `verifyRFC` errors "no viable challenge type found" ([autocert.go](https://raw.githubusercontent.com/golang/crypto/master/acme/autocert/autocert.go)). Wildcards are therefore impossible with autocert.

### 1.5 The repo's existing Cloudflare client

`internal/cli/cloudflare_tunnel.go` contains an unexported, hand-rolled `cloudflareAPI` (~170 lines, plain `net/http`, bearer token) with `findZone` (`GET /zones?name=`), `ensureTunnel`, `setTunnelIngress`, `ensureDNSRecord` (CNAME upsert by name) and `tunnelToken`. It **can** be taught TXT writes — add `POST/DELETE /zones/{zone}/dns_records` for `type: TXT` and list by `name`+`type`+`content` for cleanup — and it would then satisfy `libdns.RecordAppender`/`RecordDeleter` if wrapped (~60 lines). But it lives in package `cli`, not in a package the Auth App can import, and `libdns/cloudflare` is itself stdlib-only, so reusing it saves no dependency weight. Recommendation: use `libdns/cloudflare` for ACME and keep the tunnel client as is; if the Auth App later needs A-record management for Machine Public Hostnames (ADR-0018 reconciler), `libdns/cloudflare`'s `SetRecords`/`DeleteRecords` covers that too, and the two can converge on one client then.

Existing prior art: ADR-0025 already builds the appliance's Caddy with `caddy-dns/cloudflare` (which is `libdns/cloudflare` under the hood) for wildcard Public Routes, with the token in a `0600` env file. The Auth App path adds a second consumer of a Cloudflare token; the "Caddy never sees Auth App secrets and vice versa" separation in ADR-0025 should be revisited in the ADR for #155, since the Auth App now holds a DNS-edit token by design.

---

## 2. Let's Encrypt — limits and policy (rate-limits page dated 2026-08-05)

Source: [letsencrypt.org/docs/rate-limits](https://letsencrypt.org/docs/rate-limits/) unless noted.

### 2.1 Limits that apply to this design

| Limit | Value | Override? |
|---|---|---|
| New certificates per **registered domain** per 7 days | **50**; "registered domain" = public suffix + 1 label (`hase.de`), counted per name in the order | yes, via the rate-limit adjustment form |
| New orders per account per 3 hours | 300 | yes |
| Certificates per exact identifier set per 7 days ("duplicate certificate") | **5** | **no** |
| Authorization failures per identifier per account per hour | 5 | no |
| Pending authorizations | no longer documented — retired with the 2024 GCRA-based rate limiter (no dated announcement found) | — |
| Names per certificate | 100 (classic profile) | — |

Consequence for #155: each zone-mode machine spends **one** of the 50/week for its zone (both `<m>.<pd>` and `*.<m>.<pd>` are under the same registered domain, one order). A destroy/recreate loop of the *same* machine name hits the 5-per-week duplicate limit first. Two certificates per machine (the `ManageSync` shape) would halve zone capacity to 25 machines/week.

### 2.2 Renewals

- Renewals **timed by ARI** (the client requests the cert inside the CA's suggested window and sets `replaces`) are exempt from **all** rate limits.
- Renewals for an identical identifier set without ARI are exempt from the new-orders and per-registered-domain limits but still count toward the 5-per-week duplicate limit.
- Integration guide: honour ARI, check twice daily, and cap retry backoff at once per day ([integration guide](https://letsencrypt.org/docs/integration-guide/)).

### 2.3 Staging

`https://acme-staging-v02.api.letsencrypt.org/directory`; much higher limits (30,000 certs per registered domain per week) and untrusted roots ([staging environment](https://letsencrypt.org/docs/staging-environment/)). certmagic's `DefaultACME.TestCA` points here and it is used automatically on the retry after a production failure.

### 2.4 Lifetimes, profiles, OCSP

- Profiles: `classic` 90 days (→ 64 days from 2027-02-10, → 45 days from 2028-02-16), `tlsserver` 45 days, `shortlived` 6 days. Plan renewal on ARI, not on a hard-coded 60-day cadence ([certificate lifetime changes](https://letsencrypt.org/2025/01/16/6-day-and-ip-certs/)).
- OCSP ended 2025-08-06; revocation is via CRLs. OCSP stapling code paths in certmagic are inert for Let's Encrypt ([ending OCSP](https://letsencrypt.org/2024/12/05/ending-ocsp/)).

### 2.5 Wildcards, DNS-01, and one order for base + wildcard

- Wildcard names require DNS-01 ([challenge types](https://letsencrypt.org/docs/challenge-types/)).
- An order may contain `m.hase.de` and `*.m.hase.de` (RFC 8555 §7.1.3 allows any set of identifiers; §7.1.3/§8.4). The CA creates **two authorizations**; both dns-01 challenges are validated at the same name `_acme-challenge.m.hase.de`, so both TXT values must be present concurrently until both authorizations are valid ([RFC 8555 §8.4](https://www.rfc-editor.org/rfc/rfc8555#section-8.4), [challenge types](https://letsencrypt.org/docs/challenge-types/)). Cloudflare permits multiple TXT records at one name; certmagic's solver appends and later deletes by value, so the two do not collide.
- Validation is multi-perspective since 2024-03-27: the primary plus remote vantage points must agree, so the TXT must be visible from Cloudflare's anycast edge globally before the challenge is posted ([multi-perspective validation](https://letsencrypt.org/2024/03/27/multi-perspective-validation/)).

---

## 3. Cloudflare

Sources: [API token permissions](https://developers.cloudflare.com/fundamentals/api/reference/permissions/), [token templates](https://developers.cloudflare.com/fundamentals/api/reference/template/), [create token](https://developers.cloudflare.com/fundamentals/api/get-started/create-token/), [API limits](https://developers.cloudflare.com/fundamentals/api/reference/limits/), [DNS records](https://developers.cloudflare.com/dns/manage-dns-records/how-to/create-dns-records/).

- **Minimum token for TXT + A writes in one zone:** `Zone > DNS > Edit`, resource-scoped to that zone. `Zone > Zone > Read` is only required if the client resolves zone *name* → zone *id* via `GET /zones?name=` (the "Edit zone DNS" template grants exactly `Zone.DNS:Edit`). If the Auth App stores the zone id at `sc admin add public-dns` time, DNS:Edit alone suffices at runtime; `libdns/cloudflare` looks up the zone by name, so with that provider grant Zone:Read too (or supply a separate `ZoneToken`).
- **Scoping below zone level:** not possible. Tokens scope to account/zone resources; there is no per-record or per-subdomain restriction (only a community feature request exists). The token can therefore edit every record in the zone — the mitigation is a dedicated zone (e.g. delegate `sc.hase.de` as its own Cloudflare zone) per install.
- **Rate limit:** 1,200 requests per 5 minutes per user across the API. One machine issuance = ~4–6 DNS calls (list zone, 2 appends, 2 deletes); irrelevant at Sandcastle scale.
- **Propagation:** Cloudflare states record changes propagate "globally within 5 minutes, usually much less" (typically seconds). Do not rely on a fixed sleep — use certmagic's authoritative-NS `Wait()` (2 min default timeout) or lego's 120 s/2 s pre-check.
- **TTL:** `Auto` = 300 s; minimum 60 s for DNS-only records. TXT challenge records should be short-TTL (certmagic `DNSManager.TTL`; lego default 120 s).
- `caddy-dns/cloudflare` (ADR-0025) documents `Zone.Zone:Read` + `Zone.DNS:Edit`; the same token can serve both Caddy and the Auth App if a single-token model is chosen.

---

## 4. Recommendation

**Adopt certmagic + acmez + libdns/cloudflare, used through `ACMEIssuer.Issue` with a two-SAN CSR; do not use `ManageSync`/`ManageAsync`.**

Concretely:

1. Add `github.com/caddyserver/certmagic` (pulls `mholt/acmez/v3`, `libdns/libdns`) and `github.com/libdns/cloudflare` to `go.mod`. Dependency cost: ~12 modules, all stdlib-based or already present (`x/crypto`, `x/net`); `zap` is the only "framework" dependency.
2. In the Auth App, build one `certmagic.Config{Storage: &certmagic.FileStorage{Path: <appliance data dir>/acme}}` and one `ACMEIssuer{CA: LetsEncryptProductionCA, Email: <operator>, Agreed: true, DisableHTTPChallenge: true, DisableTLSALPNChallenge: true, DNS01Solver: &DNS01Solver{DNSManager{DNSProvider: &cloudflare.Provider{APIToken: <zone token>}, TTL: 60s}}}`. `Storage` only holds the ACME account and locks; the machine certificates go into the Auth Database next to the machine row, since the reconciler already owns machine state (ADR-0018).
3. Per machine: generate a key, CSR with SANs `[<m>.<pd>, *.<m>.<pd>]`, call `Issue`, store cert + key + `NotAfter` + ARI window, push to the machine via Incus file push and restart Caddy (as settled in #155). The first attempt against a fresh install should be run against `TestCA` (staging) explicitly to burn no production budget while the DNS path is unproven.
4. Renewal: the existing 30 s reconciler tracks a per-cert `nextCheck`; twice a day call `GetRenewalInfo` and re-issue when inside the ARI window (fallback: 1/3 of lifetime remaining), setting the ARI `Replaces` context so the renewal is rate-limit exempt. Cap retries at once per day on failure.
5. Budget guard: keep a per-zone rolling 7-day counter of successful issuances in the Auth Database and refuse (or queue) new zone-mode machines at 45/50, surfacing "certificate pending: zone budget exhausted" — this is the "budget exhausted" behaviour still unspecified in #155.
6. Token: request `Zone > DNS > Edit` + `Zone > Zone > Read` on one zone per Public DNS Zone, and document that the token can edit every record in that zone (no sub-zone scoping exists) so operators dedicate a zone to Sandcastle.

**Rejected:**

- *certmagic via `ManageSync`* — two certificates per machine, doubling budget use and DNS-01 rounds; `SubjectTransformer` does not fix it.
- *lego v5* — functionally fine (native SAN orders, ARI helpers, propagation check) but 109 direct dependencies, no storage/renewal scaffolding, and a fresh v5 API break; nothing it does better than `ACMEIssuer.Issue` + `DNS01Solver`.
- *`x/crypto/acme` raw* — zero new dependencies, but no ARI, no solver, no propagation check, no account persistence; every one of those would be reimplemented and maintained here. `autocert` is ruled out outright (no DNS-01).
- *Reusing `internal/cli/cloudflare_tunnel.go`* — possible but saves nothing over `libdns/cloudflare`, which is equally light and already implements the solver interface.

---

## 5. Open points for the ADR / spec (not decided here)

- Whether to store certificates in the Auth Database or implement `certmagic.Storage` over SQLite and let certmagic own the layout — the former keeps one source of truth for machine state; the latter reuses `CacheManagedCertificate`.
- Single-token vs dual-token Cloudflare model per zone (`libdns/cloudflare` supports both).
- Whether the ADR-0025 Caddy token and the Auth App token are the same secret.
- Profile selection (`classic` vs `tlsserver`) — shorter lifetimes reduce revocation exposure but increase renewal traffic; ARI handles either.
