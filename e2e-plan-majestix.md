# Full sc2 E2E run on `majestix` (fresh VM, everything in `docs/e2e-sc2.md`)

## Context

`env.e2e` provides a fresh test VM (`ssh -p 7001 -i ~/majestix_ed25519 sc@big.thieso2.dev` → **majestix**: Debian 13, 4 vCPU, 15 GiB RAM, 124 GB disk, `/dev/kvm` present, passwordless sudo, no Incus), a Tailscale auth key, and a Cloudflare API token. Goal: execute the **entire** e2e protocol from `docs/e2e-sc2.md` on this VM — unattended (simulated GitHub, no browser click), with our own Cloudflare-tunnel ingress endpoints — and leave the stack **running** at the end.

## Topology (validated shape: "nested full-stack" appendix + two-VM client rules)

- **majestix = the Incus host + sc server.** Install latest Incus (Zabbly) directly on it; `sc-adm install` puts the auth-app appliance (caddy + cloudflared inside) on it as Incus containers. Tunnel installs deploy **no broker appliance** and bind no host port.
- **Client = a nested VM `e2eclient`** on majestix's Incus (KVM available), a **genuine tailnet node** (`tailscale up --accept-routes`) — its only path to tenant machines is the sidecar's approved subnet route, exactly as the two-VM protocol requires. All client functionality (sc login, sc create/list/connect, DNS via systemd-resolved link scopes) is tested from inside it.
- **Tenant machines** (CTs + one VM for the shared-home battery) run on majestix's Incus at L2 — KVM works.
- **Two installs on one daemon** for the coexistence battery: prefix `sc` (default) + prefix `id`, each with its own first-level hostname/tunnel, CIDR pool, and Tenant DNS Suffix.

Key parameters:
- Hostnames (first-level on the CF zone — Universal SSL limitation): `majestix-<rand>.thieso2.dev` (install A) and `majestix2-<rand>.thieso2.dev` (install B).
- CIDR pools clear of majestix's net (10.200.0.0/24), incus bridges, and every known live deployment on the tailnet (10.248–10.254 are in use by big/obelix/testzone/tc2): **A = `10.61.0.0/16`**, **B = `10.62.0.0/16`**.
- `SIMULATE_TOKEN` freshly generated per run; users `e2edns` (install A) and same user again on install B (shared-identity battery); suffixes `castle` (A) / `idefix` (B).
- Fresh static build from current main: `make build` with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 BIN_DIR=bin/linux-amd64` (don't trust the possibly stale `bin/linux-amd64/sandcastle`).

## Run logging (required by the doc)

`logs/majestix.log` + `logs/e2eclient.log` in the run's working dir via the doc's `run()` wrapper — every remote command + full output, no side-channel commands. Client commands go through `incus exec`/ssh from majestix but are logged to the client's log.

## Phases

**P0 — Preflight (fail fast on the credentials)**
0. **`git pull` latest main first** (working tree only has the untracked `env.e2e` / deleted `.env.default` — no conflict expected); build and run everything from the freshly pulled tree.
1. Build the fat binary from main; `go test ./...` must pass first.
2. Verify the CF token can touch zone `thieso2.dev` (API: zone read, tunnel create perms) — read-only checks.
3. Inspect the Tailscale auth key shape (reusable? tagged?). It must join **3+ nodes** (e2eclient, sidecar A, sidecar B) and the sidecars advertise `tag:sandcastle`; the tailnet needs the `autoApprovers` rule for zero-touch route approval (prior runs on this tailnet were unattended, so it's likely in place). **If the first login's routing check stalls on route approval, that's the one human-in-the-loop fallback** — surface it immediately rather than spinning.

**P1 — Host prep (majestix)**
- scp binary; install as `/usr/local/bin/sandcastle` + `sc`/`sc-adm`/`sandcastle-admin` symlinks.
- `sudo sc-adm install-incus` (Zabbly latest, minimal init). PASS: `incus --version` ≥ 6.x-zabbly current.

**P2 — Install A (one-command, tunnel ingress, simulated auth)**
```
sc-adm install --auth-hostname $HOST_A --simulate-github-token $SIMULATE_TOKEN \
  --admin-github-users thieso2 --ingress cloudflare \
  --cloudflare-api-token $CF_TOKEN --cidr-pool 10.61.0.0/16
```
PASS: `sandcastle-auth-app`, `caddy`, `cloudflared` active in `sc2-auth-app`; tunnel Healthy; `curl https://$HOST_A/healthz` → 200; simulate endpoint 200 with token / 403 wrong token; nothing binds host :9443.

**P3 — Client VM**
- `incus launch images:debian/13/cloud e2eclient --vm` (on majestix); push binary; `apt-get install incus-client`; install tailscale; `tailscale up --auth-key … --accept-routes`.

**P4 — Login + tenant provisioning (install A)**
- `sc login https://$HOST_A --simulate-token … --as e2edns --tailscale-auth-key … --dns-suffix castle`.
- PASS: all four layered routing checks ✓; suffix stored (`incus project get sc2-e2edns user.sandcastle.v2.suffix` = castle); profile has both `home`+`workspace` devices, volumes `security.shifted=true`; sidecar CoreDNS active; first-pass provisioning (no resolv.conf regression).

