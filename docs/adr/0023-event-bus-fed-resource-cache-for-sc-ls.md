# Event-Bus-Fed Resource Cache in the Auth App, Behind an Opt-Out Toggle, for `sc ls`

> Status: **accepted (implemented).** Builds on ADR-0018 (per-tenant DNS reconcile via the same event bus) and ADR-0021 (one Incus remote, therefore one Auth App, per install). Captured 2026-08-10.

## Context

`sc ls` answers every invocation with a live Incus round trip: `GetProjects`
once, then one `GetInstancesFull` per project, sequentially. On a
several-project tenant this is seconds of wall clock even though almost all
of it is Incus, not Sandcastle, thinking — a representative `VERBOSE=1` trace
showed `sc ls -a` taking over 8s, dominated by per-project
`GetInstancesFull` calls each running several seconds on a loaded host (see
`docs/shape/admin-server-config-toggled-event-bus-ca8marg.md`).

The fix built here: the Auth App (`sc-adm auth-app serve`) grows an
in-memory index of Incus resources — instances, networks, storage volumes and
pools, profiles, and images, across every project — seeded by one full read
on startup and kept current by consuming the Incus event bus instead of
re-polling. `sc ls` calls a new bearer-authenticated endpoint on the Auth App
first and only falls through to today's live per-project path when the cache
is not a safe source of truth.

## Decisions

1. **The cache lives inside the existing Auth App, not a new service.**
   `internal/cli/admin_dns_events.go` already runs a long-lived event-bus
   subscription (`GetEventsAllProjects()`) from inside `auth-app serve` for
   DNS/route reconciliation (ADR-0018), and `internal/authapp` already
   exposes a family of bearer-authenticated JSON endpoints (`/api/tenants`,
   `/api/projects`, `/api/routes`, …) that the CLI already calls through
   `internal/authapp/client.go` + `commandAuthHostname()`, using the
   `AuthToken` already persisted from device login. Standing up a second
   service — its own process, its own port, its own auth — to hold a second
   Incus-derived cache would duplicate all of that plumbing for no
   independent benefit. This is exactly the shape of mistake the old
   `routebroker` (a separate standalone service with its own config toggle,
   removed for the duplicated-plumbing it caused) taught the codebase to
   avoid. One Incus remote is one install is one Auth App (ADR-0021); the
   cache is a property of that one instance, not a new topology node.

2. **Readiness is a single flag covering all five resource types, not a gate
   per resource type.** The wish talks about "the cache" as one thing
   throughout — "cache not yet ready," "event bus connection has dropped" —
   and nothing in the acceptance criteria asks for partial availability
   (e.g. instances ready but images still loading). `ResourceCache.Ready()`
   is `initialReadDone && streamConnected && !stale`, computed once and
   shared by every resource type. The simpler alternative (five independent
   readiness flags) would have forced `sc ls` and the endpoint to reason
   about partial cache answers — mixing a cache-backed instance list with a
   live-queried image list in a single response — which nothing asked for
   and which would have made the fallback contract far harder to reason
   about for no observed benefit.

