# Spec: admin-server config-toggled event-bus cache for `sc ls`

Builds on `docs/shape/admin-server-config-toggled-event-bus-ca8marg.md`. Read that first — this
spec inherits its "settled without escalation" and "decisions taken" sections without repeating
their reasoning, and only restates a conclusion where the exact behavior needs pinning down
further than the shape left it.

## Problem statement

`sc ls` resolves every invocation against live Incus API: one `GetProjects` call, then one
`GetInstancesFull` call per project (already issued **concurrently**, one goroutine per project —
not sequentially, correcting a claim in the shape doc; see `internal/incusx/machine_store.go:110-115`).
On an install with several projects, each carrying multi-second server-side latency, the
concurrent fan-out still costs the *slowest* project's latency every single invocation — the
motivating trace shows `sc ls -a` taking over 8 seconds wall-clock while individual calls run
1-8s. There is no caching; every invocation re-pays this cost, and it scales with project count.

## Solution, in one paragraph

The Auth App (`sc-adm auth-app serve`) grows an in-memory cache of Incus resources — instances,
networks, storage volumes, profiles, images, across all projects — seeded by one full read at
startup and kept current afterward purely by consuming Incus lifecycle events (no periodic
resync). A new bearer-authenticated endpoint on the Auth App answers `sc ls`-shaped queries
against that cache. A server-side-only config toggle (env var read once at `auth-app serve`
startup, same mechanism as `SANDCASTLE_ROUTE_INGRESS`) decides whether `sc ls` is offered the
cache path at all; whenever the cache path isn't a safe answer for any reason, `sc ls` falls back
to exactly today's live per-project query path, silently for the request shapes that path already
serves.

## Scope

**In scope:**
- Auth App: cache population (full read + event-driven incremental updates), the new
  cache-backed endpoint, the config toggle.
- `sc ls` (the `newListCommand` in `internal/cli/list.go`, aliased from `sc list`): routing between
  cache-backed and live paths, and new flags/output for the newly-cached resource types.
- Documentation: `docs/usage.html`, `docs/e2e-sc2.md`, `implementation-notes.md`, and a new ADR.

**Out of scope (per the shape doc; restated here as hard boundaries):**
- `sc-adm list` / `sc admin list` (`newAdminMachineListCommand`, `internal/cli/admin_machine.go`)
  keeps its current live-only behavior unconditionally. It shares `listMachines()` with `sc ls`
  today; this wish does not touch that call site. Admins listing a busy install still see the
  original multi-second trace after this ships.
- No new standalone service — this extends the existing Auth App process and its existing
  bearer-token endpoint family (`/api/tenants`, `/api/projects`, `/api/routes`, …).
- No new authorization model — the new endpoint scopes results exactly as `/api/tenants` does
  today (`requireBearerUser` + tenant-access filtering). A cache-backed answer must never show a
  caller anything their bearer token wouldn't already show them live.
- No rethink of `sc ls`'s addressing/globbing/multi-remote-sweep grammar. Every remote in a sweep
  (`sc ls -a 'g*:d*'`) keeps deciding cache-vs-live independently — its own Auth App, own toggle,
  own cache state — the same way `listMachinesAcrossRemotes` already treats each remote
  independently today (report a broken one as a warning, still show the rest).
- No periodic full-resync safety net for the cache. It is kept current from Incus lifecycle
  events alone. (Contrast: the existing DNS reconciler, ADR-0018, deliberately pairs a *trimmed*
  event trigger set with a 30s ticker precisely because it excludes several lifecycle actions to
  cut noise. This cache doesn't have that luxury — `sc ls` output depends on the fuller set of
  lifecycle actions per resource type — so the accepted risk is: an event Incus doesn't emit, or
  one missed during a reconnect gap, leaves the cache silently stale for that one resource until
  its next event. Not mitigated by a ticker. If this proves to be a problem in practice, adding
  one is a known, already-proven-in-this-codebase follow-up, not something blocked here.)
- No runtime toggle changes — like `SANDCASTLE_ROUTE_INGRESS`, the toggle is read once at
  `auth-app serve` startup; flipping it means restarting the Auth App.
