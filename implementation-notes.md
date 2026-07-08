# Implementation Notes

Running log of decisions that weren't in the spec — choices invented on the
spot, deviations from what was asked, tradeoffs, and workarounds for
environment/tooling limits. The "why" behind the code; larger hard-to-reverse
decisions live in `docs/adr/`. Newest first.

## 2026-07-08 — majestix e2e run: three live-caught fixes

All three surfaced running the full `docs/e2e-sc2.md` protocol from scratch on
a fresh VM (`majestix`, two installs `sc`+`id` on one Incus 7.2 daemon, nested
client VM). Each was fixed, regression-tested, redeployed, and re-verified live
in the same run.

- **Auth DB `SQLITE_BUSY` on first login.** The new svclog sink writes a row
  per request into the same SQLite DB the device poll writes users to;
  `OpenDatabase` set pragmas via a one-off `Exec` (one pooled connection) and
  left the default rollback journal with no busy timeout, so the very first
  `sc login` died with `database is locked (5)`. Fix: pragmas in the DSN
  (`busy_timeout(10000)`, `journal_mode(WAL)`, `foreign_keys(1)`,
  `synchronous(NORMAL)`) so every pooled connection gets them. Alternative
  considered: `SetMaxOpenConns(1)` — rejected, it serializes reads too.
- **`sc dns-proxy`: the resolved link scope now points at an on-link forwarder,
  not at the tenant CoreDNS.** systemd-resolved binds a link scope's UDP
  sockets to the scope's interface; our scope lives on a dummy link, so UDP to
  the off-link CoreDNS was transmitted into the dummy and dropped (tcpdump:
  zero packets on any real interface). resolved silently degraded the server
  to TCP and then re-probed UDP after each ~5-min idle grace period — failing
  exactly one `getent` per idle period, forever. Alternatives considered:
  (a) a primer query in the unit — shipped first, but only fixes the first
  cycle, not the idle re-probe; (b) a keepalive timer — shrinks but keeps the
  window, masks the defect; (c) attaching the scope to `tailscale0` —
  tailscaled owns that link's resolved settings and clobbers them; (d) socat —
  new client dependency. Chosen: a ~100-line UDP+TCP forwarder inside the fat
  binary (`sc dns-proxy`, hidden), run by the per-suffix unit as a daemon
  (`Type=exec`, `Restart=on-failure`, `PartOf=systemd-resolved.service`): it
  owns the dummy link, listens on the link's own 169.254 address (bound-to-link
  delivery of an on-link address is local), pins the scope there, forwards to
  the CoreDNS over normal routing. UDP+EDNS0 works natively; the degradation
  ladder is gone; the resolver-install step dropped from ~21s (probe cost) to
  ~0.4s. The unit embeds `os.Executable()` at render time — moving the binary
  needs a re-login (documented tradeoff).
- **Install scoping for URL-named remotes.** The naming-url-install-identity
  merge renamed enrolled remotes to `sc-<install-label>` (from the Auth
  Hostname), but `installPrefixFromRemoteName` still only inverted the legacy
  `sc-<prefix>-<tenant>` shape — every lookup under a URL-named remote ran
  unscoped and the cross-install shadowing returned (`sc list` under install A
  showed install B's machines). Fix: derive the prefix from the remote's
  pinned project in the shared incus config (`remotes[<remote>].project` =
  `<prefix>-<tenant>[-<app>]`), which login writes for every enrollment; the
  legacy name-shape inversion stays as fallback. Considered extending the
  `installs:` map in `config.yml` to carry the prefix — rejected: the pin
  already exists for every enrollment (old and new) and needs no schema change.

## 2026-07-08 — majestix e2e round 2: exec exit codes were silently swallowed everywhere

The single most consequential find of the run: the incus SDK's `op.Wait()` on
an exec operation succeeds as long as the *operation* ran — a command that
exited nonzero is only visible in `Metadata["return"]`, which none of the
sidecar exec helpers checked. Consequences caught live: `sc-adm tenant create`
returned success in ~5s on a cached-image host while the whole package install
had failed against a still-booting container (no CoreDNS, no Tailscale); the
DNS reconciler's post-write CoreDNS reload failed invisibly and the live-file
compare then skipped it forever (zone file right, served zone stale ~1min).
Fixes: `execExitError` applied to `execSidecar`/`execSidecarCapture`, the v1
`restartCoreDNS`, and the reconciler's reload (now with stderr capture);
`waitV2SidecarBoot` (systemd settled + tenant IPv4 on eth0) between sidecar
launch and provisioning execs. Deliberately NOT swept every other exec site in
one go (machine connect/lifecycle/ssh-key paths) — those have their own
error-observation semantics and deserve a separate pass; noted here so the
sweep isn't forgotten. Also repaired `scripts/e2e-v2.sh` (pipefail early-exit,
nss-myhostname false positives via `getent ahostsv4`, cleanup that now purges
via the product path incl. shared volumes). `e2e-v2.sh` runs GREEN on majestix.

## 2026-07-08 — verbose service logging + per-user log browser

- Added a shared `internal/svclog` package (the repo had no logging layer at
  all — only two `log.Printf` calls in the auth-app). It emits one verbose,
  timestamped line per HTTP request plus named work spans, each with a duration,
  to stderr (journald under systemd), and optionally to a `Sink`.
- **Identity attribution via request-scoped context, not middleware guessing.**
  The HTTP middleware knows method/path/status/duration but not *who* — that is
  resolved inside handlers (session cookie, CLI bearer token, cert fingerprint).
  Rather than re-resolve identity in middleware (an extra DB hit, and impossible
  for the machine-called workload-token path), the middleware installs a mutable
  record in the context and handlers enrich it with `svclog.SetUser`. I
  instrumented the existing identity choke points **once** each
  (`requireAllowlistedSession`, `requireAdmin`, `requireBearerUser`,
  `requireTenantAccess`, plus the broker principal resolvers) so every route is
  attributed for free. Workload-token issuance is attributed to the machine's
  owning `user_key` from the runtime-secret row, so it shows in that user's log.
- **Async DB sink.** SQLite is a single writer; writing a log row synchronously
  on every request would serialize request handling. The auth-app's `dbSink`
  hands entries to a background goroutine over a buffered channel and **drops on
  overflow** (best effort) — the verbose stderr line is never dropped, only the
  persisted copy. `Close()` flushes the buffer at shutdown. Each drained write
  uses a detached 5s-bounded context so a wedged DB can't block the drain.
- **Scope decision (asked the user):** the browse UI covers auth-app activity
  only. The brokers get the same verbose stderr logger but no DB sink — they are
  a separate `sandcastle-broker` process on a separate appliance and don't share
  the auth DB. Shipping broker logs into the UI was deferred; it would need an
  authenticated internal endpoint.
- **Retention: keep forever (user's choice).** No pruning job. The `logs` table
  grows unbounded; flagged for the future. Indexes on `(user_key, ts)` and `ts`
  keep the browse queries fast regardless.
- `/logs` page: reuses the session guard `requireAllowlistedSession`, branches on
  `user.SandcastleAdmin` (admin → `ListAllLogs`, else `ListLogsForUser`). System
  rows (empty `user_key`, e.g. the DNS-reconcile error) are visible to admins
  only. Styling copied from `machinesTemplate` (mobile-first inline CSS).
- Verified with `go test ./internal/svclog ./internal/authapp` — unit tests for
  middleware status/duration + span timing, the security-critical scoping
  (`ListLogsForUser` vs `ListAllLogs`, and search can't escape a user's scope),
  the `/logs` page rendered per viewer, and the full middleware→dbSink→DB→query
  pipeline.

## 2026-07-07 — `sc connect --vm`: auto-create as a virtual machine

- `sc c <name>` (v2) already auto-creates a missing machine, but always as a
  container. Added `--vm` to connect so `sc c --vm tubu` creates the machine as
  a VM when it doesn't exist (pass-through to `EnsureMachineV2`; no effect on
  an existing machine). VMs get a 240s SSH-wait window instead of 120s —
  firmware + kernel boot precede cloud-init. Validated live: created a VM in
  `tc3-thieso2-default`, SSH'd in, hostname `tubu.default.tc4` (login-chosen
  DNS suffix intact).

