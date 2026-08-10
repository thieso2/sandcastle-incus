# Shape: admin-server config-toggled event-bus cache for `sc ls`

## What this is

`sc ls` (and its admin twin `sc-adm list` / `sc admin list`) resolve every
invocation by calling live Incus API: `GetProjects` once, then one
`GetInstancesFull` per project, sequentially. Both commands share the same
`listMachines()` in `internal/cli/list.go` — `sc-adm list` is
`newAdminMachineListCommand` in `internal/cli/admin_machine.go` calling the
identical function with admin-scoped config. With enough projects this is the
8s+ trace in the wish description.

The fix: the **Auth App** (`sc-adm auth-app serve`) grows an in-memory cache of
Incus resources, seeded by a full read on startup and kept current by
consuming the Incus event bus, and exposes it over a new authenticated HTTP
endpoint. `sc ls` calls that endpoint instead of querying Incus directly, when
a server-side toggle says the cache is in play — and falls back to today's
live path whenever the cache isn't a safe answer (not ready yet, or the toggle
is off).

## What this is not

- Not a new standalone service. `internal/cli/admin_dns_events.go` already
  subscribes to `GetEventsAllProjects()` from inside `auth-app serve` for
  DNS reconciliation (ADR-0018), and `internal/authapp` already exposes a
  family of bearer-authenticated JSON endpoints (`/api/tenants`,
  `/api/projects`, `/api/routes`, …) that the CLI already calls through
  `internal/authapp/client.go` + `commandAuthHostname()`, using the
  `AuthToken` already persisted in `~/.config/sandcastle/config.yml` from
  device login. "The admin server" is the Auth App; this wish extends it, it
  doesn't stand up a new one. (The old `routebroker` — a separate standalone
  service with its own config toggle — was removed for exactly the
  duplicated-plumbing reasons that make extending Auth App the obvious
  choice here.)
- Not a rethink of `sc ls`'s addressing, globbing, or multi-remote sweep
  syntax (`sc ls -a 'g*:d*'`, `obelix:gbrain:d*`, etc.) — every existing
  remote in a sweep keeps deciding cache-vs-live independently, the same way
  `listMachinesAcrossRemotes` already treats each remote independently today
  (report a broken one as a warning, still show the rest). One Incus remote
  = one install = one Auth App = one cache (ADR-0021), so this composes
  without new sweep logic.
- Not a new authorization model. The new endpoint scopes results the same
  way `tenantsAPI`/`projectsAPI` already do: `requireBearerUser` +
  `accessibleTenantSummaries(user)`. A cache-backed answer must never show a
  caller anything their bearer token/tenant access wouldn't already show
  them live.

## Settled without escalation

**Cache readiness is a single gate, not per-resource-type.** The wish
consistently talks about "the cache" as one thing ("cache not yet ready,"
"event bus connection has dropped"); nothing in the acceptance criteria asks
for partial availability (e.g. instances ready but images still loading).
Simplest correct thing: one readiness flag per Auth App instance, covering
all five resource types, gating the whole cache-backed endpoint.

## Decisions taken

**The toggle defaults to on (opt-out).** Once a deployment upgrades to a
binary that has the cache, `sc ls` routes through the cache-backed endpoint
by default; an operator who wants the old behavior sets the env var to
disable it explicitly (same `SANDCASTLE_*`-env-var-read-at-`auth-app serve`-
startup mechanism as `SANDCASTLE_ROUTE_INGRESS` / `SANDCASTLE_AUTH_INGRESS_MODE`
in `internal/cli/admin_root.go` — no new config mechanism needed). Because
fallback to live queries is mandatory whenever the cache isn't ready or the
event stream has gone stale (acceptance criterion 4/6), "on by default" only
changes behavior once the cache has actually proven itself ready — it does
not weaken the safety net.

**No periodic full-resync safety net — the cache is kept current from the
Incus event bus alone.** `admin_dns_events.go`'s DNS reconciler leans on a
periodic ticker precisely because it deliberately *excludes* several
lifecycle actions from its trigger set to cut noise (see its comment: keying
off every `instance-*` action degenerated into near-continuous reconciling
from routine `instance-file-retrieved`/`instance-exec` events). This wish's
cache does not have that luxury of exclusion — `sc ls` output depends on
`instance-updated` (IP/NIC changes, state changes) and the equivalent
create/update/delete/rename lifecycle actions for networks, storage,
profiles, and images, so the implementer must subscribe to the full set
relevant to each resource type, not the DNS reconciler's trimmed one.
Given event-only was the human's call, the corresponding risk — an event the
Incus event bus doesn't emit, or a listener that misses one during a
reconnect window, leaves the cache silently stale until the next event for
that resource arrives — is accepted, not mitigated by a ticker. If gaps show
up in practice, "add a periodic resync" is the known, already-proven-in-this-
codebase fallback (ADR-0018's pattern) for a later wish, not blocked by
anything decided here.

**Only `sc ls` moves onto the cache; `sc-adm list` / `sc admin list` keeps
its live per-project path.** `sc-adm list`
(`internal/cli/admin_machine.go:newAdminMachineListCommand`) calls the exact
same `listMachines()` and has the identical per-project-sequential-polling
slowness — it's reached with admin-scoped config (global admin Incus certs,
across any tenant) rather than a bearer token, so wiring it in later is a
distinct, additive change to the admin-auth path, not a blocked or
foreclosed one. This wish does not touch it. Worth remembering: admins
listing a busy install still see the original 8s+ trace after this wish
ships — that's expected, not a bug, until a follow-up wish extends the same
cache/endpoint to the admin-auth path.

## What a good outcome looks like

- `auth-app serve` startup performs one full read across instances,
  networks, storage, profiles, and images (all projects) and marks the cache
  ready; from then on it's kept current from Incus lifecycle events alone
  (no periodic resync — see "Decisions taken"), not per-project polling.
- A new bearer-authenticated endpoint on Auth App answers with the same
  shape of data `listMachines()` produces today (plus the newly-cached
  resource types), applying every filter `sc ls` already supports
  (project/machine name, wildcards, `-a`/`--all-projects`,
  `-u`/`--include-unmanaged`) against the cache instead of Incus.
- `sc ls` tries the cache endpoint first; any non-answer (toggle off, cache
  not ready, event stream stale/dropped, endpoint unreachable, no stored
  `AuthToken`) falls through to exactly today's live per-project path with no
  user-visible error — a difference only shows up under `VERBOSE=1`, matching
  the existing `[verbose] incus api: …` trace style.
- `sc ls` gains flags/output for networks, storage, profiles, and images
  (exact flag names/columns are an implementation call — follow the existing
  table-based UX in `internal/cli/list.go`), but only once the toggle is on;
  with it off, `sc ls` output is byte-for-byte what it is today.
- `docs/usage.html`, `docs/e2e-sc2.md`, and `implementation-notes.md` are
  updated in the same commits as the CLI/flag and admin-server behavior
  changes (per root `CLAUDE.md`), and a new ADR under `docs/adr/` records the
  cache/event-bus design given its scope (this is exactly the kind of
  larger, harder-to-reverse architectural decision ADRs are for).
