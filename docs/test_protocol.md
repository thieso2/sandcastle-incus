# Sandcastle E2E Test Protocol

Concrete verification checklist for the full e2e suite. Each item records **what to assert** and **why** — the bug class that would be missed without it.

The assumed environment is a Linux VM with direct Incus access, reachable private IPs, a Tailscale auth key, and the auth app running with `--debug-device-user`. No human interaction. No `--skip-setup`.

---

## 1. Infrastructure

**Verify after `sc-adm infra create`:**

- [ ] Incus project `<infra-project>` exists
- [ ] Instance `sc-caddy` exists in `<infra-project>` and is running
- [ ] Instance `sc-route-broker` exists in `<infra-project>` and is running
- [ ] Instance `sc-auth-app` exists in `<infra-project>` and is running *(if auth app is deployed)*
- [ ] Route broker responds 401 to a request without a client certificate (mTLS enforced)
- [ ] Route broker responds 200 to a request with an authorized Incus client certificate

**Verify after `sc-adm infra delete`:**

- [ ] Incus project `<infra-project>` no longer exists
- [ ] All three instances are gone

**Why:** Permanent infrastructure (`sc-infra`) blocks a second `sc-caddy` on the same bridge. Disposable infra tests must skip or run on a clean server. The skip guard checks `sc-caddy` existence before creating.

---

## 2. Tenant Lifecycle

**Verify after `sc-adm tenant create <tenant>`:**

- [ ] Main project `sc-<tenant>` exists with `features.networks=true`
- [ ] Infra project `sc-<tenant>-infra` exists with `features.images=true`
- [ ] Native project `sc-<tenant>-native` exists
- [ ] Bridge network `<PrivateNetworkName(sc-<tenant>)>` exists in main project
- [ ] Storage pool `sc-<tenant>` exists
- [ ] Volumes `sc-home`, `sc-workspace`, `sc-ca` exist in the storage pool
- [ ] Tailscale sidecar `sc-<tenant>` exists in **infra project** (not main project) and is running
- [ ] DNS sidecar `sc-dns` exists in **infra project** and is running
- [ ] Sidecar `eth0` NIC: `nictype=bridged`, `parent=<bridge-name>` (not `network=`)
- [ ] Sidecar `eth0` NIC: no `ipv4.address` key (invalid on unmanaged bridge)
- [ ] Tailscale sidecar has a private IP from the tenant CIDR (`.2`)
- [ ] DNS sidecar has a private IP from the tenant CIDR (`.3`)
- [ ] `sc tailscale status` exits 0 — verifies the exec targets the **infra project** *(regression: previously targeted main project → "Instance not found")*

**Verify after `sc-adm tenant delete --purge <tenant>`:**

- [ ] All three projects are gone
- [ ] Storage pool and volumes are gone
- [ ] Bridge network is gone

**Why:** The 3-project split (main / infra / native) is the core invariant. Sidecars in the wrong project cause every subsequent operation to fail.

---

## 3. Login Flow

**Verify `sc login --debug-approve <auth-host>` (no `--skip-setup`):**

- [ ] Exit code 0
- [ ] Output contains "Personal tenant … is ready."
- [ ] Output contains "Approved as …"
- [ ] Output contains "Remote … enrolled."
- [ ] Incus remote `sandcastle-<user>` exists in `~/.config/sandcastle/<remote>/incus/`
- [ ] Main project, infra project, and native project exist (same as tenant lifecycle checks)
- [ ] Both sidecars running in infra project
- [ ] `sc tailscale status` exits 0 — proves sidecar is reachable in infra project *(regression: RunUp/RunStatus used main project → "Instance not found")*
- [ ] Local DNS resolver file for tenant exists (`/etc/resolver/<tenant>` on Darwin, systemd-resolved drop-in on Linux)
- [ ] Tenant CA is installed in local trust store

**Verify re-login (tenant already exists):**

- [ ] `EnsureAuxProjects` runs without error even if sidecars are already running
- [ ] Sidecars remain running (no double-start error) *(regression: Start:true + explicit start → "already running")*
- [ ] Certificate re-enrolled, existing Incus state preserved

**Why:** `--skip-setup` bypasses DNS, trust, and Tailscale steps. Running without it is the only way to catch wrong-project bugs in the setup path. The `sc tailscale status` call catches project lookup regressions without requiring a real Tailscale auth key.

---

## 4. Machine Create (detached)

**Verify `sc create <project>/<machine> --detach --template base`:**

