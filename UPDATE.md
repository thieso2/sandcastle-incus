# How Sandcastle components are updated

This is the operator/developer reference for the self-update system (issue
#124). It explains **what carries the Sandcastle binary**, **how those
components are told apart from ordinary tenant workloads**, and **which command
updates which component, by what mechanism**.

For the "why" behind specific choices see `implementation-notes.md`
(2026-07-17, "self-update system (#124)"). Domain vocabulary is canonical in
`CONTEXT.md`.

---

## 1. The one distinction that matters: `user.sandcastle.kind`

Every Incus instance Sandcastle creates is tagged with a `user.sandcastle.kind`
metadata key (`internal/meta/meta.go`). That key — **not** the instance name,
project, or host — decides whether an instance is a *Sandcastle component* that
carries the fat binary and gets updated, or an ordinary workload that does not.

| `kind`      | What it is                                   | Runs `sandcastle-admin` as a service? | In the fleet / updated? |
|-------------|----------------------------------------------|:-------------------------------------:|-------------------------|
| `auth-app`  | **Global** Auth App appliance (per deployment) | ✅ `sandcastle-auth-app.service`      | Yes — admin plane        |
| `broker`    | **Global** Broker appliance (per deployment)   | ✅ `sandcastle-broker.service`        | Yes — admin plane        |
| `sidecar`   | **Per-tenant** infra (DNS + TLS leaf signer)   | ✅ `sandcastle-tls-sign.service`      | Yes — tenant plane       |
| `machine` *(or untagged)* | **Tenant workload** container/VM (user's stuff) | ❌ no Sandcastle binary at all | **Never**        |
| `infra`     | v2 per-tenant Incus *project* (holds CIDR/suffix) | ❌ (a project, not an instance)     | Never                    |
| `project`   | v2 per-project Incus *project* (app machines)  | ❌                                     | Never                    |
| `route`     | Legacy public-route marker                     | ❌                                     | Never                    |

The fat binary lives at a single well-known path inside every component that
carries it:

```
/usr/local/bin/sandcastle-admin        (incusx.SandcastleBinaryPath)
```

Each carrier also records the release it last received:

```
user.sandcastle.binary-version = vX.Y.Z    (meta.KeyBinaryVersion)
```

Missing stamp ⇒ treated as **unknown ⇒ outdated**. This is distinct from
`user.sandcastle.version` (the topology schema version, always "2").

### How the fleet is enumerated

`TenantCreator.ListBinaryVersions()` → `classifyComponents()`
(`internal/incusx/binary_update.go`) does one `GetInstancesFullAllProjects`
call and keeps only instances whose `kind` is `auth-app`, `broker`, or
`sidecar`. The match is **positive**: everything else is dropped — a tenant
workload machine (`kind=machine`, or, when the user created it directly, no
`user.sandcastle.*` metadata at all) never passes the filter. That is why a
tenant's machines are never touched by an update: they are not components, they
hold no `/usr/local/bin/sandcastle-admin`, and the classifier never selects
them.

> **Global vs tenant is a role, not a place.** All of these instances can live
> on the same Incus host and even share it with several installs (`--prefix`).
> "Global" (`auth-app`/`broker`) means *one per deployment, serving every
> tenant*; "tenant" (`sidecar`, `machine`) means *scoped to a single tenant*.
> Install-scoping is by project prefix: `filterInstallComponents`
> (`internal/cli/admin_update.go`) keeps `<prefix>-infra` (auth-app),
> `<prefix>-broker`, and `<prefix>-<tenant>` (sidecar), so updating one install
> never disturbs a neighbour on the same host.

---

## 2. The three update actors

There are three independently-versioned things, updated by three different
triggers:

| Actor                     | Who runs it | Command            | Source of the new binary               |
|---------------------------|-------------|--------------------|----------------------------------------|
| **sc CLI** (your laptop)  | any user    | `sc update`        | GitHub release asset (self-replace)    |
| **Global appliances** (auth-app, broker) | operator/admin | `sc-adm update` | GitHub release asset → `incus file push` |
| **Tenant sidecar**        | tenant (owns it) | `sc update` | delegated: the deployment pushes its **own** running binary |

Tenant **workload machines** have no fourth row — they are never updated by
Sandcastle. Their contents are the user's responsibility.

---

## 3. `sc update` — the tenant/user command

`internal/cli/update_cmd.go`. Always user-initiated, never automatic. It prints
one status table and then applies what is outdated.

**CLI row.** Compares the running `version` (ldflags-stamped
`internal/cli.version`, `v0.0.0-dev` for un-stamped/dev builds) against the
latest (or `--version`-pinned) GitHub release. Applying self-replaces the
binary atomically, keeping a `.bak` (`internal/update/apply.go`). A
Homebrew-managed install is never self-replaced — it prints
`brew upgrade sandcastle` instead.

**Sidecar row.** Two probes, no GitHub access:

- **WANTED** = the deployment's version, learned passively from the
  `X-Sandcastle-Version` response header on an unauthenticated
  `GET https://<auth-hostname>/healthz` (`probeDeploymentVersion`). The header
  is stamped by the auth-app's `withVersionExchange` middleware
  (`internal/authapp/app.go`) from its own binary version. An appliance built
  before the version-exchange feature emits no header → the row reads
  `unknown (deployment reported no version)` (reachable but unversioned) vs
  `unknown (deployment unreachable)` (no answer at all).
- **CURRENT** = the tenant's own sidecar version, read from the signer's
  `X-Sandcastle-Version` header over the tenant network
  (`probeSidecarVersion`; derives the signer address from the broker URL's /24
  gateway → the DNS role address). Only reachable from a host on the tenant's
  tailnet.

**Applying the sidecar update** (`updateSidecarViaDeployment`) delegates to the
deployment — the sidecar never fetches from GitHub, so it can never run *ahead*
of the deployment:

1. **Auth-app token plane** (preferred, tunnel-friendly): if an auth token +
   auth hostname are configured, `authapp.DeviceClient.UpdateSidecar` calls the
   auth-app, which runs `SidecarSelfUpdater`.
2. **Broker mTLS plane** (fallback): `POST /v2/sidecar/update` to the broker
   over the restricted client cert.

Either way the server side is `SidecarSelfUpdater`
(`internal/incusx/binary_update.go`): it reads its **own** `os.Executable()`
and pushes that into the tenant's sidecar. That is why a sidecar tracks the
deployment automatically — e.g. after the auth-app itself is upgraded and its
reconcile runs, the tenant sidecar is rolled to match with no GitHub round-trip.

---

## 4. `sc-adm update` — the admin/fleet command

`internal/cli/admin_update.go`. Updates a deployment's **global** components
(auth-app + broker) and, on request, tenant sidecars. Unlike `sc update`, it
**pulls a GitHub release** and pushes the released, checksum-verified,
version-stamped binary — so appliances advertise a real `vX.Y.Z`, not
`v0.0.0-dev`.

```
sc-adm update            # update auth-app + broker to latest release
sc-adm update --check    # fleet version table only; change nothing
sc-adm update --prefix NAME      # target a specific install (see §6)
sc-adm update --version vX.Y.Z   # pin/rollback to a specific tag
sc-adm update --tenants a,b      # ALSO force-roll named tenants' sidecars
sc-adm update --all-tenants      # ALSO force-roll every sidecar
```

The install to act on is resolved as described in §6 (`--prefix` → env →
auto-detect). A remote hosting several installs requires `--prefix`.

Sidecars are **tenant-managed**: a plain `sc-adm update` does *not* touch them
(they update via `sc update`). `--tenants`/`--all-tenants` is the admin
override for a fleet-wide roll.

### Mechanism, per component

For each targeted component `sc-adm update`:

1. Resolves the release and downloads the asset for the component's
   **architecture** (`downloadArch` maps `x86_64→amd64`, `aarch64→arm64`), so a
   mixed-arch fleet gets the right binary each.
2. **Appliances** (auth-app/broker) → `UpdateApplianceBinary`:
   `incus file push` the binary to `/usr/local/bin/sandcastle-admin`, stamp
   `binary-version`, then `systemctl restart <unit> && systemctl is-active`.
   Units come from `componentUnits`:
   - `auth-app` → `sandcastle-auth-app.service`
   - `broker`   → `sandcastle-broker.service`
3. **Sidecars** (with `--tenants`) → `UpdateTenantSidecar`: same push + stamp,
   but restarts **only** `sandcastle-tls-sign.service` — CoreDNS and tailscaled
   are left running (sub-second, no DNS/network blip).

Every path is **idempotent**: a re-run repairs a partial update (push + stamp +
restart again).

### The image-builder is not updated

The Image Builder appliance runs upstream Debian + podman and never receives a
Sandcastle binary, so `sc-adm update` prints a one-line note and skips it
(see `implementation-notes.md`).

---

## 5. Version exchange & skew (#124 §6)

- Every auth-app/broker response is stamped `X-Sandcastle-Version` by
  `withVersionExchange` (`internal/update/exchange.go`, `internal/authapp/app.go`).
  This is how `sc update` learns the deployment version passively, and how a CLI
  prints a one-line skew notice after its normal output.
- `update.MinCLIVersion` is a compile-time constant (normally `""`). A
  known-breaking release sets it; the auth-app then returns `426 Upgrade
  Required` on `/api/*` for CLIs older than the minimum, with a clean
  "run `sc update`" message instead of protocol errors. Dev builds are exempt.
- `SANDCASTLE_NO_UPDATE_NOTIFIER=1` silences the passive CLI-side notices.

---

## 6. Targeting: which remote, which install

A deployment's identity is two things: the **Incus remote** (which daemon) and
the **install prefix** (which sandcastle on that daemon — several can share one
Incus remote via `--prefix`). There is no seed file (the `*.seed.yml`
"Infrastructure Seed File" is documented but not implemented).

- **Remote** (which daemon): `sc-adm` resolves it from `SANDCASTLE_REMOTE` /
  admin config `admin_remote` / cert auto-detect / the global incus
  `default-remote`. Note this is the *admin Incus remote*, which can differ from
  the user-CLI `remote:` in `config.yml` — e.g. the `idefix` install lives on
  the admin remote `home`. The admin CLI reads the real per-OS incus config
  (`~/.config/incus` on Linux, `~/Library/Application Support/incus` on macOS);
  when `INCUS_CONF` is unset it defaults to that native dir, so admin commands
  work on macOS without exporting `INCUS_CONF`.
- **Install** (which sandcastle on that daemon): `sc-adm update` resolves it in
  this order (`resolveUpdatePrefix`):
  1. `--prefix <name>` flag — explicit choice;
  2. an explicit `SANDCASTLE_INCUS_PROJECT_PREFIX` / `SANDCASTLE_PROJECT_PREFIX`
     env var;
  3. **auto-detection** from the live fleet: exactly one install present ⇒ it's
     used (and named on stderr); more than one ⇒ the command refuses and lists
     them so you pass `--prefix`; none ⇒ the configured/default prefix, whose
     "no updatable components" error carries the usual guidance.

  Installs are discovered by their global-appliance projects — an auth-app in
  `<prefix>-infra`, a broker in `<prefix>-broker` (`discoverInstallPrefixes`).

Example — update the `idefix` install from macOS. When the admin config's
`default-remote` already points at the right daemon and only one install lives
there, no env or flags are needed:

```
sc-adm update --check          # or: sc admin update --check
# → targeting install "idefix" (the only one on this remote)
```

Pin the remote and/or install explicitly when they aren't unambiguous:

```
SANDCASTLE_REMOTE=home sc-adm update --prefix idefix
```

(`sc admin …` and `sc-adm …` are the same admin plane — both resolve the incus
config, remote, and install identically.)

---

## 7. Quick reference

| Component        | `kind`     | Binary path                         | Version stamp                    | Restart unit                     | Updated by                         |
|------------------|------------|-------------------------------------|----------------------------------|----------------------------------|------------------------------------|
| Auth App         | `auth-app` | `/usr/local/bin/sandcastle-admin`   | `user.sandcastle.binary-version` | `sandcastle-auth-app.service`    | `sc-adm update` (release)          |
| Broker           | `broker`   | `/usr/local/bin/sandcastle-admin`   | `user.sandcastle.binary-version` | `sandcastle-broker.service`      | `sc-adm update` (release)          |
| Tenant sidecar   | `sidecar`  | `/usr/local/bin/sandcastle-admin`   | `user.sandcastle.binary-version` | `sandcastle-tls-sign.service`    | `sc update` (delegated) / `sc-adm update --tenants` |
| sc CLI           | —          | user's `$PATH`                      | `internal/cli.version` (ldflags) | —                                | `sc update` (release self-replace) |
| Tenant machine   | `machine`  | — (none)                            | —                                | —                                | **never**                          |
| Image Builder    | —          | — (podman, no binary)               | —                                | —                                | **never** (skipped)                |