3. **The cache is kept current from the Incus event bus alone — no periodic
   full resync.** `admin_dns_events.go`'s DNS reconciler leans on a periodic
   ticker specifically *because* it deliberately trims its trigger set (it
   excludes `instance-updated` and several other actions to avoid a
   feedback loop from its own file reads — see
   `implementation-notes.md`, 2026-07-31 entry). This cache does not have
   that luxury: `sc ls` output depends on `instance-updated` (IP/NIC/state
   changes) and the equivalent create/update/delete/rename/refresh actions
   for networks, storage volumes, storage pools, profiles, and images (see
   `resourceCacheActions` in `internal/authapp/resource_cache_server.go`),
   so it subscribes broadly rather than trimming for noise. Given that,
   adding a ticker on top would mean paying a recurring full-fleet sweep —
   the exact cost this wish exists to remove — "just in case" an event was
   missed. **Accepted risk:** an event the Incus event bus does not emit,
   or a listener that misses one during a reconnect window, leaves the
   cache silently stale for that one resource until the next relevant event
   arrives for it. Mitigated only by the readiness/staleness gate (decision
   4) making a *disconnected* stream visible and by the always-available
   live-query fallback — not by detecting or repairing a *missed-but-still-
   connected* event. If this proves insufficient in practice, adding a
   periodic resync is the known, already-proven-in-this-codebase pattern
   (ADR-0018's ticker) for a follow-up wish; nothing here forecloses it.

   **Amended 2026-08-21 — the risk landed, and is now mitigated at the point
   of use.** "`sc ls` output depends on `instance-updated` (IP/NIC…)" is not
   true of addresses: upstream emits `api.EventLifecycleInstanceUpdated` only
   from the tail of `(*lxc).Update()` / `(*qemu).Update()`, i.e. an instance
   *config* change made through the API. A guest picking up its DHCP lease is
   not an API operation and emits nothing, so the empty address read at
   `instance-started` survives every subsequent `sc ls` until an unrelated
   event touches the same project — observed live on obelix (a machine
   reachable over ssh listed with a blank IP column). Rather than add the
   ticker, `sc ls` now treats *a running machine with no address* as a cache
   miss and falls back to the live per-project path
   (`runningWithoutAddress` in `internal/cli/list.go`; see
   `implementation-notes.md`, 2026-08-21). The no-resync decision stands; what
   changes is that this particular undetectable-staleness case is detectable
   in the answer itself, so the fallback contract of decision 2 covers it.

4. **Staleness is detected by a last-event-received heartbeat, not a
   protocol-level liveness ping** (the Incus event bus has none). Any event
   received on the listener — not just ones the cache acts on — resets
   `lastEventAt`; the cache reports not-ready once `now() -
   lastEventAt` exceeds `DefaultResourceCacheStaleAfter` (2 minutes), and
   immediately not-ready on an explicit socket disconnect (no grace
   period, since every event since a drop is unaccounted for). A
   reconnect resets the clock fully rather than inheriting the pre-drop
   timestamp, so a fresh connection gets a full timeout window instead of
   reading as instantly stale.

5. **The toggle defaults to on (opt-out), read once at `auth-app serve`
   startup.** `SANDCASTLE_RESOURCE_CACHE` follows the exact
   `os.Getenv`-read-at-startup pattern already used for
   `SANDCASTLE_ROUTE_INGRESS`/`SANDCASTLE_AUTH_INGRESS_MODE`
   (`internal/cli/admin_root.go`) — no new config mechanism. Once a
   deployment runs a binary that has the cache, `sc ls` routes through it by
   default; an operator who wants the pre-wish behavior sets the env var to
   one of `off`/`disable`/`disabled`/`0`/`false` explicitly. This is safe
   specifically *because* decisions 2–4 make the fallback mandatory whenever
   the cache is not ready — "on by default" only changes behavior once the
   cache has already proven itself current, never as a way to skip the
   safety net.

6. **`sc-adm list` / `sc admin list` (the admin-auth listing path) is
   explicitly out of scope for this wish.** `newAdminMachineListCommand`
   (`internal/cli/admin_machine.go`) calls the same `listMachines()` and has
   the identical per-project-sequential-polling cost as pre-wish `sc ls`,
   but it is reached with admin-scoped config (global admin Incus certs,
   across any tenant) rather than a caller's bearer token — wiring it onto
   `GET /api/resources` would need its own authorization design (the
   endpoint's scoping is deliberately `accessibleTenantSummaries`, the
   narrower per-caller-tenant check `tenantsAPI` uses, not an
   admin-wide view) rather than being a drop-in reuse. This wish does not
   touch it: admins listing a busy install still pay the original
   several-second trace after this ships. That is expected, not a bug,
   until a follow-up wish extends the cache/endpoint to the admin-auth
   path.

## Consequences

- `auth-app serve` performs one full, all-resource-type read across every
  project on startup (`seedResourceCache`) and, from then on, only re-reads
  the one `(kind, project)` bucket a lifecycle event names — never the
  event's own resource name, since interpreting rename-event payloads
  reliably across Incus versions is not well-enough documented to trust (see
  `refreshResourceCacheProject`'s doc comment).
- `GET /api/resources` (`internal/authapp/resource_cache_api.go`) answers
  200 with a fully-filtered, tenant-scoped result or 503 — toggle-off and
  not-ready collapse to the identical 503, deliberately, since `sc ls` never
  needs to tell them apart (see `implementation-notes.md`, t2 entry, for the
  full status-code contract, including the request-validation codes — 400,
  401, 403, 404 — that are genuine errors `sc ls` must **not** treat as a
  fallback trigger).
- `sc ls` (`internal/cli/list.go`) tries the cache first with a short (5s,
  see the 2026-08-12 amendment) request-scoped timeout and falls through to
  unmodified `listMachines()` on
  any non-answer, with no user-visible difference — a fallback shows up only
  under `VERBOSE=1`. With the toggle off, `sc ls`'s default output is
  byte-for-byte what it was before this wish; the new
  `--networks`/`--storage-pools`/`--storage-volumes`/`--profiles`/`--images`
  flags exist but render nothing on a live-path answer, since the live path
  never populated those fields.
- **Tradeoff (accepted):** an install with zero Incus activity for over 2
  minutes reads as not-ready and falls back to the live path even though
  nothing is actually wrong — judged acceptable because the fallback is
  exactly today's (safe, correct) behavior, not an error state.
- **Amendment (2026-08-11).** Decision 2's single readiness flag stands, but
  the *seed* is no longer all-or-nothing: storage volumes are read
  best-effort. Every other resource type is a plain database read, while
  listing volumes reaches into the storage driver per volume — so one broken
  instance on the host (an Incus record whose backing dataset is gone) fails
  the pool listing for every project and, under the original fatal seed,
  disabled the whole cache permanently: re-seed every 5s, identical failure,
  `sc ls` on the live path forever. Observed on the `obelix` install. A pool
  that cannot be enumerated is now logged and skipped, so the instance
  listing — the only thing default `sc ls` renders — survives it. Refusing to
  serve never produced the unreadable volumes either; it only took everything
  else down with them.

- **Amendment (2026-08-12).** The request now names the resource kinds it
  will render (`GET /api/resources?include=machines[,networks,…]`), and the
  budget is 5s rather than 3s. Both come from the same field measurement on
  `obelix`: a `sc ls -a` that renders machines only was being answered with
  79 KB, 62 KB of it storage-pool `used_by` and profile `config`/`devices`
  that no `sc ls` section prints, and over a Cloudflare-tunnelled Auth
  Hostname the round trip missed the 3s budget often enough that a *ready*
  cache still fell back. A missing `include` still means "everything", so an
  older `sc ls` is unaffected, and an unknown kind is ignored rather than
  rejected so a newer one is too. The budget move is a separate admission:
  even a minimal body costs a 0.7–2.2s cold TLS handshake through the tunnel
  before Sandcastle does anything, and the live path it falls back to was
  measured at 49s on that install — waiting a little longer for the cache is
  strictly cheaper than losing it. `SANDCASTLE_LS_CACHE_TIMEOUT` overrides
  the budget for slower links.

- **Tradeoff (accepted):** `sc-adm list`/`sc admin list` keeps its full
  original cost. A busy install's admin-auth listing is not made faster by
  this wish.