- [ ] Exit code 0
- [ ] Instance `<project>-<machine>` exists in main project and is running
- [ ] `instance.Devices["home"]["source"]` = `sc-home/<home-dir>`
- [ ] `instance.Devices["home"]["path"]` = `/home/<unix-user>`
- [ ] `instance.Devices["workspace"]["source"]` = `sc-workspace/<workspace-dir>`
- [ ] Linux user `<unix-user>` exists in instance (`id -un <unix-user>` via Incus exec)
- [ ] Caddy ingress files written: cert, key, Caddyfile
- [ ] `sc status <ref>` exits 0 and JSON contains `instanceName`, `privateIP`, `appPort`, `linuxUser`, `running=true`
- [ ] `sc status` `containerTools` field matches `--container-tools` flag
- [ ] If `SSH_PUBLIC_KEY` set: key present in `/home/<unix-user>/.ssh/authorized_keys`

**Why:** The create-detach path exercises machine provisioning without SSH. Caddy ingress, Unix user creation, and home/workspace mounts must all be present.

---

## 5. Machine Connect (SSH)

*Requires direct access to machine private IPs (`SANDCASTLE_E2E_LOCAL_VM=1`).*

**Verify `sc create <machine> --template base` (interactive, stdin-driven):**

- [ ] Exit code 0
- [ ] Shell interactive output contains the sentinel (`sandcastle-create-interactive-ok`)
- [ ] Shell output contains the Linux username (`whoami`)
- [ ] Instance exists and is running after exit

**Verify `sc connect <machine> pwd`:**

- [ ] Exit code 0
- [ ] Command executes without SSH host key scan error *(regression: missing known-hosts cache)*

**Why:** SSH to private IPs only works from within the Incus bridge. Gate with `SANDCASTLE_E2E_LOCAL_VM=1`. Running from inside a VM unblocks this gate.

---

## 6. Machine Lifecycle (start / stop / delete)

**Verify full lifecycle sequence:**

- [ ] Create machine → running
- [ ] `sc stop <machine>` → stopped, exit 0
- [ ] `sc start <machine>` → running, exit 0
- [ ] `sc connect <machine> id -un <user>` → prints unix user, exit 0 *(via Incus exec, not SSH)*
- [ ] `sc delete <machine>` → instance gone, exit 0

**Why:** Stop/start/delete are independent code paths; each can break silently if only create is tested.

---

## 7. Tenant Storage Shares

*Requires `SANDCASTLE_E2E=1`, `SANDCASTLE_E2E_LOCAL_VM=1`, and imported base/AI image aliases.*

**Verify cross-tenant read-only sharing:**

- [ ] Create two disposable Tenants.
- [ ] Create a source Machine with project-scoped `/workspace`.
- [ ] Create `/workspace/docs` in the source Machine and write a marker file.
- [ ] Create an outbound Tenant Storage Share for `project:/workspace/docs`.
- [ ] Accept the offer for the recipient Tenant.
- [ ] Reconcile recipient shares.
- [ ] Create or reconcile a recipient Machine and verify the share appears at `/shared/<source-tenant>/<source-project>/docs`.
- [ ] Verify the recipient can read the marker file.
- [ ] Write a second marker from the source Tenant and verify the recipient sees it.
- [ ] Verify recipient writes under `/shared/...` fail.
- [ ] Revoke or delete the share, reconcile, and verify the recipient mount is removed.
- [ ] Cleanup leaves no disposable share state, Machines, or Tenants unless `SANDCASTLE_E2E_KEEP=1`.

Run:

```sh
SANDCASTLE_E2E=1 SANDCASTLE_E2E_LOCAL_VM=1 go test ./internal/e2e -run TestTenantStorageShareReadOnlyE2E -count=1 -v
```

**Why:** This catches cross-project disk-device regressions, missing read-only flags, stale recipient metadata, and cleanup paths that unit tests cannot prove against real Incus.

---

## 8. DNS

**Verify CoreDNS after `sc-adm dns apply`:**

- [ ] DNS sidecar is running and has its private IP configured
- [ ] Direct UDP query to `<dns-ip>:53` for `<machine>.<project>.<tenant-suffix>` returns the machine's private IP
- [ ] Per-machine wildcard `test.<machine>.<project>.<tenant-suffix>` resolves
- [ ] A removed machine's records disappear after DNS reapply

**Verify local OS resolver:**

- [ ] OS resolver is configured to use tenant DNS sidecar
- [ ] `host <machine>.<project>.<tenant-suffix>` resolves correctly via OS resolver

**Why:** CoreDNS exec happens in the infra project. DNS apply must target infra project, not main project. *(Regression: `DNSManager.Apply` used main project for infra project lookup.)*

---

## 9. Tailscale

*Requires `SANDCASTLE_E2E_TAILSCALE_AUTHKEY`.*

**Verify `sc tailscale up`:**

- [ ] Exec targets the **infra project** (sidecar lives there, not in main project) *(regression)*
- [ ] `sc tailscale status` shows `state=Running`
- [ ] Tailscale IP assigned to sidecar
- [ ] Tenant CIDR advertised as a Tailscale route
- [ ] DNS query through Tailscale-routed private IP resolves machine hostname
- [ ] HTTPS to machine hostname over Tailscale-routed IP returns expected response