## 2026-07-07 — incus current remote is the single source of truth for `sc`'s remote

- Two knobs selected the active install and could disagree: the shared incus
  dir's `default-remote` (moved by `incus remote switch`, set by login's
  enrollment) and `remote:` in `~/.config/sandcastle/config.yml` (written by
  login, read by `sc`). A manual `sc incus remote switch` moved only the first,
  so `sc` kept operating on the previous install — exactly the confusion the
  operator hit with tc2/tc3 on one daemon.
- Now `LoadUser` prefers the shared incus dir's `default-remote` whenever it
  names a Sandcastle enrollment (`sc-…` and listed in that config); precedence
  is `SANDCASTLE_REMOTE` env → incus current remote → config.yml `remote` →
  default. Non-sandcastle current remotes (`local`, `images`, …) are ignored so
  raw-incus work doesn't hijack `sc`. `sc config set remote X` writes through
  to the incus `default-remote` (refusing names that aren't enrolled), so the
  two knobs can no longer diverge; config.yml's `remote` stays as a fallback
  and for back-compat. Admin commands (`LoadAdmin`) are unchanged — their
  remote points at an Incus host, not an enrollment.

## 2026-07-07 — tenant lookup scoped to the current remote's install (same-daemon multi-install)

- With two installs on one Incus daemon (tc2 + tc3) and one user logged into
  both, `sc incus ls` connected over the tc3 remote but pinned
  `INCUS_PROJECT=tc2-thieso2-default` — every sidecar's Incus Reach lands on
  the same host API and the shared client cert sees BOTH installs' projects, so
  `v2TenantSummary`'s first-match-by-tenant-name picked whichever install
  sorted first. All summary consumers (`sc incus`, `create`, `connect`,
  machine lifecycle) inherited the wrong project.
- Fix: `v2TenantSummary` now scopes `tenant.ListForPrefix` by the install
  prefix recovered from the configured remote's name
  (`installPrefixFromRemoteName`: `sc-<prefix>-<tenant>` → prefix,
  `sc-<tenant>` → default). Chose the remote name as the source of truth
  because it is the one client-side datum that is per-install by construction
  (server-generated via `usertrust.RemoteInstallName`); unparseable remote
  names fall back to the old unscoped behavior. Note `sc incus remote switch`
  moves only the raw incus CLI's current remote — `sc`'s own install selection
  is `sc config set remote …`.

## 2026-07-07 — first-login "write /etc/resolv.conf to sidecar: Not Found" root-caused (dangling symlink, not a boot race)

- Every fresh v2 tenant provisioning failed once with `write /etc/resolv.conf
  to sidecar: Not Found` and only succeeded on the re-ensure pass. The earlier
  `waitInstanceRunning` guard assumed a boot race, but the real cause is that
  `/etc/resolv.conf` on the stock Debian sidecar image is a **symlink** to
  systemd-resolved's stub under `/run`; the Incus file API follows symlinks, and
  pushing through a dangling one returns "Not Found". It never hit the other DNS
  files (real paths), and the retry only worked because the package-install
  bootstrap had meanwhile written through the symlink, creating the target.
  The machine-create path already guarded this with `rm -f /etc/resolv.conf`
  before writing; the sidecar path didn't.
- Fix: `writeInstanceDir` (the prep step before every sidecar/appliance file
  push) now also clears a symlink at the target path (`[ ! -L p ] || rm -f p`),
  so pushes always land on a regular file. Chose the generic prep-step fix over
  a resolv.conf special case since any pushed path could be a symlink on a
  future base image.

## 2026-07-07 — interactive tailnet-join URL made durable (primary path; auth key is CI-only)

- **Context.** `sc login` without a Tailscale auth key looked like an infinite
  loop: after approval it polled "waiting to join your tailnet" for hours with
  ~70s per poll. Root causes were all in the interactive branch of
  `v2TailscaleUp` (re-run by the server on every awaiting-tailnet poll):
  (1) each pass truncated `/var/lib/sandcastle-tsup.log` and then failed
  silently to restart the already-running `sandcastle-tsup` unit, so the login
  URL was destroyed and could never be re-obtained — a Ctrl-C + re-login printed
  no URL at all; (2) `tailscale status` exits non-zero while logged out, so the
  daemon-wait loop burned its full 30s on every pass, plus another 30s grepping
  the now-empty log. Design decision confirmed with the operator: the
  **interactive URL is the primary join path**; `--tailscale-auth-key` at login
  is for unattended/CI only — so the fix makes the URL durable rather than
  pushing users toward keys.
- **Fix (sidecar script).** The pending `tailscale up` unit is now left alone
  while healthy: the script (re)starts it only when the unit is not running *or*
  its log no longer contains a `login.tailscale.com` URL (which also self-heals
  sidecars stuck by the old truncation bug). The log is truncated only when a
  fresh unit is started. Daemon readiness uses `tailscale status --json`, which
  answers as soon as tailscaled is up even when logged out. The URL grep takes
  the *newest* match (`tail -n 1`) since each `up` mints a fresh URL. Awaiting
  polls now answer in seconds.
- **Fix (client).** `sc login` printed the join instructions only on the first
  approved poll — if that poll carried no URL (or the URL changed later), the
  user never saw it. It now prints the instruction block whenever the reported
  URL is new, and prints "Waiting for the sidecar…" once. The tip line now
  frames `--tailscale-auth-key` as unattended/CI, matching the intended design.
- **Not changed (deliberate).** The server still never uses its deployment-wide
  `--tailscale-auth-key` for sidecar provisioning (it only echoes it to clients)
  and approved device logins still don't expire while awaiting the tailnet join
  — acceptable now that polls are cheap and the client's `--max-polls` bounds
  the wait (~25 min at the 5s cadence).

## 2026-07-07 — per-install infra project `<prefix>-infra` + install resource inventory

- **The auth-app appliance now lives in `<prefix>-infra`, not the generic
  `infrastructure`.** Every install put its auth-app in one shared, unprefixed
  `infrastructure` project, so on a host with several sandcastles (or an older
  `sc-infra` install) you couldn't tell which appliance belonged to which install,
  and the project name didn't group with its own `<prefix>-<tenant>` / `<prefix>-net`
  resources. The install now derives `infraProject := installV2Prefix(prefix) +
  "-infra"` (e.g. default `sc` → `sc2-infra`, `--prefix id` → `id-infra`), matching
  the appliance-bridge (`<prefix>-net`) and tenant (`<prefix>-<tenant>`) naming.
  Safe rename: the appliance project name is decoupled from runtime — provisioning
  and the DNS reconciler scope tenants by `SANDCASTLE_INCUS_PROJECT_PREFIX`, not by
  the appliance project name — so only the install wiring + the existing-install
  guard referenced the literal. `AuthAppDefaultProject` stays `"infrastructure"` as
  the fallback for the lower-level standalone `authapp deploy`.
- **Install now prints a resource inventory.** The summary ends with a
  "resources created by this install" list (infra project, auth-app instance,
  bridge, broker project/instance, cloudflare tunnel) plus a one-line teardown
  hint. Makes coexistence auditable and teardown obvious (delete the listed
  project(s) + bridge).

## 2026-07-07 — enrollment reaches the sidecar over the tailnet, not the private CIDR

Found during the first real-OAuth login from a Mac that was on the tenant tailnet
but had NOT accepted the tenant subnet route.

- **`incus remote add` now auto-answers with the sidecar's tailnet endpoint.** The
  Incus join token embeds the sidecar Incus's own https address, which lives on the
  tenant's PRIVATE CIDR (e.g. 10.253.x.x). A client that already accepted the tenant
  subnet route (like the e2e VM) can reach it; a plain tailnet client cannot, so
  `incus remote add <token>` fell through to an interactive "provide alternate
  server addresses" prompt and, non-interactively, failed with "All server
  addresses are unavailable." The auth-app already returns the sidecar's TAILNET IP
  as `IncusRemoteAddress` (used for the later `set-url`), so we now feed that
  `<ip>:8443` to the prompt on stdin — enrollment connects over the tailnet with no
  subnet route required. Falls back to the caller's stdin when no tailnet address is
  known. Without this, every first login from a Mac/laptop that isn't a subnet-route
  client would stall at the prompt.
- **TODO (not yet fixed): auth-app SQLite `SQLITE_BUSY` under concurrent device
  poll.** The first real login hit `auth app device poll: database is locked (5)
  (SQLITE_BUSY)`; a retry cleared it. The device-poll path races provisioning
  writes on the same SQLite file. Needs a busy_timeout / WAL / serialized writer.

## 2026-07-07 — client-side split-DNS for v2 + reconciler self-heal

Surfaced while chasing "why doesn't `<machine>.<project>.<suffix>` resolve on the
client" during the coexistence e2e. Four linked fixes:

- **systemd-resolved: drop-in, not `resolvectl … lo`.** The old strategy ran
  `resolvectl dns lo <ip>` / `domain lo ~<suffix>`; modern systemd-resolved (257
  on Debian 13) rejects it outright — "Link lo is loopback device." Pinning to a
  real link (`tailscale0`) works but *replaces* Tailscale's MagicDNS servers on
  that link. Chosen fix: a global `resolved.conf.d` drop-in
  (`DNS=<endpoint>` + `Domains=~<suffix>`) so the kernel routes the query to the
  tenant CoreDNS over the tailnet and every link is left alone. macOS keeps its
  `/etc/resolver/<domain>` file. Alternatives rejected: dummy interface (query
  binds to the link's egress, which can't route to the tenant subnet); per-link
  tailscale0 (clobbers MagicDNS).
- **`10-` filename prefix is load-bearing.** systemd merges `resolved.conf.d`
  into ONE flat global server list in lexical order and does NOT fall through on
  an authoritative NXDOMAIN. So the tenant CoreDNS must sort before the public
  upstream (`50-public-upstream.conf`): CoreDNS answers its own zone and REFUSEs
  everything else (fall-through covers public + other tenants), whereas a public
  server would NXDOMAIN a tenant name first and win. Verified live: with the
  tenant server last, resolution failed; first, it worked.
- **v2 login installs the resolver automatically.** The v2 login path previously
  only verified tenant routing and left client name resolution to a *manual*
  Tailscale Split DNS console entry — exactly the kind of shortcut the e2e is
  meant to avoid. It now also installs the local split-DNS drop-in, using the
  CIDR the device response already carries (a restricted client can't read its
  infra project's CIDR via the store — see `internal/tenant/list.go` — so
  `localdns.PlanForV2` takes it directly) and the suffix visible on the app
  projects. Best-effort: a failure warns and points at the Tailscale Split DNS
  fallback rather than failing the login.
- **Elevation executes the exact plan.** `runLocalDNSWithSudoFallback` used to
  re-run `sc dns <action> <tenant>` under sudo, which rebuilt the plan from the
  store (empty CIDR → `ParsePrefix("")`). It now serializes the resolved plan and
  a hidden `dns apply-elevated` runs it verbatim across the privilege boundary.
- **DNS reconciler self-heals an externally-reset sidecar.** The auth-app
  reconciler skipped writing when the rendered zone matched an in-memory
  `lastZone` cache. But a sidecar can restart and lose its zone (back to SOA+ns)
  while the auth-app keeps running — the cache then masks the loss forever
  (observed live: a machine's A-record vanished and only an auth-app restart,
  which clears the cache, brought it back). Fixed by comparing against the
  sidecar's ACTUAL zone file (serial-normalized) instead of the cache.

## 2026-07-07 — enrollment hang on a second install (trusted-client project pin)

- **Cert-based remote-add fallback now pins `--project`.** Found live during a
  full-suite e2e: enrolling a *second* install on a client that already trusts
  the shared keypair failed. The token path is refused (`Client is already
  trusted`), so we fall back to `incus remote add … --auth-type=tls
  --accept-certificate`; but the shared cert can see *both* installs' projects,
  so `incus remote add` prompted interactively (`Name of the project to use for
  this remote:`) and died on EOF in the non-interactive login — the login hung
  and never enrolled `sc-id-<tenant>`. Fix: pass `--project <install-default>`
  to that fallback so it never prompts. Extracted `trustedClientRemoteAddArgs`
  for a pure unit-test (the shell-out itself resists mocking because
  `setRemoteProject` expects the remote already in `config.yml`). Validated live:
  second install now enrolls cleanly, project pins correct, no cross-leak.

## 2026-07-07 — multi-install coexistence, shared identity, appliance bridge

- **Each install owns its appliance bridge `<prefix>-net`** (was: appliances on
  shared `incusbr0`). Subnet is `ipv4.address=auto` — let Incus pick a free /24,
  provably non-overlapping, vs. a `--appliance-cidr` flag or deriving from the
  tenant pool. `--bridge` default flipped from `incusbr0` to empty (empty ⇒ own
  bridge; set ⇒ use that existing bridge) — a deliberate behaviour change.
  No unit test (no `TenantCreateServer` fake; logic mirrors the live-tested
  `ensureV2Bridge`) — validated on the live install.
- **Skip the broker entirely for Cloudflare ingress** (was: broker deployed with
  a container-internal `:9443`). It was unreachable dead weight — no host port,
  no tunnel route; tenant self-service rides the auth-app `/api/projects`.
  Removed the `NoHostPort` half-measure. Existing-install guard keys on the
  auth-app instance, so detection still works with no broker project.
- **Shared incus dir auto-detects `~/.config/incus`.** Prefer the native dir so
  plain `incus` works with no wrapper, but only when it has no foreign identity.
  A `.sandcastle-owned` marker (dropped *before* the client cert is written)
  pins the choice so the dir doesn't flip to the dedicated dir once its own
  `client.crt` appears. Driven by the hard constraint: one keypair per Incus
  config dir, so an admin cert and a restricted tenant cert can't coexist —
  which keeps hosts/admin workstations on the dedicated dir automatically.
- **Provision on a detached context** (`context.Background()`, 8-min budget, per
  device-code lock) instead of the poll request context. Workaround for a real
  limit: over a flaky Cloudflare tunnel the client poll times out (~30s) and
  cancels the request, which aborted provisioning mid-flight so bring-up never
  finished. This was *the* unlock for the from-scratch dual-install e2e. A full
  async-job design is possible; this was the minimal correct fix.
- **Provisioning idempotency/boot-race fixes** exposed by a cached-base-image
  host (creates return instantly): tolerate spurious "already running" on
  `Start:true`; wait for RUNNING before configuring an appliance/sidecar; start
  an existing STOPPED sidecar on re-provision.
- **Trust union + per-remote project pin** (shared-identity core): Incus keys
  trust by cert fingerprint, so multiple installs sharing one client cert means
  each install must *union* its projects into the one trust entry, and each
  remote must be *pinned* to its install's default project (the shared cert's
  server-side default is otherwise ambiguous and lists the wrong install's
  machines).