- Incus storage **pools** are out of scope; the cached "storage" resource type is storage
  **volumes**. Pools are a server-level construct with no project scoping; volumes (like
  instances, networks, profiles, and images) are addressable per-project in Incus
  (`features.storage.volumes`, already used to scope tenant projects — `internal/incusx/tenant_create_v2.go:313`)
  and are what a tenant listing their own resources means by "storage."

## Ground truth this spec relies on (verified against current code, not the shape doc's paraphrase)

- `sc ls` has exactly one flag today: `-a`/`--all-projects` (`internal/cli/list.go:110`). There is
  no `-u`/`--include-unmanaged` flag — it existed once and was removed in a prior commit.
  `Unmanaged []machine.UnmanagedMachine` is populated and shown unconditionally today (and, since
  every v2 tenant's instances are already first-class machines, is always empty in practice — see
  `internal/incusx/machine_store.go:91-96`). Any flag list in this spec reflects this; don't
  reintroduce `-u` as if restoring existing behavior.
- Addressing grammar `[[remote:]project[:machine]]`, all three parts glob-capable
  (`internal/cli/list.go:61-79,155-180`), is unaffected by this wish and must keep working
  identically over both the cache and live paths.
- `VERBOSE=1` tracing today prints `[verbose] incus api: <label> done (<duration>)` lines
  (`internal/incusx/api_log.go`). The cache path's tracing (see Behavior §B7) must fit this same
  convention so a human reading a trace can tell which path a listing took.
- ADR-0021 ("one Incus remote per install") is **Status: proposed**, not accepted, and establishes
  remote↔install cardinality only. "One remote = one Auth App = one cache" is this wish's own
  extrapolation from that decision, not something ADR-0021 itself states. The new ADR this wish
  adds (see §Documentation) should make that lineage explicit rather than cite ADR-0021 as if it
  already settled the cache's cardinality.

## Behavior specification

### B1 — Auth App startup: cache seeding

When the config toggle (§B4) is on, `auth-app serve` startup performs one full read, across every
Incus project, of each of: instances, networks, storage volumes, profiles, images. The cache is
**not ready** until this read completes successfully for all five resource types across all
projects — readiness is a single gate, not per-resource-type (per the shape doc), and this spec
extends that: it is also not per-project. A failure reading any single project or resource type
during the initial read keeps the whole cache not-ready; Auth App retries the full read (with
backoff) rather than giving up, so a transient Incus outage at boot doesn't permanently strand the
cache in "never ready." Auth App must not fail to start, and must not block serving its other
endpoints, while this read is in flight or retrying — `sc ls` (and everything else the Auth App
serves) must keep working via existing paths throughout.

When the toggle is off, none of this happens: no full read, no event subscription, no
steady-state cost.

**How you'd know it's wrong:** start `auth-app serve` with the toggle on against a live Incus,
immediately issue `sc ls` before the startup log/metric says the cache is ready — it must return
correct results (via fallback) with no error and no partial/wrong data. Kill Incus reachability
during the initial read (e.g. block the socket) — the Auth App process must still accept
connections and answer non-cache endpoints normally.

### B2 — Keeping the cache current: event-bus consumption

Once ready, the cache is updated **only** by consuming Incus lifecycle events
(`GetEventsAllProjects()`), following the reconnect pattern already proven by
`subscribeInstanceLifecycleEvents` (`internal/cli/admin_dns_events.go:56-96`: reconnect with
backoff on socket drop, blocking `AddHandler`/`Wait` loop). Unlike that reconciler's trimmed
`dnsRelevantLifecycleActions` allow-list, this cache must subscribe to the **full** set of
lifecycle actions relevant to each resource type's cached fields — instance create / delete /
rename / start / stop / restart / restore / resume / shutdown / **and update** (a field the DNS
reconciler's trigger set explicitly excludes but `sc ls` output depends on, e.g. an IP or NIC
change reflected in `PrivateIP`), plus the equivalent create/update/delete/rename lifecycle
actions Incus emits for networks, storage volumes, profiles, and images.

**Readiness during a connection gap.** The event socket dropping is itself one of the two
explicit fallback triggers named in the wish's acceptance criteria ("the event bus connection has
dropped or gone stale"). This spec reads "gone stale" as synonymous with "not currently connected"
— there is no additional heartbeat/max-age staleness check beyond connectivity, since nothing in
the wish asks for one and the shape doc's "no periodic resync" decision argues against building
extra staleness machinery. Concretely: readiness is revoked the moment the event listener
disconnects, and restored the moment it reconnects — **without** a fresh full read on reconnect
(consistent with the accepted risk that events missed during the gap leave that one resource
silently stale until its next event, not resolved by a resync).

