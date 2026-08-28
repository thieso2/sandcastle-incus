# Spec: support wildcard public routes (jot.moyn.dev)

> Follow-up: ADR-0025 proposes optional operator-configured Cloudflare DNS-01
> for a real wildcard certificate. This spec still defines the compatible
> default when no route DNS provider is configured.

Builds on `docs/shape/support-wildcard-public-routes-jot-moyn-fpjav1m.md`. That map located the
real surface (all under `internal/authapp/`, not the wish's stale `internal/route`/`internal/caddy`
paths) and settled three decisions: self-service authorization (no new gate), acme-only ingress
for this wish, issuance rate-limiting deferred. This spec pins down the behaviour precisely enough
to build and test against.

## Goal

A Tenant can publish a Route whose Hostname is `*.<domain>` (exactly one leading wildcard label)
and have every real subdomain under it (`foo.<domain>`, `bar.<domain>`, ...) proxy to one
Machine/port, each getting its own lazily-issued, broker-authorized on-demand TLS certificate.

## Non-goals (unchanged from shape, restated for the record)

- No real wildcard certificate, no DNS-01, no xcaddy plugin. Every distinct subdomain gets its own
  ACME leaf cert on first request.
- No acme-proxied (shared-front) support or tests. `RenderFrontSNIFragment` already round-trips a
  literal `*.` entry unmodified (confirmed by prior research); this wish makes no claim about it
  and adds no coverage for it.
- No new ownership-verification gate. Wildcards get the same self-service, first-come-by-PK
  treatment as custom hostnames today.
- No rate-limiting of per-subdomain ACME issuance.
- No multi-level wildcards (`a.b.<domain>`) — out of scope; wildcard cert semantics are
  single-leftmost-label only (RFC 2818 style), and this wish does not extend that.
- No change to Jot's tailnet-only workaround — separate follow-up.

## Behaviour

### 1. Hostname acceptance

`isValidDNSLabel` / `isValidPublicHostname` (`internal/authapp/routes_api.go`) gate every custom
Hostname reaching `routeHostname`. Today a hostname is valid iff it contains a `.` and every
dot-separated label passes `isValidDNSLabel` (lowercase alnum + internal hyphens, 1-63 chars).

Extend it: a hostname is also valid if its first label is the single character `*` and the
remaining labels (rejoined without the `*.` prefix) independently pass today's
`isValidPublicHostname`. Concretely:

- `*.jot.moyn.dev` → valid (remainder `jot.moyn.dev` is a valid public hostname).
- `*foo.jot.moyn.dev` → invalid (first label is not exactly `*`).
- `foo.*.jot.moyn.dev` → invalid (`*` not in the leftmost position — this rule only special-cases
  label index 0; a literal `*` anywhere else still fails `isValidDNSLabel` as today).
- `*.dev` → invalid, unchanged reasoning: the remainder `dev` has no `.`, so it already fails
  `isValidPublicHostname`'s existing "must contain a dot" rule. No new special-casing needed here —
  it falls out of applying the existing function to the remainder.
- `*` alone → invalid (no remainder).

This check applies only to the custom-Hostname path (`RoutePublishRequest.Hostname` →
`routeHostname`). The auto-subdomain label path (`RoutePublishRequest.Name` / Machine-name
fallback → `isValidDNSLabel`) is untouched and cannot produce a wildcard — auto-subdomain labels
are always server-derived from a single DNS label, never a hostname string.

### 2. Ownership / liveness signal (`RouteManager.Status`)

`Status` currently flags a custom hostname `awaiting-dns` when `hostResolves(ctx, route.Hostname)`
is false — i.e. it does a literal DNS lookup of the Hostname. A literal `*.jot.moyn.dev` string can
never resolve (no resolver answers a query containing a literal asterisk label), so unmodified this
would report a wildcard Route as permanently `awaiting-dns` even once the operator's wildcard DNS
record is live.

Change: when the Route's Hostname has `*.` as its first label, `Status` must resolve a synthesized
probe name instead of the literal Hostname — strip the `*.` prefix and prepend a random DNS label
(e.g. a short hex string) to the remainder, then run the existing `hostResolves` check against
*that* name. A wildcard Route is `live` (DNS-wise) iff that synthesized subdomain resolves; it stays
`awaiting-dns` otherwise. Non-wildcard hostnames are unaffected — same literal-hostname lookup as
today.