- **Environment note (not a code decision):** the test VMs' frp link drops
  constantly and aggressive manual cleanup (`rm -rf /var/lib/incus`, `ip link
  delete` on bridges) corrupts the Incus seccomp/device runtime → fresh sidecars
  flake to STOPPED; a daemon/VM reboot clears it. Tear down tenant bridges with
  `incus network delete` (clear the app project's default-profile `eth0` first),
  never raw `ip link`, or dnsmasq orphans hold the gateway `:53`.

## 2026-07-07 — foreign v1 tenant CIDR adopted as own on a second install

- **Bug (live on big):** first login to the `tc2` install by GitHub user
  `thieso2` failed with `dnsmasq: failed to create listening socket for
  10.248.1.1: Address already in use`. `ProvisionReuseInputs` scoped v2
  (`kind=infra`) own-tenant matching by `meta.KeyV2Prefix`, but the v1
  (`kind=tenant`) branch matched by tenant name alone — so the old `sc`
  install's v1 project `sc-thieso2` (10.248.1.0/24) was adopted as the new
  install's own CIDR (`PreferredCIDR`) instead of counting as occupied, and
  tc2 tried to build its tenant bridge on the live `sc-thieso2` gateway.
- **Fix:** v1 (`kind=tenant`) projects are **never** own in the v2
  provisioning path — same-named or not, whatever the prefix, their `/24` is
  always occupied. Alternative considered: recognize a v1 tenant as own when
  the project name equals `<installPrefix>-<tenant>` (v1 carries no prefix
  metadata), so a same-install v1→v2 re-provision keeps its /24 — rejected
  because the v1 bridge (dnsmasq bound to the gateway IP) may still be live,
  so reusing the /24 collides at bridge creation even within the same
  install, and the v2 path never creates `kind=tenant` projects anyway.
  Regression test: `TestProvisionReuseInputsNeverOwnsV1CIDR`. One fix covers
  all three provisioning paths (device login, `sc-adm tenant create`, project
  broker) — they all call `ProvisionReuseInputs`.

## 2026-07-07 — `sc list` (and project/dns/status) matched same-named tenants unscoped

- **Bug (witnessed live):** with two installs sharing one Incus daemon and the
  same tenant name in both (the standard coexistence shape — one GitHub user,
  two installs), `sc create dev` succeeded but `sc list` came back without the
  machine. The earlier cross-install scoping fix covered `sc create`,
  `sc connect`, lifecycle, and `sc incus*` (via `v2TenantSummary` →
  `tenant.ListForPrefix`), but `sc list`, `sc project *`, the dns/trust
  commands, and `sc status` still resolved the tenant by NAME only over the
  unscoped `tenant.List` — and the other install's same-named tenant sorts
  first (`id-…` < `sc2-…` in the project scan), so those commands silently
  operated on the other install.
- **Fix:** one shared `scopedListTenants` helper (prefix from
  `installPrefixFromRemoteName`, i.e. the current remote is the single source
  of truth), used by `listMachines`, `currentTenantSummary` (project.go), and
  `findTenantSummary` (dns.go); `tenant.GetStatusWithTopologyForPrefix` for
  `sc status`. Unscoped fallback (empty prefix) is preserved for admin remotes
  and v1 shapes. Alternative considered: filter inside `tenant.List` by a
  store-carried prefix — rejected as it would push CLI remote-name semantics
  into the tenant package's store abstraction.
- Regression test: `TestListMachinesScopedToCurrentInstall` (two installs,
  same tenant name, each remote must see its own machine set).

## 2026-07-07 — Incus 7.x broke idmapped-mounts detection → shared /home silently gone

- **Bug (caught by the shared-home e2e battery on obelix):** on a fresh Incus
  7.2 host the `home` volume was created but attached to NO profile, and both
  shared volumes were created unshifted — CT↔VM `/home` sharing silently
  gone. `SupportsIdmappedMounts` keyed on
  `kernel_features["idmapped_mounts"] == "true"`, and Incus 7.x stopped
  populating `environment.kernel_features` (always `{}`), so every 7.x host
  read as idmapped-less.
- **Fix:** absent entry → supported (Incus 7.x's kernel floor 5.15 already
  includes idmapped mounts, which landed in 5.12); explicit `"false"` (older
  daemons that still report) → unsupported. Alternative considered: probing
  by attaching a shifted volume — rejected, the failure only surfaces at
  instance start. Known tradeoff: a container-hosted incus (nested CT) also
  reports `{}` and would now try shifted volumes and fail at machine start —
  that topology can't host the tenant VMs anyway and is not a supported
  server shape. Regression test: `TestKernelFeaturesSupportIdmappedMounts`.

## 2026-07-07 — terminal provisioning errors kept the device login polling to timeout

- **Bug (immutability e2e check):** `sc login --force --dns-suffix other`
  printed the immutable-suffix error immediately but then polled for ~10
  minutes to "device login polling timed out", with the server re-attempting
  provisioning on every poll. Provisioning failures always left the device
  login `pending` — right for transient bring-up errors (the retry loop is
  deliberate), wrong for deterministic user-input errors.
- **Fix:** a `tenant.TerminalProvisionError` wrapper marks no-retry-can-fix
  errors (immutable-suffix conflict, rejected suffix); the poll handler
  DENIES the device login on one, and the client surfaces
  `device login denied: <message>` (exit 1) on its next poll. Transient
  errors keep the pending/retry behavior. Regression test:
  `TestDevicePollDeniesLoginOnTerminalProvisioningError`.
- **Deploy gotcha (harness):** `incus file push` over an existing same-named
  file through the nested (big → obelix VM) path silently left the OLD file
  in place once — always `rm -f` the target first and verify `sha256sum`
  after pushing a binary.

## 2026-07-07 — multi-suffix client DNS: global resolved drop-ins replaced by per-suffix link scopes

- **Bug (coexistence e2e, two installs on one Linux client):** only one Tenant
  DNS Suffix ever resolved via `getent` even though `dig @<sidecar>` answered
  for both. Two layered causes:
  1. The sidecar Corefile's catch-all `.:53` FORWARDED foreign names upstream,
     so a tailnet client asking the wrong tenant's server got an authoritative
     NXDOMAIN instead of the REFUSED the client-resolver design depended on.
  2. Even with REFUSED, systemd-resolved's GLOBAL scope (where the
     resolved.conf.d drop-ins landed every tenant server) asks only its
     rotating "current server" — the REFUSED answers rotate it onto the
     public upstream and then BOTH tenant zones die with public NXDOMAINs.
     Per-domain routing in resolved only works ACROSS link scopes; the `10-`
     filename-ordering trick was never sufficient.
