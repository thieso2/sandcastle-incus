# Per-Tenant `/.sc` Shared-Scripts Volume: Two Layers, Stable Shims, Central Updates

> Status: **accepted** (2026-07-17). Spec: issue #127 (tickets #128–#132). Builds on ADR-0016 (v2 native-Incus topology, shared home/workspace volumes). Complements ADR-0005/0011 (secrets and signing stay in the Sidecar/Auth App — never on `/.sc`).

## Context

Every platform script a machine needs — the SSH agent-forwarding indirection today; token helpers, Caddy setup, generalize over time — was baked into each machine via the default profile's cloud-init. cloud-init runs only at first boot, so changing a platform script meant touching every existing machine individually (`sc fix`, `scripts/fix-agent-forwarding.sh`): a per-machine fleet treadmill for every one-line change. Tenants also had no central place to put their own fleet-wide shell setup.

## Decisions

1. **Two shared volumes per v2 app project, mounted on every machine.** `sc-platform` → `/.sc/platform` and `sc-local` → `/.sc/local`, created with the same machinery as the shared `home`/`workspace` volumes (`ensureV2SharedVolume`, `security.shifted` for CT+VM idmap parity) and attached through the project default profile as disk devices. The volume set and device descriptors are plan data (`CreatePlanV2.SCVolumes`), pure-testable.

2. **Writability is the external contract, enforced at the disk device.** The platform device carries `readonly=true`: machines — container or VM — mount `/.sc/platform` read-only, so a tenant cannot accidentally delete a script the fleet depends on. `/.sc/local` is tenant-writable from any machine (UID-2000-owned, like `/workspace`) and shared, so an edit on one machine is visible on all and survives machine re-creation.

3. **Machines carry only stable baked shims.** `V2DefaultProfileUserData` bakes thin shims at the fixed OS paths (`/etc/ssh/sshrc`, appended blocks in `/etc/zsh/zshrc` and `/etc/bash.bashrc`). Each shim sources `/.sc/platform/<x>` then `/.sc/local/<x>`, each `[ -r … ] &&`-guarded: a missing or broken payload is a clean no-op, never a shell/SSH lockout. The shims are content-stable across payload changes — the payload is what evolves.

4. **The platform payload is versioned, built into the binary, and written centrally.** `tenant.PlatformPayload()` returns the file set plus a content-derived version; a `VERSION` marker inside `/.sc/platform` makes the running version reportable and drift detectable. The payload is written over the Incus storage-volume-file API once per project (not per machine) — at tenant provisioning, at project creation, and on demand via `sc-adm tenant payload-sync` (admin) or `sc payload-sync` (tenant self-service: the restricted certificate may write the volume inside its own projects, so no install prefix or admin remote is needed). Rollback is running the previous binary's sync (the payload is derived from the binary). **Shim ↔ payload contract:** every `/.sc/platform` path a shim sources must be produced by the payload builder — enforced by a unit test.

5. **Trust-boundary invariant (architectural).** Platform, host, and Sidecar code must **never** source or execute tenant-writable `/.sc` content; only the tenant's own machines do — the tenant already has root there, so `/.sc` never crosses the tenant boundary. Secrets (Machine Runtime Secret, workload identity, TLS keys, any GitHub token) are never stored on `/.sc`; they stay in the Sidecar/Auth App and are delivered per-machine (`CreateInstanceFile`) or over the network. `/.sc` may hold the *code that reads* a secret, never the secret.

6. **`incus exec` is the recovery floor.** Admin reach into any machine is independent of `/.sc`, shells, and SSH, so a bad payload is a convenience-off, never a lockout. Combined with the guarded shims and versioned rollback, this is what makes the deliberately flipped blast radius (a payload update hits all of a tenant's machines at once) acceptable.

## Scope notes

- **Per-tenant contract, per-project realization.** For the default single-project tenant the per-project volumes *are* per-tenant. For multi-project tenants the platform layer stays converged because the central sync writes every app project of the tenant in one pass; the local layer is per-project today (accepted; the spec leaves the multi-project transport open).
- The Sidecar is the design-level owner of the canonical payload; operationally the sync runs wherever the admin binary runs (auth-app provisioning, `sc-adm`), because app-project volumes are reachable only through the Incus API, which the Sidecar deliberately has no credentials for.

## Consequences

- A platform-script change propagates by one central write per tenant; the `sc fix` treadmill for script changes is retired (`sc fix` becomes the one-time shim+payload bootstrap for legacy machines).
- Tenants get first-class fleet-wide customization (`/.sc/local`) with fail-safe semantics.
- Tenant delete must enumerate the two extra volumes (`DeletePlanV2.DurableVolumes`), like home/workspace.