The probe label does not need to be unpredictable for security — this is a liveness signal, not an
authorization gate (per the shape doc's decision: no new ownership gate exists for exact hostnames
either, and wildcards get identical treatment). It only needs to be a syntactically valid label that
was very unlikely to have been provisioned on purpose, so a positive result means "the operator's
wildcard record is live," not "this one specific test label was configured."

`isCustomHostname` is unchanged — a wildcard Hostname is, definitionally, a custom hostname (it can
never equal the auto-subdomain suffix form), so it already routes into the custom-hostname branch of
`Status` correctly once the resolution target is fixed.

### 3. On-demand TLS authorization (`routesAsk`)

`routesAsk` (`internal/authapp/routes_api.go`) currently authorizes a requested `domain` only via
exact-match lookup (`RouteHostnameRegistered`). Extend the authorization check so a `domain` is also
authorized when it is covered by a registered wildcard Route, using the same single-leftmost-label
semantics Caddy/certmagic use for wildcard certs (`certmagic.MatchWildcard`, confirmed by prior
research: exactly one label — the leftmost — may be replaced by `*`):

- Exact match first: if `domain` is itself a registered Route Hostname, authorize (unchanged
  behaviour — this is also how exact-beats-wildcard precedence is expressed at this call site: the
  exact lookup is checked, and short-circuits, before the wildcard one).
- Else, if `domain` has two or more labels, strip its leftmost label and prepend `*.` to what
  remains; if *that* string is a registered Route Hostname, authorize.
- Else, deny (`403`, unchanged error shape).
- A `domain` that itself starts with `*.` is always denied outright — no real TLS handshake ever
  presents a literal `*` SNI, so authorizing one would only ever be a bug or a probe, never a
  legitimate on-demand request.

Worked cases, given a registered wildcard Route `*.jot.moyn.dev`:

| `domain` | Registered exactly? | Covered by `*.jot.moyn.dev`? | Result |
|---|---|---|---|
| `foo123.jot.moyn.dev` | no | yes | 200 |
| `bar.jot.moyn.dev` | no | yes | 200 |
| `jot.moyn.dev` (the bare zone) | no | no (0 extra labels) | 403 |
| `a.b.jot.moyn.dev` (two extra labels) | no | no (wildcard covers exactly one label) | 403 |
| `foo123.jot.moyn.dev`, also registered exactly | yes | yes | 200 (exact match short-circuits) |
| `evil.example.com` | no | no | 403 |
| `*.jot.moyn.dev` (literal) | — | — | 403 (always denied) |

### 4. Caddyfile rendering (`RenderCaddyfile`, `internal/authapp/routes_caddy.go`)

`RenderCaddyfile` writes each Route's Hostname verbatim as a site address (`%s {`). Caddyfile syntax
already accepts a leading-wildcard site address natively, and `tls { on_demand }` on such a block is
exactly the "ask per real SNI, issue a leaf cert per SNI" mechanism this wish relies on — so **no
code change to the rendering logic itself is expected**. This must be confirmed with a test (see
Test seams) rather than assumed, because it's the one blocker in the original wish text that maps to
"verify it still holds," not "change it":

- A registry containing a wildcard Route renders a `*.jot.moyn.dev {` block with `tls { on_demand }`
  and the correct `reverse_proxy 127.0.0.1:<local-port>`, identically to how an exact-host Route
  renders today.
- A registry containing both an exact Route (`foo.jot.moyn.dev`) and a covering wildcard Route
  (`*.jot.moyn.dev`) renders both blocks. Which one actually serves a request for `foo.jot.moyn.dev`
  is Caddy's own address-specificity sort (exact hosts outrank wildcard hosts) — not app logic. This
  wish does not add bespoke precedence code here; it adds a test that exercises this coexistence and
  confirms the exact block wins, per the settled decision "exact route wins."

If that confirmation test fails — i.e. Caddy does *not* prefer the exact block, or a wildcard site
address is rejected by Caddyfile parsing — that is new information that changes this spec's approach
to item 4, and should come back as a question, not be silently worked around.

### 5. Unaffected paths (confirm, don't change)

- `UpsertRoute`/`GetRoute`/`DeleteRoute`/`RouteHostnameRegistered` (`internal/authapp/routes.go`):
  treat the wildcard Hostname as an opaque string PK, exactly like any custom hostname. First-come
  global uniqueness, idempotent re-publish, conflict errors — all unchanged and apply as-is.