- **Fix, server side:** the Corefile catch-all now REFUSES tailnet sources
  (`acl { block net 100.64.0.0/10 }`) — machines on the tenant bridge keep
  full recursion (that server is their only DNS), clients get the terminal
  REFUSED.
- **Fix, client side (the real one):** each suffix gets its own resolved link
  scope: `sandcastle-dns-<suffix>.service` creates a dummy link
  (`scdns-<fnv32-hash>`, name ≤ IFNAMSIZ) with a deterministic 169.254/16
  link-local address — resolved does NOT activate a link's DNS scope until
  the link carries an address (found empirically; a bare `up` dummy stays
  "Current Scopes: none") — and pins `DNS=<CoreDNS>` `Domains=~<suffix>` via
  resolvectl. `PartOf=systemd-resolved.service` re-applies the runtime scope
  whenever resolved restarts (validated live: restart → scopes re-form,
  both zones + public resolve). Install removes any legacy drop-in (plus one
  resolved restart to flush its global servers). Alternatives considered:
  systemd-networkd .network files (only work where networkd manages links)
  and putting tenant servers on the tailscale0 link (same flat-list problem,
  plus clobbers MagicDNS).
- macOS is untouched: `/etc/resolver/<suffix>` is natively per-domain.

## 2026-07-07 — `sc-adm tenant delete` on a v2 tenant was a silent no-op success

