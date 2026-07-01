# Sandcastle v2 — Implementation Plan

> Sequenced rollout of the v2 topology (ADR-0011/0012/0013/0014/0015, `v2-topology.md`). **v2 ships as a parallel deployment beside v1** (ADR-0015) — not a flag inside v1's running deployment. The v2 code is built incrementally (phases below) and tested on a throwaway parallel deployment, then stood up beside v1 for real; users migrate one at a time (Phase 8); v1 is retired last. Commits are kept tiny per the repo's convention.

## Phase P — Prerequisite: make the infra deployment-scoped (blocker for coexistence, ADR-0015)
Two host-global singletons are hardcoded and prevent a second deployment from installing beside v1. This must land **first** (it's also useful for v1 alone — enables N deployments per host):
- **Host front door:** make the infra Caddy's proxy listen address/ports seed-configurable (`internal/infra/plan.go:648-654` `tcp:0.0.0.0:80/443`) — v2 on a second host IP or `:8080/:8443` (or, longer-term, a shared SNI router owning `:443`).
- **Infra bridge + octets:** make `InfrastructureNetworkName` (`plan.go:41` `incusbr0`) and the sidecar last-octets (`infrastructure.go:206-211` `.20/.21/.22`) seed-configurable, or give each deployment its own infra bridge.
- *Commits:* (1) seed fields for front-door listen + infra bridge/octets, (2) thread through `PlanCreate`/`ApplyStaticNetwork`/`infrastructureStaticAddresses`, (3) `infra create` a 2nd deployment on distinct prefix/CIDR/ports on one host (e2e).

## Phase 0 — Scaffolding (additive, no behavior change)
- Add v2 naming helpers in `internal/naming` **alongside** v1: `UserInfraProjectName(user) = sc2-<user>`, `UserProjectName(user, project) = sc2-<user>-<project>`, `UserBridgeName(user) = sc2-<user>` (v2 uses a distinct project prefix so it coexists with v1's `sc-*`). Keep `TenantIncusProjectName` etc. intact.
- No in-binary `v2` flag — the v2 build *is* v2; it's the deployment (distinct seed/prefix) that makes it parallel to v1.
- *Commits:* (1) naming helpers + tests, (2) v2 seed template (prefix `sc2`, non-overlapping CIDR, own auth host/OAuth/DB).

## Phase 1 — Per-user infra project + consolidated sidecar
- Build `sc-<user>` infra project creation and the single sidecar (CoreDNS + private Caddy + Tailscale subnet-router) in `internal/incusx` (new file, don't touch `tenant_create.go` yet).
- Bridge `sc-<user>` in the `default` Incus project; `features.networks=false` wiring.
- *Commits:* (1) infra-project create, (2) sidecar image/launch, (3) sidecar config (Corefile + Caddy private + tailscale up), (4) e2e (gated) that pings the sidecar.

## Phase 2 — Project = Incus project + project profile
- `sc project create/delete` creates/deletes a real `sc-<user>-<project>` Incus project (`features.networks=false` → shared bridge), each with a `default` profile bundling the shared `/home` + `/workspace` volumes + login (user/SSH/idmap). Reuse the seed-volume + uid-pin pattern proven on the `thieso` profile.
- *Commits:* (1) project create → Incus project, (2) shared-volume pair + seed, (3) project `default` profile, (4) project delete + guards, (5) e2e: `launch -p default` in a fresh project gets home/workspace + SSH.

## Phase 3 — DNS `<machine>.<project>`
- `internal/dns`: one CoreDNS zone **per project**; hostname `<machine>.<project>` (drop tenant label); build records by scanning `sc-<user>-*` Incus projects instead of one project; stop reading `user.sandcastle.project`.
- Tailscale split-DNS: register each project domain → per-user sidecar.
- *Commits:* (1) render one zone/project, (2) multi-project scan in `apply.go`, (3) split-DNS registration, (4) render_test updates, (5) e2e: `ping web.acme`.

## Phase 4 — Machine lifecycle on v2
- `sc create/connect/list/delete` use v2 names + `<machine>.<project>` hostnames; drop `{project}-{machine}`; machines land in `sc-<user>-<project>` on the shared bridge.
- *Commits:* per subcommand (create, connect, list, delete, status), each with tests.

## Phase 5 — Auth App re-scope (tenant → user)
- Provision per-user infra + projects on device login; OIDC issuer keyed **per user**; workload token claims `user/project/machine`.
- *Commits:* (1) provisioner → per-user infra, (2) OIDC key per user + migration of key storage, (3) workload claims, (4) machines-web + tenants API on v2.

## Phase 6 — Route Broker re-scope
- Principal = User (restricted cert per user); route target `sc-<user>-<project>/machine`; shared Caddy proxies to the machine on the per-user bridge (P1, unchanged path).
- *Commits:* (1) principal=user, (2) target identity, (3) authorize project ownership, (4) e2e public route to a v2 machine.

## Phase 7 — CLI/config polish
- `sc config`, `sc status`, `sc dns`, `sc trust`, `sc tailscale` on v2 vocabulary; help text; `docs/usage.html` update (same-commit doc rule).

## Phase 8 — Migration v1 → v2  (ADR-0014: preserve volumes, recreate everything else)
Per user, one at a time (v1 stays until v2 verified):
1. **Quiesce** — stop the user's v1 machines so `sc-home`/`sc-workspace` are quiescent. Mark the volumes retained (never deleted by v1 teardown).
2. **Stand up v2 infra** — create `sc-<user>` infra project + the consolidated sidecar; create the shared `sc-<user>` bridge; per-tenant CA carries over (personal tenant), tailnet swaps the two old sidecars for one.
3. **Recreate projects** — for each v1 Sandcastle project, create `sc-<user>-<project>` with a `default` profile that mounts the **preserved volumes' subdirs** (`sc-home/<project>`→`/home/<user>`, `sc-workspace/<project>`→`/workspace`), UID-reseeded to 2000.
4. **Recreate machines** — relaunch each machine into its `sc-<user>-<project>` (rootfs disposable; state is on the mounted volume subdir). Re-issue leaf certs for the new `<machine>.<project>` SANs.
5. **Cutover** — regenerate the user's local DNS + `known_hosts` (hostnames drop `.tenant`); re-point public routes to `sc-<user>-<project>/machine`.
6. **Verify, then tear down v1** — confirm SSH + DNS + routes on v2; delete the v1 tenant projects (volumes already preserved/reused).
- *Commits (R1 — runbook):* (1) `retain-volumes` guard on v1 teardown, (2) a documented runbook + helper (`sc-adm user migrate` optional under R2), (3) cutover helper (regenerate DNS/known_hosts), (4) e2e: migrate a throwaway tenant, assert home/workspace data survives.
- *Open:* R1 vs R2 driver; hostname break vs `.tenant` grace-period alias (ADR-0014).

## Phase 9 — Cleanup / ratify
- Remove v1 code paths + the feature flag; delete `{project}-{machine}` and per-tenant sidecar code; **fold `docs/v2-glossary.md` into `CONTEXT.md`** and retire superseded terms; mark ADR-0001/0006/0007 superseded.

## Test strategy
- Unit tests per phase (as today). Gated integration/e2e (`SANDCASTLE_INCUS_INTEGRATION=1`, `SANDCASTLE_INCUS_E2E=1`) exercise each phase end-to-end on real Incus. `make e2e-safe` in CI. v2 phases are validated on a **throwaway parallel deployment** (distinct `sc2` prefix / CIDR / ports), so v1 is never touched during development.

## Risks / dependencies
- **Migration** is the highest-risk phase and is unspecified pending the grilling.
- **Instance move across Incus projects** — Incus can't rename-across-project cheaply; migration likely means recreate + volume re-attach.
- **Hostname change** (`.tenant` dropped) breaks existing SSH/known_hosts/routes — needs a cutover step.
- **Multi-host** — P1 public routing assumes a single Incus host (ADR-0013).
</content>