- `RouteManager.Publish`/`Delete`/`Reconcile`: a wildcard Route is one Route row, one proxy device,
  one local port, mapped to one Machine — identical lifecycle to a custom-hostname Route.
- `routeDeviceName`: hashes `normalizeHostname(hostname)`; a literal `*` in the input is fine, SHA-256
  input has no charset constraint.
- `RenderFrontSNIFragment` / `FrontMatcherName` (`internal/authapp/routes_front.go`): out of scope
  per the shape doc's acme-only decision. No change, no new test in this wish.

## Test seams

Concrete places to hang tests, matching the existing per-file test suites:

- **`internal/authapp/routes_api_test.go`**
  - `isValidDNSLabel` / `isValidPublicHostname` — pure functions, table-driven cases from the
    worked examples in §1 (`*.jot.moyn.dev` valid, `*foo...`/`foo.*...`/`*.dev`/`*` invalid).
  - `routeHostname` — table test over `RoutePublishRequest{Hostname: "*.jot.moyn.dev"}` on a
    `handler` fixture (existing pattern: `TestRoutePublishAPI_CustomHostname`).
  - `routesAsk` — extend the existing `TestRouteAskAPI_GatesUnregisteredHostnames` pattern (an
    `httptest`-driven call through `handler.routesAsk`) with cases from the §3 table: register a
    wildcard Route, assert 200/403 per row. Recommend factoring the exact-then-wildcard matching
    into a small pure helper (e.g. `routeCoveringWildcard(domain string) (string, bool)`) so the
    matching logic itself has a direct unit test independent of the HTTP plumbing.

- **`internal/authapp/route_manager_test.go`**
  - `RouteManager.Status` — extend the `TestStatus_CustomHostnameAwaitingDNS` /
    `TestStatus_AutoSubdomainNeverAwaitingDNS` pattern: publish a wildcard Route, inject
    `ResolveHost` as a recording fake, assert (a) it is never called with the literal
    `*.jot.moyn.dev`, (b) it is called with some `<label>.jot.moyn.dev` string, and (c) `Status`
    reports `live`/`awaiting-dns` correctly depending on the fake's return value.

- **`internal/authapp/routes_caddy_test.go`**
  - `RenderCaddyfile` — pure function, existing seam. Add: a wildcard-only registry (assert the
    rendered block's address line and `tls { on_demand }`), and an exact+wildcard coexistence
    registry (assert both blocks render; if there's an available `caddy validate`/`caddy adapt`
    check in this repo's test tooling, use it here to confirm Caddy itself prefers the exact block —
    otherwise this precedence claim needs an integration/e2e-level check instead, see below).

- **`internal/authapp/routes_test.go`** — no new seam needed; existing `UpsertRoute`/`GetRoute`
  round-trip tests already cover arbitrary hostname strings as PKs, so a case using
  `*.jot.moyn.dev` as the hostname is a cheap addition to confirm no code path chokes on the `*`
  character, but no new behaviour is being tested here.

- **E2E (`internal/e2e`, `SANDCASTLE_INCUS_E2E=1`) / `docs/e2e-sc2.md`**: add a phase publishing
  `*.jot.moyn.dev`, then hitting at least two distinct random subdomains and confirming each gets a
  live proxied response with its own cert (per the shape doc's "good outcome" #6). This is the only
  seam that exercises the real ACME/on-demand-TLS path end to end; the unit-level seams above cover
  the authorization and rendering logic but stub out actual certificate issuance.

## Documentation to update alongside the code (per `CLAUDE.md`)

- `docs/usage.html` — a wildcard `sc route publish --hostname '*.jot.moyn.dev'` example.
- `docs/e2e-sc2.md` — the phase/PASS criterion described above.
- `implementation-notes.md` — record (a) leaf-cert-per-SNI instead of a real wildcard cert, and (b)
  reliance on Caddy's own address-specificity sort for exact-vs-wildcard precedence rather than
  app-level logic, once §4's confirmation test has actually run and it's known whether that reliance
  holds.

## Open risk carried forward, not re-litigated here

Per the shape doc: self-service wildcard registration, acme-only scope, and no issuance
rate-limiting are settled human decisions, not open questions of this spec.