- **Bug (audit, validated live):** `tenant delete e2ea --yes` on a v2 tenant
  printed "Deleted runtime resources for e2ea; durable state was preserved."
  and deleted NOTHING — `PlanDelete` computes v1 `sc-<tenant>` resource names
  that don't exist for v2, and each per-resource delete is ignore-not-found.
  An operator would believe the tenant was gone.
- **Fix:** the delete command first runs `tenant.PlanDeleteV2` (scoped to the
  install prefix — a same-named tenant of another install must not be
  touched); a v2 match routes to `TenantDeleter.DeleteTenantV2`, which tears
  down each app project (instances, images, shared home/workspace volumes —
  detached from the default profile first — and profiles), the infra project
  (sidecar), and the tenant bridge. Without `--purge` a v2 tenant is refused:
  the shared volumes live inside the app projects, so there is no meaningful
  "runtime only" subset (unlike v1, whose volumes live in the tenant project
  and survive a non-purge delete). First live run caught a second bug: the
  plan reused the v1 volume names (`sc-home`/`sc-workspace`) — the deletes
  404'd silently and project deletion failed with "Only empty projects can be
  removed"; v2 names are plain `home`/`workspace` (now shared constants
  `tenant.V2HomeVolumeName`/`V2WorkspaceVolumeName` used by create and
  delete). The sidecar's tailnet device is deliberately not removed (BYO
  tailnet, no server-side API key — ADR-0017); documented instead.

## Running Notes

- Started implementation from the committed domain docs (`CONTEXT.md`,
  `docs/sandcastle-v1-spec.md`, ADR-0001). The existing Go code is still built
  around the superseded owner/project/sandbox model.
- First implementation slice is the foundational naming and metadata vocabulary,
  because CLI parsing, Incus resource names, DNS, routes, and tests all depend
  on those types.
- `internal/naming` now owns the new reference grammar:
  `tenant/project`, `tenant/machine`, `tenant/project/machine`, user
  `machine`, and user `project/machine`. Incus tenant project names are
  `sc-{tenant}` and machine instance names are `{project}-{machine}`.
- Local/admin config moved from `Owner`/`SANDCASTLE_OWNER` to
  `Tenant`/`SANDCASTLE_TENANT`. Local config also has a `Project` field for the
  current-project behavior.
- `internal/meta` now serializes `tenant`, `machine`, and route target tenant
  fields. I moved the previous per-project SSH public key to tenant metadata
  because the new spec has tenant-scoped infrastructure/storage and projects
  have no settings.
- Renamed the old Incus-project lifecycle package from `internal/project` to
  `internal/tenant`. Its focused tests now cover tenant creation/deletion/list
  and status. `PlanCreate` takes only a tenant name, derives `sc-{tenant}`,
  initializes the `default` project in tenant metadata, and renders DNS for the
  tenant suffix.
- Renamed the runtime package from `internal/sandbox` to `internal/machine`.
  Machine planning now uses current tenant/current project resolution, Incus
  instance names of `{project}-{machine}`, private hostnames of
  `{machine}.{project}.{tenant}`, and tenant storage defaults of
  `{project}/{machine}`.
- Local DNS, local trust, Tailscale, and restricted-user grants now resolve
  tenant references rather than owner/project references. Local DNS writes a new
  `tenants:` state schema with `dnsSuffix` entries; there is intentionally no
  migration or compatibility alias for the old `projects:` local state.
- Restricted-user grants still produce Incus restricted certificate `Projects`
  because that is the Incus API surface, but command input is now tenant refs
  and maps to `sc-{tenant}`.
- Public route planning and host overrides now target machines, not sandboxes.
  Canonical references are `tenant/project/machine`; user-side calls may resolve
  `machine` or `project/machine` through `SANDCASTLE_TENANT` and
  `SANDCASTLE_PROJECT`. Route metadata writes `targetTenant`,
  `targetProject`, and `targetMachine`.
- `sandcastle route status <hostname>` is implemented as a filtered read over
  the existing route listing API rather than a new broker endpoint. That keeps
  the current mTLS broker protocol smaller while still exposing the v1 command
  shape; it can become a dedicated metadata lookup later if route lists become
  too large.
- Route broker authorization is now tenant-grant based. The mTLS principal has
  a human user string for audit (`CreatedBy`), but route create/delete
  authorization checks whether the certificate grants the target Incus tenant
  project (`sc-{tenant}`), not whether the user name matches the tenant.
- The Incus adapter layer moved from project/sandbox method semantics to
  tenant/machine semantics. The remaining old public-surface names are Incus API
  terms such as project, or historical notes explicitly describing the
  superseded model.
- User CLI command names now expose the new no-alias surface for the main
  machine lifecycle: `list`, `create`, `connect`, and `delete`. I removed the
  old `ls`, `add`, `enter`, and `rm` registrations from the root command rather
  than keeping compatibility aliases. `status <machine>` now uses the machine
  status planner/result directly; the old `inspect` command is no longer
  registered.
- `sandcastle list` now lists machines in the current tenant instead of listing
  tenant summaries. It scopes to `SANDCASTLE_PROJECT` unless `--all-projects/-a`
  is supplied or no current project is configured. The `--include-unmanaged/-u`
  flag shows non-Sandcastle Incus instances for tenant-wide lists, while the
  unmanaged count is always printed even when unmanaged rows are hidden.
- The admin tenant lifecycle group is now `sandcastle-admin tenant ...` instead
  of `project ...`. The admin machine lifecycle is now top-level:
  `sandcastle-admin list tenant[/project]`,
  `sandcastle-admin create/connect/status/delete tenant[/project]/machine`.
  These commands translate admin refs into the same tenant-scoped machine
  planners used by the user CLI so the admin and user paths do not diverge.
- Admin tenant access is now exposed in tenant-first command shape:
  `sandcastle-admin tenant grant <tenant> <user>`,
  `sandcastle-admin tenant revoke <tenant> <user>`, and
  `sandcastle-admin tenant users <tenant>`. These commands still mutate Incus
  restricted certificate project grants internally, because Incus calls the
  access boundary a project. The duplicate user-first
  `sandcastle-admin user grant <user> <tenant>` surface has been removed so
  tenant access has one canonical admin shape.