**P5 — Machines + projects battery (doc steps 2/2b)**
- `sc create web --detach`; `sc project create backend` (flagless broker path); `sc create backend:api --detach`; `sc create vm1 --vm --detach`; `sc list` shows all immediately.
- Shared `$HOME` + `/workspace` CT↔VM read/write both directions; login user writes /workspace; authorized_keys on shared /home.

**P6 — ADR-0018 DNS battery (doc step 3 + client resolver)**
- At sidecar CoreDNS (tenant .3): canonical `web.default.castle`, short `web.castle`, wildcard `app1.web.default.castle`, second project `api.backend.castle`, and `api.castle` → NXDOMAIN.
- Guest identity: `hostname -f` = `web.default.castle`; guest resolver `getent hosts` cross-project.
- Client: `getent hosts web.default.castle` resolves via the per-suffix link scope (`sandcastle-dns-castle.service`), survives `systemctl restart systemd-resolved`.

**P7 — Lifecycle + error UX + suffix immutability (doc steps 4/5)**
- `sc c web -- hostname` (ensure/start/ssh); stop→`sc c` restarts; `sc delete backend:api --yes`; `sc create nosuch:box` error text; `sc delete api:backend --yes` swapped-ref hint.
- Re-login with no suffix → stays `castle`, idempotent (`Already logged in` on plain re-run); `sc login --force --dns-suffix other` → **fast deny** with immutable-suffix message (no 10-min poll).
- **Service logging + Activity Log (doc step 6, new on main)**: `journalctl -u sandcastle-auth-app` shows `request POST /api/device/poll status=200 dur=…` lines and `span provision.personal_tenant dur=… user=…` spans; auth-app `/logs` as non-admin `e2edns` (session via the simulate endpoint, cookie jar + curl) shows ONLY that user's rows; `/logs` as admin `thieso2` shows all users' + system rows, `?q=` filters, and a non-admin can't reach another user's rows via `?q=`.

**P8 — Install B + multi-install coexistence (prefix `id`)**
- Second `sc-adm install --prefix id --auth-hostname $HOST_B --cidr-pool 10.62.0.0/16 …` (same simulate token or a second one).
- Same user logs in from the same e2eclient: `sc login https://$HOST_B --simulate-token … --as e2edns --dns-suffix idefix` (trusted-client cert fallback path, `--project` pin, no interactive prompt).
- PASS battery: distinct bridges `sc2-net`/`id-net`; fresh /24 from B's own pool; `sc-id-e2edns` remote; `VERBOSE=1 sc incus ls` under each remote shows only that install's machines (create `api` in B); `sc list`/`sc status` scoped the same; both resolver zones resolve **simultaneously**, cross-zone NXDOMAIN both ways.

**P9 — Admin plane + v2 teardown check**
- `scripts/e2e-v2.sh` on majestix (refuses without auth-app — run once stack is up).
- `sc-adm tenant delete` **without** `--purge` on a v2 tenant → refused; then create a throwaway tenant (second simulated user `e2edel`), `--purge` it, verify projects/bridge gone and install B's same-named state untouched.

**P10 — Public HTTPS expose (Phase 7b, tunnel-mode adaptation — stretch)**
- Doc's 7b assumes a public-IP sc-edge; majestix is tunnel-only. Adaptation: app on `:3000` in a tenant machine, add a first-level hostname `e2eweb-<rand>.thieso2.dev` route via the install's cloudflared + a Caddyfile vhost → machine IP:3000; `curl` → 200 over a real edge cert. If the tunnel-config plumbing fights back, record as documented-limitation and move on (7b is marked public-IP-shape in the doc).
- Phase 8b (tenant-CA trust) stays ⚠ skipped — feature not built (per doc).

**P11 — Report + end state**
- **Leave everything running** (both installs, e2eclient, tenants); document teardown commands (tenant purge ×2, `incus delete -f e2eclient`, CF tunnel+DNS delete via API, tailnet device cleanup) in the final report.
- Append any newly-hit problems + fixes to the doc's appendix table and `implementation-notes.md`; update `docs/e2e-sc2.md` status markers if a step's reality diverges. Update memory with the majestix deployment facts.

## Known risks / fallbacks

| Risk | Handling |
|---|---|
| Tailnet route not auto-approved (no `autoApprovers` for 10.61/10.62, no TS API key provided) | Detect at P4 routing check; report immediately and ask the operator to approve in the TS console (only human step possible) |
| Auth key single-use or lacks `tag:sandcastle` | Detected at P3/P4 join; surface with the exact key requirement |
| CF token lacks tunnel/DNS perms | P0 read-only probe fails fast |
| Stale same-/24 sidecar devices on the tailnet blackholing routes | Pools 10.61/10.62 chosen clear of all known deployments; verify `Self.PrimaryRoutes` after provisioning |
| Mid-run binary swap vs immutable suffix | Fresh `--as` user per re-attempt (doc gotcha) |

## Verification

The run **is** the verification: every phase above carries the doc's PASS criterion, checked in-band and captured in `logs/*.log`. Final deliverable: a phase-by-phase PASS/FAIL table, the live endpoints (`$HOST_A`, `$HOST_B`), leftover-state inventory, and doc/notes updates committed in the same change if any code or doc fixes were needed.