**Verify `sc tailscale down`:**

- [ ] Exec targets infra project *(same regression class as up)*
- [ ] Sidecar disconnects from tailnet

**Why:** RunUp/RunStatus/RunDown all used `plan.Tenant.IncusName` (main project) instead of `plan.Tenant.InfraProject`. Fixed, but regressions are easy to reintroduce.

---

## 10. Sidecar NIC Invariants

These are low-level structural checks that catch the class of errors seen during the 3-project migration. Assert on the live Incus instance config after tenant creation:

- [ ] `eth0.type = nic`
- [ ] `eth0.nictype = bridged`
- [ ] `eth0.parent = <PrivateNetworkName(incusProject)>` *(not a hash collision, not the project name when > 15 chars)*
- [ ] `eth0` has no `network` key *(Incus rejects `network:` + `ipv4.address` on unmanaged bridge; also `network:` in infra project resolves against default project network namespace → "Network not found")*
- [ ] `eth0` has no `ipv4.address` key *(invalid on unmanaged bridge)*

**Why:** Three iterations of NIC config were tried: (1) `network:` — wrong namespace; (2) `nictype:bridged + ipv4.address` — rejected; (3) `nictype:bridged, parent:` — correct. A future refactor could regress to either broken form.

---

## 11. Idempotency

Each creation operation (`CreateTenant`, `EnsureAuxProjects`, `ensureSidecar`) must be idempotent:

- [ ] Running `sc-adm tenant create` twice does not fail
- [ ] Running `sc login` twice does not fail (no double-start, no cert conflict)
- [ ] `ensureSidecar` on a running sidecar: no action, no error
- [ ] `ensureSidecar` on a stopped sidecar: starts it, no error

**Why:** Login is expected to be run repeatedly. `EnsureAuxProjects` is the repair path. Both must be safe to call on existing state.

---

## 12. Auth App End-to-End

**Verify with `--debug-approve`:**

- [ ] `/device/start` returns `user_code` and `verification_uri`
- [ ] `POST /debug/device/approve` with `user_code` auto-approves as `debugDeviceUser`
- [ ] Poll returns `status=approved`, tenant name, project list, enrollment status
- [ ] `DebugApprove` client method is exercised (not just `openBrowser`) *(regression: DebugApprove method missing → compile error)*

**Why:** The auth app login flow has three actors: client, server, GitHub OAuth. `--debug-approve` short-circuits GitHub but still exercises the full device token lifecycle.

---

## Full Unattended Run Checklist

When running from a VM with full Incus access, the following env vars unlock all gates:

```
SANDCASTLE_E2E=1
SANDCASTLE_E2E_REMOTE=local
SANDCASTLE_E2E_LOCAL_VM=1
SANDCASTLE_E2E_STORAGE_POOL=default
SANDCASTLE_E2E_CIDR_POOL=10.248.0.0/16
SANDCASTLE_E2E_TAILSCALE_AUTHKEY=tskey-auth-...
SANDCASTLE_E2E_TAILSCALE_TAG=tag:sandcastle
SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:latest
SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:latest
SANDCASTLE_E2E_SSH_PUBLIC_KEY="$(cat ~/.ssh/id_ed25519.pub)"
SANDCASTLE_E2E_AUTH_HOST=https://<auth-app-host>
```

Expected result: all tests **PASS or SKIP** (SKIP only for image-build and public-route tiers that need extra credentials), zero **FAIL**.

Tests that should pass with the above env (no skips):

| Test | Gate removed by |
|------|----------------|
| `TestCLIConnectCommandE2E` | `LOCAL_VM=1` |
| `TestCLICreateDetachE2E` | `LOCAL_VM=1` |
| `TestCLICreateDefaultConnectE2E` | `LOCAL_VM=1` |
| `TestCLILoginE2E` | `AUTH_HOST` set |
| `TestTailscaleAttachmentE2E` | `TAILSCALE_AUTHKEY` set |
| `TestHostOverrideHostsFileE2E` | `LOCAL_VM=1` |
| `TestLocalTrustPlatformInstallUninstallE2E` | `LOCAL_VM=1` |
| `TestDisposableInfrastructureCreateAndDelete` | run on clean server (no permanent `sc-caddy`) |
| `TestRouteBrokerAuthorizedMutationE2E` | run on clean server |

Tests that remain skipped regardless (require additional config):

| Test | Requires |
|------|---------|
| `TestImageBuildBaseE2E` | `SANDCASTLE_E2E_IMAGE_BUILD=1` |
| `TestImageBuildAIE2E` | `SANDCASTLE_E2E_IMAGE_BUILD=1` |
| `TestTailscaleAttachmentE2E` | `SANDCASTLE_E2E_TAILSCALE_AUTHKEY` |