- Bare machine resolution for existing-machine operations now searches across
  the current tenant only when no current project is configured. If exactly one
  project contains the machine name, `connect`/`status`/`delete` use it; if
  multiple projects match, the CLI returns an ambiguity error and requires an
  explicit `project/machine`. When `SANDCASTLE_PROJECT` is set, bare names stay
  scoped to that project.
- User project management now lives under `sandcastle project
  list/create/status/delete`. Projects remain lightweight tenant metadata only.
  Project status intentionally reports tenant, project, and machine count rather
  than infrastructure checks, because v1 projects do not own Incus networks,
  DNS, storage, or CA state. Delete requires `--yes`, rejects `default`, and
  checks the tenant's machine metadata to ensure the project is empty. There is
  no `createdBy` value yet for user-created projects because the current local
  config identifies the tenant but not the human principal.
- I cleaned up several command help strings and e2e fixture references that
  still said owner/project/sandbox. The e2e tests that create machines now use
  the v1 instance and DNS shape (`default-{machine}` or `{project}-{machine}`,
  `{machine}.default.{tenant}` / `{machine}.{project}.{tenant}`).
- Updated the user-facing usage docs, quickstart snippets, README overview, and
  `.env.default` examples away from `SANDCASTLE_OWNER`, `add`/`enter`/`rm`/
  `inspect`/`ls`, and sandbox wording toward tenant/project/machine command
  shape. Later docs passes replaced the deeper implementation and e2e planning
  docs with the current tenant/project/machine shape.
- Disposable VM e2e with Debian 13's Incus 6.0.4 exposed that tenant storage
  pool creation cannot pass a derived `source` path for `dir` pools: Incus does
  not create that nested path before volume file upload. The creator now omits
  `source` for `dir` pools and lets Incus manage the pool path; non-dir pools
  still derive a per-tenant source from the admin pool.
- E2E fixtures and diagnostics now use tenant references and tenant local-DNS
  state. Safe e2e tiers pass after the latest CLI-shape work:
  `go test ./...`, `scripts/e2e.sh unit`, `scripts/e2e.sh gated`, and
  `scripts/e2e.sh local`. Full destructive tiers still need more environment
  setup than the host currently provides: image-dependent tests require
  `SANDCASTLE_E2E_BASE_IMAGE_SOURCE` and `SANDCASTLE_E2E_AI_IMAGE_SOURCE`, the
  restricted tier requires a non-local HTTPS Incus remote, and Tailscale/public
  route tiers require external credentials or DNS inputs.
- Disposable VM e2e on Debian 13/Incus 6.0.4 also exposed that SDK image copy
  from the default project into a tenant project fails over a local Unix socket
  with `The source server isn't listening on the network`. Tenant image
  propagation now uses Incus relay mode for project image copies. This is less
  efficient than pull mode for remote-to-remote copies, but it works for the
  local-admin path and keeps behavior deterministic across Unix-socket and HTTPS
  remotes.
- The same Incus 6.0.4 VM does not support the storage volume file API used by
  newer Incus clients for custom volumes (`CreateStorageVolumeFile`/
  `GetStorageVolumeFile` return `not found`). For local dir-backed storage
  volumes, the Incus adapter now falls back to reading/writing the project
  volume path under `/var/lib/incus/storage-pools/<pool>/custom/<project>_<vol>`
  after the SDK returns 404. HTTPS remotes and newer servers still use the SDK
  path first.
- Tenant private bridge names can no longer be simple 15-character truncations
  of long Incus project names. Linux bridge names have a 15-character limit, but
  truncation made e2e tenants like `sc-tenant-e2e-local...` collide on
  `sc-tenant-e2e-l`, causing sidecar IP/subnet validation failures. Long names
  now use a stable `sc-` plus 12-hex hash bridge name.
- I created disposable Incus VMs twice and seeded nested Incus images to keep
  exercising `scripts/e2e.sh local-vm`. Docker-based image building filled the
  host's 9.6 GB root filesystem, so I switched to a lean Incus-native image seed
  by copying `images:debian/13`, installing only sidecar runtime packages, and
  publishing `sandcastle/base:latest`/`sandcastle/ai:latest`. Even with that,
  nested tenant creation and image copies filled the host root filesystem and
  forced the VM into Incus `ERROR`; both disposable VMs were deleted to restore
  space. Current verified host gates after these fixes: `go test ./...`,
  `scripts/e2e.sh unit`, `scripts/e2e.sh gated`, and `scripts/e2e.sh local`.
- Host Incus e2e needed the same image shape as the Debian base Dockerfile.
  Using a quick Ubuntu cloud image seed let Caddy/CoreDNS install, but it already
  had UID/GID 1000 allocated and caused tenant user bootstrap to silently miss
  the expected Linux user in one detached create path. I replaced the host seed
  with an Incus-native Debian 13 container image customized with the base runtime
  packages and `sandcastle-bootstrap`, then pointed
  `sandcastle/base:latest`/`sandcastle/ai:latest` at that Debian image.
- Some base images expose `/etc/resolv.conf` as a symlink whose target does not
  exist when Incus' file API tries to overwrite it. Machine creation now falls
  back to an in-instance shell write for machine resolver configuration when
  `CreateInstanceFile("/etc/resolv.conf")` returns 404.
- With the Debian host seed, the destructive `incus` tier passed the CLI
  create/detach, default create/connect, connect, host override, image sync,
  local trust, tenant purge, tenant listing, and machine lifecycle cases.
  Remaining host `incus` failures from run `e2e-incus-20260521-1051`:
  infrastructure route broker mTLS probe got connection refused, and tenant DNS
  lookup from one machine timed out.
- Tenant DNS timeout was caused by Debian Incus images running
  `systemd-resolved`, which binds port 53 on loopback and prevents CoreDNS from
  binding `:53`. DNS sidecar CoreDNS restart now stops and masks
  `systemd-resolved` before launching CoreDNS. Verified with
  `TestTenantDNSE2E` on host Incus run `e2e-dns-fix-20260521-1100`.
- Infrastructure now uploads and runs `sandcastle-admin` for the route broker
  service (`sandcastle-admin route-broker serve`). `SANDCASTLE_ADMIN_BIN` is the
  preferred binary source, with `SANDCASTLE_BIN` as a local fallback for older
  setups. The e2e infrastructure tests build `./cmd/sandcastle-admin` for the
  target architecture before creating the sidecars.
- The route broker sidecar uses the mounted Incus Unix socket directly when it
  is serving inside infrastructure. Socket-mounted broker instances are marked
  privileged because an unprivileged container root cannot open the host
  `/var/lib/incus/unix.socket`; Caddy remains unprivileged and does not receive
  that mount.
- Tenant listing skips managed projects whose Sandcastle metadata kind is not
  `tenant`, so infrastructure projects (`kind=infrastructure`) no longer make
  `tenant list`/e2e diagnostics fail while parsing tenant summaries.
- Route broker mutation e2e now uses canonical `tenant/default/machine` target
  references. The host route-broker tier passed as
  `e2e-route-broker-20260521-1212`, including unowned-target 403, DNS-proof 400,
  add/list/remove 201/200, and route cleanup checks.
- The broader host `incus` tier passed again as `e2e-incus-20260521-1220` after
  the route broker service and route ingress fixes. The broker mutation path now
  uses the default host Incus socket mount, and the dedicated `route-broker` tier
  covers that socket-mounted path.
