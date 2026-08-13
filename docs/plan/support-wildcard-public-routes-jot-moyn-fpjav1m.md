# Plan: support wildcard public routes (jot.moyn.dev)

Builds on the shape and spec docs. Real surface is `internal/authapp/` (the wish's
`internal/route`/`internal/caddy`/`internal/infra` paths are stale — deleted in the v1 purge).
Four slices: two independent core-behaviour changes, one confirmation-only slice, one closing
slice that needs all three done.

## t1 — Accept and authorize wildcard hostnames

Files: `internal/authapp/routes_api.go`, `internal/authapp/routes_api_test.go`.

1. `isValidPublicHostname`: accept a hostname whose first label is exactly `*` when the
   remainder (rejoined without the `*.` prefix) independently passes the existing
   `isValidPublicHostname`. `*foo.jot.moyn.dev` (not exactly `*`), `foo.*.jot.moyn.dev` (`*` not
   leftmost), `*.dev` (remainder has no dot), and `*` alone stay invalid — these fall out of
   applying the existing rules to the remainder, no extra special-casing needed beyond the
   leading-label check.
2. `routesAsk`: extend authorization beyond exact match. Factor the matching logic into a small
   pure helper, e.g. `routeCoveringWildcard(domain string) (string, bool)`, that: strips
   `domain`'s leftmost label (only if it has ≥2 labels) and prepends `*.`, returning that
   candidate wildcard hostname. In `routesAsk`: try exact match first (unchanged, and this is
   what makes exact-beats-wildcard precedence hold at this call site — short-circuit on exact
   before trying wildcard); else try the wildcard-covering match against `RouteHostnameRegistered`;
   else 403. A `domain` that itself starts with `*.` must be denied outright regardless of
   registration state — no real TLS handshake ever presents a literal `*` SNI.
3. Tests (table-driven, extend existing suites):
   - `isValidDNSLabel`/`isValidPublicHostname` cases from point 1 above.
   - `routeHostname` with `RoutePublishRequest{Hostname: "*.jot.moyn.dev"}`.
   - `routesAsk`/`routeCoveringWildcard`: register `*.jot.moyn.dev`, assert against the worked
     table in the spec (§3): `foo123.jot.moyn.dev`→200, `bar.jot.moyn.dev`→200,
     `jot.moyn.dev`→403, `a.b.jot.moyn.dev`→403, `evil.example.com`→403, literal
     `*.jot.moyn.dev`→403 always, and an exactly-registered hostname that's also wildcard-covered
     →200 (exact short-circuit).

No dependency on t2/t3 — `routesAsk` and `isValidPublicHostname` are pure/self-contained; tests
register routes directly via `UpsertRoute`, not through the CLI publish path.

## t2 — Caddyfile rendering: confirm wildcard site blocks and exact-vs-wildcard precedence

Files: `internal/authapp/routes_caddy.go`, `internal/authapp/routes_caddy_test.go`.

`RenderCaddyfile` writes each Route's Hostname verbatim as a site address; Caddyfile syntax
already accepts a leading-wildcard address, so **no rendering code change is expected** — this
slice is a confirmation test, not a feature build.

1. Add a test: a registry containing only a wildcard Route (`*.jot.moyn.dev`) renders a
   `*.jot.moyn.dev {` block with `tls { on_demand }` and the correct
   `reverse_proxy 127.0.0.1:<local-port>`, matching how an exact-host Route renders today.
2. Add a test: a registry containing both `foo.jot.moyn.dev` (exact) and `*.jot.moyn.dev`
   (wildcard, covering it) renders both blocks.
3. If this repo's test tooling has a way to actually run/validate the rendered Caddyfile (check
   for a `caddy` binary or `caddy adapt`/`caddy validate` invocation elsewhere in the test suite),
   use it to confirm Caddy itself prefers the exact block for a request to `foo.jot.moyn.dev`
   (the settled decision: exact route wins). If no such tooling exists in this repo, don't add
   one from scratch — note in `implementation-notes.md` that precedence relies on Caddy's own
   address-specificity sort and was confirmed by reading Caddyfile address-matching semantics,
   not by an executable test, and say so plainly.
