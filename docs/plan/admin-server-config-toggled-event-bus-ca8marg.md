# Plan: admin-server config-toggled event-bus cache for `sc ls`

Source shape: `docs/shape/admin-server-config-toggled-event-bus-ca8marg.md`.
This plan splits that shape into 4 slices, each landable independently and in
strict dependency order (cache engine → endpoint → CLI wiring → verification/
docs), matching the vertical nature of the feature: there's no parallelism to
exploit here — each layer is unusable without the one below it actually
existing.

## t1 — Auth App: Incus resource cache engine (event-bus fed)

No dependencies. Build the in-memory cache inside `internal/authapp`, with
no HTTP surface yet — deliverable is a tested internal package.

- On `auth-app serve` startup, perform one full read across all Incus
  projects for all five resource types (instances, networks, storage,
  profiles, images) and build an in-memory index. Reuse/extend the existing
  Incus API call patterns already used for instances (see `incusx` and
  `listMachines()`'s per-project `GetInstancesFull` in
  `internal/cli/list.go`) — add the analogous full reads for networks,
  storage, profiles, and images.
- After the initial read, subscribe to the Incus event bus the same way
  `internal/cli/admin_dns_events.go` already does via `GetEventsAllProjects()`
  — but do **not** copy its trimmed action set. The DNS reconciler
  deliberately excludes noisy actions (see its comment about
  `instance-file-retrieved`/`instance-exec`); this cache cannot afford that
  exclusion because `sc ls` output depends on `instance-updated` (IP/state
  changes) and the equivalent create/update/delete/rename lifecycle events
  for all five resource types. Subscribe broadly enough to keep the index
  correct for every resource type.
- Maintain a single readiness flag per Auth App instance (not per resource
  type — see "Settled without escalation" in the shape doc): not ready until
  the initial read completes *and* the event stream is connected; flips back
  to not-ready if the event stream disconnects or goes stale. There is no
  built-in liveness ping on the Incus event bus, so define and document a
  staleness/heartbeat detection approach (e.g. a last-event-received
  timestamp checked against a timeout) — record this design choice in
  `implementation-notes.md`.
- Add a new admin-server config toggle read at `auth-app serve` startup:
  an env var following the exact existing pattern of
  `SANDCASTLE_ROUTE_INGRESS` / `SANDCASTLE_AUTH_INGRESS_MODE` in
  `internal/cli/admin_root.go` (`os.Getenv`, threaded into the server's
  config struct). Per the shape doc's decision, **default the toggle to
  on** — the cache engine starts by default; an operator sets the env var
  to explicitly disable it. When disabled, skip the initial read and event
  subscription entirely (no wasted work).
- No periodic full-resync ticker — this is a deliberate decision from the
  shape doc (event-only, staleness risk accepted). Do not add one.
- Tests: unit-test the initial-read index construction, unit-test that
  synthetic event sequences (create/update/delete/rename, across all five
  resource types) correctly mutate the cache, and unit-test the readiness
  flag's transitions (not-ready → ready after initial read + stream connect;
  ready → not-ready on simulated disconnect/staleness).
- Update `implementation-notes.md` with the staleness-detection mechanism
  and the "default on" toggle decision.

## t2 — Auth App: cache-backed listing endpoint

Depends on: t1.

- Add a new bearer-authenticated HTTP endpoint on the Auth App, following
  the existing pattern used by `/api/tenants`, `/api/projects`, etc.
  (`requireBearerUser` + `accessibleTenantSummaries(user)` — see
  `internal/authapp/tenants.go` and `routes_api.go`). Register it in
  `internal/authapp/routes.go` alongside the existing `/api/*` family.
- The endpoint answers with the same data shape `listMachines()` in
  `internal/cli/list.go` produces today, plus the newly cached resource
  types (networks, storage, profiles, images).
- It must apply every filter/wildcard `sc ls` already supports **server
  side**, against the cache: project/machine name, wildcards,
  `-a`/`--all-projects`, `-u`/`--include-unmanaged` (read
  `internal/cli/list.go` for the exact current semantics before designing
  the request contract, so the client in t3 doesn't need to duplicate
  filtering logic against a different data shape).