- Added `scripts/e2e-local-vm.sh` as a reusable host-side harness for the
  VM-only local mutation tier. It launches a disposable local Incus VM, installs
  Go, mise, and nested Incus, copies the checkout, seeds nested image aliases
  from host `sandcastle/base:latest` and `sandcastle/ai:latest`, starts root's
  systemd user service manager for the local DNS service test, and runs
  `scripts/e2e.sh local-vm` inside the VM. This replaces prior ad hoc VM setup
  attempts and gives the remaining disk-constrained verification a repeatable
  entry point.
- Debian 13/Incus 6.0.4 also returned success for custom-volume file uploads on
  local dir-backed tenant pools while leaving the CA files empty. When running
  against local dir-backed volumes as root, the adapter now writes directly to
  `/var/lib/incus/storage-pools/<pool>/custom/<project>_<vol>` instead of using
  the broken upload path. The host-side VM harness passed end-to-end as
  `e2e-local-vm-20260521-122306`, covering host overrides, local DNS service
  install/reload/uninstall, local trust, and platform trust.
- The same custom-volume subdirectory fix initially regressed host e2e runs
  executed as a non-root Incus admin user: the SDK returned 404 for volume-file
  directory creation, but the process could not write `/var/lib/incus` directly.
  Machine creation now falls back to a short-lived storage helper container in
  the tenant project. The helper mounts the top-level `sc-home` and
  `sc-workspace` custom volumes, creates the requested subdirectories with UID
  and GID 1000, then is deleted before the real machine is created. This keeps
  non-root local Incus, root VM, and newer remote Incus paths working. Verified
  host tiers: `incus` passed as `e2e-20260521-123440-18835`; `route-broker`
  passed as `e2e-20260521-123909-31423`; the host-side VM harness passed again
  as `e2e-local-vm-20260521-124224`.
- Removed stale user-as-tenant bootstrap output from `sandcastle-admin user
  create/token`. The human output now tells developers to run `sc remote add
  ...` and set the default tenant explicitly after access is granted, while
  `sc remote add --tenant` remains the one-step handoff path when the tenant is
  already known.
- Replaced the old implementation and e2e planning docs with the current
  tenant/project/machine shape. The docs now describe tenant-scoped DNS,
  Tailscale, local trust, route broker authorization by restricted certificate
  grants, the disposable VM harness, and the current command names instead of
  owner/project/sandbox milestones.
- Renamed the route broker mTLS principal identity from `Owner` to `User`.
  Authorization was already grant-based; this removes the misleading implication
  that the user name must own or match the target tenant. Route metadata keeps
  `CreatedBy` as the audit field.
- Renamed the remaining private Go `sandbox` vocabulary to `machine` across the
  CLI, Incus adapter, cert, Caddy, route, and e2e helpers. The behavioral API
  was already machine-oriented; this pass removes stale type/function/file names
  such as `SandboxCreator`, `RenderSandbox`, and `sandbox_lifecycle.go`.
- Cleaned up more private e2e/test vocabulary after the public docs pass:
  restricted-user, Tailscale, route-broker, local DNS, and cleanup fixtures now
  distinguish human users from tenants instead of using `owner` as a generic
  variable name. This was behavior-preserving, but it makes the grant-based
  route broker model harder to misread.
- Renamed the private machine connection path from `enter`/`add` vocabulary to
  `connect`/`create`: `PlanConnect`, `ConnectPlan`, `MachineConnector`, and the
  e2e runner's CLI test names now match the public command surface. The executor
  still delegates to Incus `ExecInstance`; only Sandcastle's internal naming
  changed.
- Renamed the tenant lifecycle Incus adapters from project-facing store,
  creator, deleter, and SSH-key updater names to tenant-facing names. Literal
  Incus API methods and struct fields still use project terminology where Incus
  itself exposes projects, but Sandcastle-facing dependencies now read as
  tenant stores, creators, and resources.
- Verified the tenant adapter rename against full host Incus, dedicated route
  broker, and disposable local VM tiers on 2026-05-21:
  `e2e-20260521-134205-85388`, `e2e-20260521-134629-98093`, and
  `e2e-local-vm-20260521-134918`.
- Renamed private imports of `internal/tenant` from the old `project` alias to
  `tenant`, and renamed the tenant lifecycle/DNS/listing e2e tests plus the
  `scripts/e2e.sh incus` regex to match. Incus SDK method names still say
  `Project` where they call Incus projects directly.
- Verified the tenant import/test rename against safe and destructive tiers on
  2026-05-21: `scripts/e2e.sh local` run `e2e-20260521-135906-105611`, host
  `incus` run `e2e-20260521-135924-105773`, route-broker run
  `e2e-20260521-140348-118342`, and disposable VM run
  `e2e-local-vm-20260521-140634`.
- Local trust help, output, and adapter errors now say tenant CA instead of
  project CA. The command was already tenant-scoped; this only fixes stale
  wording and docs examples that omitted the tenant argument during cleanup.
- Refreshed the docs front doors after the tenant/machine rename pass. README
  now links the usage guide and admin/developer quickstart; the usage guide's
  tenant sections no longer call tenant delete/grant operations "project"
  operations; the quickstart uses the current private DNS shape
  `machine.project.tenant` and includes the tenant argument for Tailscale
  cleanup; the e2e plan examples now match the Debian 13 image aliases used by
  the runner examples.
- Renamed the route creation internals from `Add` vocabulary to `Create`
  vocabulary across the route planner, route broker client/server, Incus route
  manager, CLI wiring, and tests. The HTTP broker endpoint remains `POST
  /routes`; only Sandcastle's internal architecture now matches the public
  `sandcastle route create` command. I left host override internals as `Add`
  because that package is modeling local hosts-file entry addition rather than
  a public command verb.
- Verified that route create rename with `go test ./...`, `scripts/e2e.sh
  gated`, `scripts/e2e.sh local` run `e2e-20260521-142111-127490`, and the
  dedicated route broker mutation tier run `e2e-20260521-142123-127636`.
- Removed the obsolete `SANDCASTLE_E2E_DOMAIN_SUFFIX` harness/workflow setting.
  Tenant DNS suffixes are now always derived from tenant names, so keeping a
  separate e2e domain suffix suggested the superseded project-domain model.
- Verified the e2e harness cleanup with `go test ./...`, `scripts/e2e.sh
  gated`, `scripts/e2e.sh local` run `e2e-20260521-142654-132797`, and
  `scripts/e2e-local-vm.sh` run `e2e-local-vm-20260521-142730`.
- Removed the remaining public `ValidateProjectDomain` helper and moved domain
  validation directly into `ValidateTenantDNSSuffix`. Tenant DNS suffixes are
  intentionally single-label tenant-derived names, not configurable project
  domains. I also refreshed the low-level Caddy, certificate, and local DNS
  fixtures away from `project-tld` examples so test vocabulary matches the
  tenant DNS model.
- Verified the tenant DNS validator cleanup with focused domain/local DNS/cert
  tests, `go test ./...`, `scripts/e2e.sh gated`, `scripts/e2e.sh local` run
  `e2e-20260521-143922-137741`, host `incus` run
  `e2e-20260521-143930-137902`, route-broker run
  `e2e-20260521-144404-150446`, and disposable VM run
  `e2e-local-vm-20260521-144651`.
- Changed `sandcastle host override list` from the misleading required
  `project` argument to `list [tenant]`. Host overrides are tenant-level
  machine metadata, so the command now defaults to the current tenant while
  still allowing an explicit tenant for admin-style inspection.
- Verified the host override list shape with `go test ./internal/cli
  ./internal/hostoverride`, `go test ./...`, `scripts/e2e.sh gated`,
  `scripts/e2e.sh local` run `e2e-20260521-145351-156029`, and targeted local
  Incus `TestHostOverrideE2E`.