4. **If either confirmation test fails** — Caddy rejects the wildcard site address, or (if you
   found a way to test it) the exact block does not win — stop, do not work around it in this
   slice, and report the failure clearly: that's new information changing the spec's approach to
   this blocker, not a bug to silently patch.

No dependency on t1 — tests register Route rows directly, bypassing the publish/validation path.

## t3 — Status DNS-liveness probe for wildcard routes

Files: `internal/authapp/route_manager.go`, `internal/authapp/route_manager_test.go`.

`RouteManager.Status` currently marks a custom hostname `awaiting-dns` when
`hostResolves(ctx, route.Hostname)` is false — a literal DNS lookup of the Hostname. A literal
`*.jot.moyn.dev` string can never resolve, so unmodified this reports a wildcard Route as
permanently `awaiting-dns` even once the operator's wildcard DNS record is live.

1. In `Status` (or a small helper it calls), when `route.Hostname`'s first label is `*`,
   synthesize a probe name instead of resolving the literal Hostname: strip the `*.` prefix and
   prepend a random-looking DNS label (e.g. a short hex string) to the remainder, then run the
   existing `hostResolves`/`ResolveHost` check against that name. The probe label does not need
   to be cryptographically unpredictable — this is a liveness signal, not an authorization gate.
2. Non-wildcard hostnames are unaffected — same literal lookup as today.
3. `isCustomHostname` needs no change — a wildcard Hostname is definitionally a custom hostname
   already.
4. Test (extend `TestStatus_CustomHostnameAwaitingDNS`-style pattern): publish/construct a
   wildcard Route, inject `ResolveHost` as a recording fake, assert (a) it is never called with
   the literal `*.jot.moyn.dev`, (b) it is called with some `<label>.jot.moyn.dev` string, and
   (c) `Status` reports `live`/`awaiting-dns` correctly depending on the fake's return value.

No dependency on t1/t2 — `Status` takes a `Route` value directly; tests construct it without
going through hostname validation or Caddy rendering.

## t4 — Docs, e2e coverage, implementation notes

Depends on: t1, t2, t3 (documents and exercises the completed behaviour end to end; writing it
before the others land would describe code that doesn't exist yet).

1. `docs/usage.html` — add a wildcard publish example:
   `sc route publish --hostname '*.jot.moyn.dev' ...` (match existing custom-hostname example
   style/flags in that file).
2. `docs/e2e-sc2.md` — add a phase/PASS criterion: publish a wildcard Route, then hit at least
   two distinct random subdomains under it and confirm each gets a live proxied response with
   its own on-demand-issued cert (per the spec's e2e test seam). Follow the doc's existing
   phase/PASS format.
3. `implementation-notes.md` — append a dated entry recording the two non-obvious choices made
   across t1-t3: (a) leaf-cert-per-SNI on every real subdomain instead of one real wildcard
   certificate (no DNS-01, no xcaddy plugin), and (b) reliance on Caddy's own address-specificity
   sort for exact-vs-wildcard precedence rather than app-level logic — state plainly whether t2
   confirmed this by executable test or by reading Caddyfile semantics, per whatever t2 actually
   found.
4. If `internal/e2e` has a runnable harness (`SANDCASTLE_INCUS_E2E=1 go test ./internal/e2e`)
   and adding a real end-to-end case is feasible without new infra, add it; otherwise the
   `docs/e2e-sc2.md` phase description is the deliverable for this slice (it's a protocol doc,
   not code, and is the repo's stated source of truth for expected end-to-end behaviour).

## Not in any slice (confirmed unaffected, no ticket needed)

- `UpsertRoute`/`GetRoute`/`DeleteRoute`/`RouteHostnameRegistered`/`routeDeviceName` — treat the
  wildcard Hostname as an opaque PK string; nothing to change.
- `RouteManager.Publish`/`Delete`/`Reconcile` — one Route row, one proxy device, identical
  lifecycle to a custom-hostname Route.
- `RenderFrontSNIFragment`/`FrontMatcherName` (`internal/authapp/routes_front.go`) — acme-proxied
  path, explicitly out of scope for this wish (acme-only decision).