- Respect the t1 toggle and readiness flag: if the toggle is off or the
  cache isn't ready, respond with an explicit non-200 status (e.g. 503) —
  never partial or stale data — so the CLI can key its fallback decision
  off the response, not off guessing.
- Authorization must never show a caller more than their bearer
  token/tenant access already shows them live — same scoping as
  `tenantsAPI`/`projectsAPI`.
- Tests: hit the endpoint directly (unit/integration, using existing
  `internal/authapp` test harness conventions) covering: toggle-on +
  ready → correctly filtered results; toggle-off → non-answer status;
  not-ready → non-answer status; tenant-scoping enforcement.
- Update `implementation-notes.md` with the response/error contract chosen
  for the "non-answer" cases.

## t3 — `sc ls`: cache-first with live fallback, plus new resource-type flags/output

Depends on: t2.

- Only `sc ls` moves onto the cache — `sc-adm list` / `sc admin list`
  (`internal/cli/admin_machine.go:newAdminMachineListCommand`) keeps its
  live per-project path unchanged; this is an explicit, settled scope
  boundary from the shape doc, not an oversight.
- Wire the `sc ls` path of `listMachines()` in `internal/cli/list.go` to
  call the t2 endpoint first, via `internal/authapp/client.go`, reusing the
  persisted `AuthToken` (`~/.config/sandcastle/config.yml`) and
  `commandAuthHostname()` the same way other `/api/*` calls already do.
- On **any** non-answer — no stored `AuthToken`, endpoint unreachable,
  non-200/not-ready response, or request timeout — fall through
  transparently to exactly today's live per-project Incus query path, with
  no user-visible error. Log the fallback reason only under `VERBOSE=1`,
  matching the existing `[verbose] incus api: …` trace style.
- Extend `sc ls`'s flags and table output to cover networks, storage,
  profiles, and images (flag names/columns are an implementation call —
  follow the existing table-based UX already in `list.go` for instances).
  These new resource types are only ever populated via the cache path
  (the live path never fetched them) — when falling back to live, `sc ls`
  behaves exactly as it does today (instances only).
- With the toggle off, `sc ls` output must be byte-for-byte identical to
  pre-wish behavior — this is an explicit acceptance criterion.
- Tests in `internal/cli` covering: cache-hit rendering of new resource
  types; each fallback trigger individually (no token / unreachable /
  not-ready / toggle-off) preserving today's output; byte-for-byte
  toggle-off compatibility.
- Update `docs/usage.html` with the new flags and `docs/e2e-sc2.md` with
  the toggle-on/toggle-off/fallback behavior in the relevant listing phase.

## t4 — ADR + end-to-end verification across toggle/readiness states

Depends on: t3.

- Write a new ADR under `docs/adr/` (check the highest existing number —
  currently `0022` — and follow the existing format) documenting: why the
  cache lives in Auth App rather than a new service; the single
  all-resource-types readiness gate; the event-bus-only strategy (no
  periodic resync) and the accepted staleness risk; the opt-out-by-default
  toggle; and that `sc-adm list`/admin-auth path is explicitly out of scope
  for this wish.
- Do a holistic verification pass exercising the full feature end to end
  (via whatever integration/e2e harness fits — `internal/integration`,
  `internal/e2e`, or targeted `internal/authapp` + `internal/cli` tests if
  a live Incus isn't available) covering all four combinations from the
  wish's acceptance criteria:
  1. Toggle on + cache ready → `sc ls -a` and other flag/filter combos
     answer from the cache (verify it's not hitting live Incus calls).
  2. Toggle on + cache not-ready-yet (immediately after `auth-app serve`
     startup) → falls back to live.
  3. Toggle on + event bus connection dropped/stale → falls back to live.
  4. Toggle off → behavior/output identical to pre-wish `sc ls`.
- Fix any gaps the verification pass finds.
- Reconcile `docs/usage.html`, `docs/e2e-sc2.md`, and
  `implementation-notes.md` against what actually got built in t1-t3 —
  slices worked as separate sessions may have drifted on exact flag names,
  env var names, or endpoint contracts; make the docs match the shipped
  code.