- Renamed the remaining internal machine inspect planner/formatter/test
  vocabulary to status vocabulary. This is behavior-preserving, but it removes
  the last old command-shape name from the machine status path.
- Verified the machine status rename with `go test ./internal/machine
  ./internal/cli ./internal/e2e`, `go test ./...`, `scripts/e2e.sh gated`,
  `scripts/e2e.sh local` run `e2e-20260521-145647-159278`, and targeted local
  Incus `TestCLICreateDetachE2E` run `e2e-20260521-145657-000000000`.
- Changed `sandcastle tailscale up|status|down` to take an optional tenant
  argument. The Tailscale planners already defaulted an empty reference to the
  current tenant; the CLI now matches the spec's current-tenant user flow while
  still allowing explicit tenant references.
- Verified the Tailscale current-tenant CLI shape with `go test ./...`,
  `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-150242-162965`. The destructive Tailscale tier still requires
  a `SANDCASTLE_E2E_TAILSCALE_AUTHKEY` and route approval policy, which this
  environment does not provide.
- Removed the stale `.env.default` `SANDCASTLE_E2E_DOMAIN_SUFFIX` example that
  survived the earlier harness cleanup. The e2e harness already derives tenant
  DNS suffixes from tenant names, so leaving the old project-domain knob in the
  template would send operators toward a setting the code no longer reads.
- Renamed the admin/runtime "project prefix" config field to "Incus project
  prefix" and changed the documented env template to
  `SANDCASTLE_INCUS_PROJECT_PREFIX`. This prefix controls Incus project names
  like `sc-<tenant>`, not Sandcastle project namespaces. The loader still
  accepts the old `SANDCASTLE_PROJECT_PREFIX` as a fallback so existing local
  environments do not silently fall back to `sc`; the new env var wins when
  both are set.
- While updating the prefix docs, fixed older admin examples that still used
  `SANDCASTLE_PRIVATE_CIDR_POOL` and `SANDCASTLE_INFRASTRUCTURE_PROJECT`; the
  code reads `SANDCASTLE_CIDR_POOL` and `SANDCASTLE_INFRA_PROJECT`.
- Verified the Incus project prefix/env cleanup with `go test ./...`,
  `scripts/e2e.sh gated`, `scripts/e2e.sh local` run
  `e2e-20260521-150755-165557`, host `incus` run
  `e2e-20260521-150808-165654`, route-broker run
  `e2e-20260521-151232-178254`, and disposable VM run
  `e2e-local-vm-20260521-151522`.
- Renamed the private admin CLI command constructors and e2e test names from
  `AdminProject` to `AdminTenant`. The public command was already
  `sandcastle-admin tenant`; this removes stale internal vocabulary without
  changing command behavior.
- Verified the admin tenant private rename with `go test ./internal/cli
  ./internal/e2e`, `go test ./...`, `scripts/e2e.sh gated`, and
  `scripts/e2e.sh local` run `e2e-20260521-152337-183929`.
- Renamed a few remaining private tenant-summary helpers and local variables
  that still used project wording (`tailscale.projectSummary`, tenant status
  list results, route-broker e2e delete plans). Incus API variables that hold
  actual Incus projects remain project-named.
- Verified the tenant-summary helper rename with `go test ./internal/tailscale
  ./internal/tenant ./internal/e2e ./internal/incusx`, `go test ./...`,
  `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-152606-185485`.
- Renamed the machine lifecycle delete action from the private/internal
  `remove` value to `delete`. The public command has been
  `sandcastle delete`; keeping `remove` in JSON plans and executor messages
  was unnecessary command-shape drift.
- Verified the machine delete action rename with `go test ./internal/machine
  ./internal/incusx ./internal/cli ./internal/e2e`, `go test ./...`,
  `scripts/e2e.sh gated`, `scripts/e2e.sh local` run
  `e2e-20260521-153150-187628`, and targeted local Incus
  `TestMachineLifecycleE2E` run `e2e-20260521-153200-000000000`.
- Renamed public route and local host override delete internals from remove to
  delete vocabulary: `DeleteRequest`, `DeletePlan`, `PlanDelete`, manager
  `Delete`, broker `AuthorizeDelete`, CLI formatters, and e2e probe labels.
  I left low-level helpers named `RemoveHostsEntry`, `removeMachineExtraSAN`,
  `removeRouteIngressAttachment`, and `removeRouteBacklink` because those
  describe removing individual entries/devices from local files or Incus
  metadata, not the public command action.
- Tightened user-facing help/docs for the same slice: `sandcastle route delete`
  and `sandcastle host override delete` now say delete, `dns service
  uninstall` says uninstall instead of stop/remove, and the usage/e2e docs use
  delete wording for route and host override workflows.
- Verified the route/host override delete vocabulary slice with `go test
  ./internal/route ./internal/hostoverride ./internal/routebroker
  ./internal/incusx ./internal/cli ./internal/e2e`, `go test ./...`,
  `scripts/e2e.sh gated`, `scripts/e2e.sh local` run
  `e2e-20260521-153741-192726`, host `incus` run
  `e2e-20260521-154044-195768`, route-broker runs
  `e2e-20260521-154507-208246` and `e2e-20260521-155343-213621`, and
  disposable VM run `e2e-local-vm-20260521-154755`.
- Adjusted `sandcastle route create` and `sandcastle host override create`
  help text from "Plan..." to "Create..." because both commands mutate by
  default and only render plans when `--dry-run` is supplied. The dry-run flag
  text still uses plan vocabulary intentionally.
- Verified the create-help wording cleanup with `go test ./internal/cli
  ./internal/route ./internal/hostoverride`, `go test ./...`,
  `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-160128-222392`.
- Adjusted `sandcastle-admin user token` help text from "Plan..." to
  "Create..." because it creates a restricted certificate add token by default
  and only renders the token plan with `--dry-run`.
- Verified the user-token help cleanup with `go test ./internal/cli
  ./internal/usertrust`, `go test ./...`, `scripts/e2e.sh gated`, and
  `scripts/e2e.sh local` run `e2e-20260521-160343-223640`.
- Implemented the documented `sandcastle config unset <key>` command for the
  same local config keys supported by `config set`: `tenant`, `project`,
  `remote`, and `admin_remote`. The v1 spec already showed `config unset
  project`; the command was missing from the CLI. Unsetting clears only the
  selected key and preserves the rest of `~/.config/sandcastle/config.yml`.
- Updated the usage guide, admin/developer quickstart, and implementation plan
  for `config unset`.
- Verified `config unset` with `go test ./internal/cli ./internal/config`,
  `go test ./...`, `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-160626-224980`.
- Corrected stale docs that still described removed or unimplemented admin
  command shapes: `CONTEXT.md` now lists the implemented restricted-user
  surface (`user create`, `user token`, and tenant access commands), and the v1
  spec now states that admin `status` requires an explicit machine reference.
- Verified the docs audit cleanup with `go test ./...`, `scripts/e2e.sh
  gated`, and `scripts/e2e.sh local` run `e2e-20260521-160859-226933`.
- Wired `sandcastle-admin version` to the existing admin-specific version
  command helper instead of the generic user CLI version helper. The output
  payload remains unchanged; only admin help now says "Print the Sandcastle
  admin command version".
- Verified the admin version help cleanup with `go test ./internal/cli`, `go
  test ./...`, `scripts/e2e.sh gated`, and `scripts/e2e.sh local` run
  `e2e-20260521-161041-228322`.