**How you'd know it's wrong:** with the cache ready, create/stop/delete/rename an instance (and,
separately, a network/storage volume/profile/image) directly via Incus (bypassing `sc`) — a
subsequent cache-backed `sc ls` must reflect the change without any `sc` command having triggered
a refresh. Kill the Incus event socket (e.g. restart the Incus daemon, or block the event
endpoint) — the next `sc ls` must fall back to the live path (verifiable via `VERBOSE=1`) until
the subscriber reconnects, at which point cache-backed answers resume.

### B3 — The new endpoint

A new bearer-authenticated HTTP endpoint on the Auth App answers cache-backed listing requests,
following the existing `/api/tenants`-style contract exactly: `requireBearerUser` (403 on
failure), then tenant-access scoping equivalent to `accessibleTenantSummaries` (a cache-backed
answer is filtered to what that bearer token's user could already see live — never more).

**Request:** must be able to express every filter/wildcard `sc ls` already accepts today —
project name or glob, machine name or glob, all-projects — plus whichever request shape the new
resource-type flags need (exact field names are an implementation call; the requirement is
functional coverage, not a wire format).

**Response, cache ready:** data equivalent in shape to what `listMachines()`/`listPayload`
produces today for instances, extended with the four newly-cached resource types, filtered
exactly as the request asked.

**Response, cache not available** (toggle off, not yet ready, event stream disconnected, or any
other reason the cache can't answer *right now*): a response the caller can distinguish, in the
same round trip, from "the cache answered and the result is empty." This must not require the CLI
to make a separate probe call before the real request — that would reintroduce a round trip on
the fast path, defeating the point. (Exact status code/envelope is an implementation call; the
requirement is: one request, and the response unambiguously says whether it's an authoritative
cache answer or a "go live" signal.)

**How you'd know it's wrong:** a bearer token scoped to tenant A must never see tenant B's
resources through this endpoint even though the Auth App's cache holds every tenant's data — test
this explicitly, since it's the one place a cache-wide data structure could leak across a
boundary the live per-tenant path enforces implicitly by construction (the live path never even
fetches another tenant's projects).

### B4 — The config toggle

Server-side only, no CLI flag. Read once via `os.Getenv` at `auth-app serve` (`ExecuteAdmin`)
startup, following the identical mechanism as `SANDCASTLE_ROUTE_INGRESS`/`SANDCASTLE_AUTH_INGRESS_MODE`
(`internal/cli/admin_root.go`): env var → plain field on the struct passed into the Auth App →
threaded down to wherever the cache subsystem and the new endpoint handler read it.

**Default: on** (opt-out). An operator who wants the pre-cache behavior sets the env var
explicitly. This is safe specifically because fallback-when-not-ready is mandatory (§B1, B2,
B6) — "on by default" only changes observable behavior once the cache has already proven itself
ready; it never removes the safety net.

**How you'd know it's wrong:** start `auth-app serve` with the toggle set to disable it — no full
read happens, no event subscription starts, and `sc ls` traffic against that install shows exactly
today's live-query trace, with no added latency from probing a cache that was never going to
answer.

### B5 — `sc ls`: cache-vs-live routing and fallback

`sc ls` tries the cache-backed endpoint first, under the same kind of precondition check the
codebase already uses elsewhere for "prefer the Auth App API, fall back otherwise" (see
`internal/cli/project_v2.go:49`, the `sc project create` precedent — check preconditions, and fall
through unconditionally rather than surfacing an error when they aren't met). Preconditions /
fallback triggers, matching wish acceptance criterion 6 plus criteria already implicit in what
"safe answer" means:

- No stored `AuthToken` for this install, or no resolvable Auth Hostname (`commandAuthHostname`
  returns empty) — go straight to live, exactly as `sc ls` does today (it never attempts an
  Auth App call for instance listing currently, so this is a zero-regression precondition, not new
  behavior).
- The endpoint is unreachable (network error, timeout, non-2xx unrelated to the "not available"
  signal) — go live.
- The endpoint's own answer says "not available" (§B3) — go live.

In every fallback case, `sc ls`'s output must be **identical** to what it produces today for that
same invocation shape and underlying Incus state — this is the single most important invariant,
and the reason the cache and live paths must be tested against the same expected output rather
than independently. No user-visible error, no user-visible difference in output — the only
observable difference is latency, and (under `VERBOSE=1`) a trace line saying which path was
taken and, on fallback, why.

**How you'd know it's wrong:** run the exact same `sc ls` invocation twice against the same
underlying Incus/cache state — once with the toggle on and cache ready, once with the toggle
forced off — and diff the two outputs (text and `--output json`). They must be byte-identical
apart from ordering that was already unordered before this wish.

### B6 — New resource-type flags (networks, storage, profiles, images): cache-only, explicit about it

`sc ls` gains the ability to show the four newly-cached resource types (exact flag
names/columns are an implementation call — follow the existing table-based UX in
`internal/cli/list.go`, e.g. `formatMachineList`/`formatMultiMachineList`'s conventions). Unlike
instance listing, **there is no live equivalent code path for these types** — nothing in
`internal/incusx` reads networks or storage volumes today, `GetProfiles`/`GetImages` exist only
for teardown, not listing. This creates one case where "transparent fallback" as described in B5
cannot apply, because there is nothing to fall back *to*.

**Decision:** when a new resource-type flag is used and the cache can't answer (toggle off, not
ready, disconnected), `sc ls` must say so plainly — a clear, actionable error naming the reason —
rather than silently returning an empty list for that type, or silently downgrading to
instances-only output. Silently omitting a resource type the user explicitly asked to see would
read as "you have none," which is a correctness bug, not a graceful degradation. This only applies
to the *new* flags; a plain `sc ls` (today's default, instances-only) always falls back per B5
regardless of these flags' state.

This reading is consistent with, not in tension with, the wish's own acceptance text: "with the
toggle disabled, `sc ls` behavior is unchanged from today (live per-project queries only,
instances-only output)" describes the *default* invocation, not what happens when a user
explicitly opts into a cache-only view while the cache is unavailable.

**How you'd know it's wrong:** `sc ls --networks` (or whatever the flag turns out to be named)
against an install with the toggle off must produce a clear error, not an empty table and not a
silent instances-only listing.

### B7 — `VERBOSE=1` tracing for the cache path

Fits the existing convention (`[verbose] incus api: <label> done (<duration>)`,
`internal/incusx/api_log.go`) so a human reading a trace can tell, at a glance: which path a given
`sc ls` invocation took, and — on fallback — why. This is the acceptance criteria's own
verification mechanism ("a difference only shows up under `VERBOSE=1`"), so it is itself part of
required behavior, not incidental logging.

## Acceptance scenarios

1. **Cache hit, fast.** Toggle on, cache ready, `sc ls -a` against an install with several
   projects → near-instant response (no multi-second per-project waterfall), output identical to
   the live path's output for the same state.
2. **Cache not yet seeded.** Toggle on, Auth App just started, initial full read incomplete →
   `sc ls` falls back to live, output identical to toggle-off behavior, no user-visible error.
3. **Event stream down.** Toggle on, cache was ready, event socket then drops → `sc ls` falls back
   to live until the subscriber reconnects; once reconnected, cache-backed answers resume without
   requiring an Auth App restart.
4. **Toggle off.** `sc ls` behavior, output, and latency are unchanged from pre-wish baseline; no
   cache seeding or event subscription cost is paid by the Auth App process.
5. **Instance lifecycle reflected.** Cache ready; an instance is created, started, stopped,
   renamed, or deleted directly via Incus (not through `sc`) → a subsequent cache-backed `sc ls`
   reflects it without any explicit refresh action.
6. **Non-instance lifecycle reflected.** Same as (5), for a network, storage volume, profile, and
   image change each.
7. **Tenant isolation holds.** A bearer token scoped to tenant A never sees tenant B's resources
   via the new endpoint, even though the cache holds all tenants' data.
8. **New-type flag without a cache.** `sc ls --networks` (or equivalent) with the toggle off, or
   cache not ready → a clear, explicit error; never a silent empty/instances-only result.
9. **No login.** An `sc` install with no stored `AuthToken` → `sc ls` goes straight to live,
   exactly as today, no failed cache-endpoint call slowing anything down.
10. **Multi-remote sweep, mixed readiness.** `sc ls -a 'g*:d*'` spanning two enrolled installs,
    one with the toggle on and cache ready, one with it off → each remote's listing uses its own
    path independently; the combined output is indistinguishable from what today's all-live sweep
    would show for the same underlying state (aside from latency), and a broken/unreachable
    install is still reported as a warning rather than failing the whole sweep, per existing sweep
    behavior.

## Test seams

- **Cache population (Auth App-internal, highest-value seam).** The logic that turns "one full
  read across five resource types across N projects" plus "a stream of lifecycle events" into "the
  in-memory cache state" should be testable as pure Go, with the Incus SDK and event listener
  behind interfaces — the same style as `internal/incusx`'s existing fakes for `TopologyServer`/
  `HostOverrideServer`/`sharedTenantListServer` (fake structs implementing a narrow interface, no
  real Incus). Given a scripted sequence of initial-read responses and lifecycle events, assert
  the resulting cache state. This is the seam a reviewer should check first: it's where B1/B2's
  correctness lives, and it's the one place event semantics (which actions trigger which mutation)
  are dense enough to need real test coverage rather than an integration smoke test.
- **The new endpoint (`internal/authapp`).** `httptest`-based, following the existing pattern used
  for `/api/tenants` and its siblings (`internal/authapp/*_test.go`) — inject a fake cache (an
  interface, not the real event-fed one) that can report ready/not-ready/scoped-data on demand,
  and assert: filter semantics match `sc ls`'s existing filter behavior exactly, tenant-access
  scoping is enforced the same way `/api/tenants` enforces it, and the not-available signal is
  distinguishable from an authoritative empty result.
- **Toggle wiring.** A narrow, mechanical unit test at the same level `admin_root.go`'s existing
  env-var-to-struct-field wiring would be tested at, if it were tested today — confirm the env var
  reaches the cache subsystem and the endpoint handler with the expected on/off value, read once
  at startup.
- **`sc ls` routing (the most important seam for correctness).** A property that same underlying
  resource state produces identical `sc ls` output whether the cache or live path answers —
  testable with a fake cache-endpoint client (interface over the new endpoint's Go client method)
  that can be told to answer normally, answer "not available," error, or hang/timeout, checked
  against `listMachines()`'s existing live-path test fixtures
  (`internal/cli/list_test.go`) so both paths are asserted against the *same* expected output
  rather than independently hand-maintained expectations that could quietly drift apart.
- **End-to-end (`docs/e2e-sc2.md`).** The doc already has an `sc ls` phase with concrete PASS
  lines (e.g. `sc ls -a` at line 516, the glob/sweep matrix around lines 1111-1182). Extend it with:
  a cache-hit phase (toggle on, verify `VERBOSE=1` shows the cache path, verify latency
  qualitatively), a fallback phase (toggle off or cache forced not-ready, verify identical output
  to the pre-wish baseline), and a lifecycle-reflected-without-refresh phase (mutate via raw
  `incus`, verify `sc ls` picks it up). This is the natural place to assert the two-VM-protocol
  claim that a real deployment behaves as specified, not just unit-level fakes.

## Documentation obligations (per root `CLAUDE.md`, in the same commits as the code)

- `docs/usage.html` — new `sc ls` flags/columns for the four newly-cached resource types.
- `docs/e2e-sc2.md` — extended per the test-seams section above; this file is the executable
  source of truth for end-to-end behavior and must not trail the code.
- `implementation-notes.md` — any decision made during implementation that wasn't already pinned
  down here (e.g. the exact wire format for the "cache not available" signal, exact new flag
  names, the exact set of lifecycle actions subscribed to per resource type).
- A new ADR under `docs/adr/` (next available number: **0023**) recording the cache/event-bus
  design — its scope (an in-process cache inside an existing service, not a new one), its
  event-only staleness-acceptance decision, and its relationship to ADR-0018 (the closest existing
  precedent, event + ticker) and ADR-0021 (remote↔install cardinality, status: proposed — the ADR
  should not overstate what ADR-0021 itself settled).
