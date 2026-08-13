# Shape: support wildcard public routes (jot.moyn.dev)

## What this wish is

Let a tenant publish one Public Route whose Hostname is a single-label wildcard
(`*.jot.moyn.dev`), mapping every subdomain to one Machine/port. TLS is covered
by extending the on-demand-TLS broker coordination that already exists for
exact hostnames: Caddy requests a normal (non-wildcard) leaf certificate per
actual incoming SNI, and the Auth App's `ask` endpoint authorizes it because
the SNI is covered by a registered wildcard Route — not a DNS-01 / real
wildcard certificate.

This is an extension of an existing mechanism, not new machinery: broker-gated
on-demand TLS for exact hostnames already ships (`routesAsk` in
`internal/authapp/routes_api.go`, `RouteHostnameRegistered` in
`internal/authapp/routes.go`). The work is teaching four call sites about a
leading `*.` label.

## Key fact: the wish's file paths are stale

`internal/route/plan.go`, `internal/route/dnsproof.go`, `internal/caddy/render.go`,
and `internal/infra/plan.go:645` (the wish's "current blockers") do not exist —
deleted in the v1 purge (commit `842d3e5`). The real surface today, all under
`internal/authapp/`:

| Wish's blocker | Real location |
|---|---|
| #1 hostname validation | `isValidPublicHostname` / `isValidDNSLabel`, `routes_api.go` |
| #2 ownership proof | `RouteManager.isCustomHostname`/`hostResolves`, `route_manager.go` (status/`awaiting-dns` computation — see below, not a publish-time gate) |
| #3 Caddyfile site rendering | `RenderCaddyfile`, `routes_caddy.go` |
| #4 on-demand cert issuance / broker coordination | `routesAsk` + `RouteHostnameRegistered`, `routes_api.go` / `routes.go` — **already implements broker-coordinated on-demand TLS**, just exact-match only today |

`internal/authapp/routes_front.go` (`RenderFrontSNIFragment`) is the
acme-proxied shared-front fragment; research (prior round) confirmed it
already round-trips a literal `*.jot.moyn.dev` string unmodified via
`certmagic.MatchWildcard` semantics, so it needs no code change — it's just
out of scope for verification in this wish (see below).

## What this wish is not

- **Not a real wildcard certificate.** No DNS-01 challenge, no xcaddy plugin.
  Every distinct subdomain gets its own ACME-issued leaf cert, lazily, the
  first time it's requested.
- **Not acme-proxied support.** Scoped to `--route-ingress acme` only. The
  shared-front SNI fragment likely already works unmodified (per research),
  but this wish adds no tests or guarantees for that path — validating an
  un-vendored third-party dependency's (`mholt/caddy-l4`) wildcard-SNI
  behavior in production is deferred.
- **Not a publish-time ownership-verification gate.** None exists today for
  exact custom hostnames either — registration is first-come via the DB's
  `hostname` PRIMARY KEY, and actual domain control is proven implicitly,
  later, when (and only when) ACME issuance for a real request succeeds.
  Wildcards get the identical treatment: self-service, no new gate.
- **Not rate-limiting.** A popular wildcard could trigger many per-subdomain
  ACME orders in a burst; not addressed here.
- **Not Jot's migration off tailnet-only.** Separate follow-up once wildcard
  routing exists.

## Decisions taken (prior round, by the human)

1. **wildcard-authz-scope: self-service**, same tier as custom hostnames — no
   extra approval/allowlist. *Why:* matches the existing model exactly; there
   is no ownership-proof mechanism to hang a stricter gate on regardless.
2. **wildcard-ingress-mode-scope: acme-only now**, acme-proxied is a
   follow-up. *Why:* keeps this wish from having to validate an unpinned
   dependency's wildcard-SNI forwarding in production; acme-only is what Jot
   needs today.
3. **wildcard-issuance-rate-limit: deferred, accept risk.** *Why:* a real but
   separate concern; out of scope here.

## What a good outcome looks like

1. Hostname validation accepts a first label that is exactly `*` (not `*foo`,
   not a `*` in any other position) followed by an otherwise-valid label
   chain — reachable only through the custom-Hostname publish path
   (`RoutePublishRequest.Hostname`), never the auto-subdomain label path
   (auto-subdomains are always server-derived, never wildcards).
2. `RenderCaddyfile` emits a wildcard site block
   (`*.jot.moyn.dev { tls { on_demand } ... }`) the same way it emits exact
   ones. Exact-beats-wildcard precedence relies on Caddy's own route
   specificity sorting — no bespoke precedence logic — but this should be
   confirmed with a test/e2e case where a hostname is covered by both an
   exact Route and a wildcard Route on the same install.
3. `routesAsk` authorizes the *real* incoming SNI (e.g. `foo123.jot.moyn.dev`,
   never a literal `*.`) by checking both an exact match and — strip the
   leftmost label, prefix `*.` — a wildcard-Route match, mirroring
   `certmagic.MatchWildcard`'s single-leftmost-label semantics.
4. `RouteManager.Status`'s DNS-liveness check must stop resolving the literal
   `*.jot.moyn.dev` for a wildcard Route (a literal `*.` label never
   resolves, so today's logic would report `awaiting-dns` forever); resolve a
   synthesized random subdomain instead, per the wish's stated approach. This
   is the modern equivalent of blocker #2.
5. `sc route publish --hostname '*.jot.moyn.dev'` works through the existing
   custom-hostname path with no new CLI flag; the existing `awaiting-dns`
   CNAME hint text is already correct for a wildcard CNAME record.
6. `docs/usage.html` and `docs/e2e-sc2.md` updated per `CLAUDE.md`'s
   documentation policy: a wildcard publish example, and an e2e
   phase/PASS criterion covering wildcard Route → `ask` gate → per-subdomain
   on-demand cert → live proxy for at least two distinct random subdomains
   under one wildcard.
7. `implementation-notes.md` gets an entry recording two non-obvious choices:
   leaf-cert-per-SNI instead of a real wildcard cert, and reliance on Caddy's
   default route-specificity sort for exact-vs-wildcard precedence rather
   than app-level logic.
