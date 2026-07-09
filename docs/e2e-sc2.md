# sc2 End-to-End Test Protocol

A re-executable runbook that validates the full sc2 (v2) feature set: appliance
deploy → tenant provisioning (stock Debian sidecar) → client enrollment → machine
+ DNS → per-tenant HTTPS. Every step lists the command and the **PASS** criterion.

Run it top-to-bottom. **Phase 0 (teardown)** makes it idempotent — re-running
starts from a clean slate.

## Status legend
- ✅ **validated** live on `big` (2026-07-02)
- ⚠️ **partial** — works but has a known rough edge (noted inline)
- 🚧 **to build** — feature not implemented yet; step documents the target

## Prerequisites
- Run from a host with the **admin** Incus remote: `export INCUS_CONF=~/.config/incus-admin` (remote `big`).
- The one **fat binary** built static for linux: `bin/linux-amd64/sandcastle` (`make build` with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 BIN_DIR=bin/linux-amd64`). It is copied into every appliance.
- **Stock images, no `sandcastle/base`.** Everything (auth-app, broker, tenant sidecars) runs on the stock upstream image `images:debian/13`, **pulled on demand from the public `images:` remote** — no custom image build and no manual `incus image copy` pre-caching required. (The appliance/sidecar launch code resolves `images:…`/`ubuntu:…` refs to a simplestreams pull; a bare alias or fingerprint still means a local image.) The Incus **server** just needs outbound access to `images.linuxcontainers.org`.
- **Host network listener — now automatic.** Provisioning turns the daemon's network listener on itself: `CreateToken` calls `ensureServerListening`, which sets `core.https_address=:8443` if unset (a fresh `incus admin init --minimal` host only has the unix socket). No manual `incus config set core.https_address :8443` is needed anymore; before this fix, provisioning failed at token issuance with `Can't issue token when server isn't listening on network`.
- **Client needs the `incus` CLI.** `sc login` shells out to `incus remote add` to enrol the tenant remote, so the client machine must have the Incus client installed (`apt-get install -y incus-client` on Debian/Ubuntu; `incus-client` is the lightweight client-only package). Without it, login provisions the tenant but fails at enrollment with `incus remote add: exec: "incus": executable file not found in $PATH`.
- **GitHub auth — two modes:**
  - **Simulated (no OAuth app, recommended for e2e):** deploy the auth-app with `--simulate-github-token <secret>`; log in with `sc login <auth-host> --simulate-token <secret> --as <username>`. No `GH_CLIENT_ID`/`GH_CLIENT_SECRET`, no browser, no network to GitHub. **Dev/e2e only.**
  - **Real OAuth app:** `.env.sc2` with `GH_CLIENT_ID`, `GH_CLIENT_SECRET`; the OAuth **callback URL is `https://<auth-host>/oauth/github/callback`** (note `/oauth/…`, not `/login/…`).
- `.env.sc2` at repo root with: `PUBIC_URL=sc2.thieso2.dev`, `TAILSCALE_AUTH_KEY`, and (real-OAuth only) `GH_CLIENT_ID`/`GH_CLIENT_SECRET`. (An optional `TAILSCALE_API_KEY` is useful only for the harness's own tenant-side route-approval scripting — the product has no API-key flag.)
- **Tenant machine reachability (per-tenant tailnet, ADR-0017).** The sidecar advertises the tenant `/24` as a subnet route; a client reaches tenant machines only once that route is **approved** and the client **accepts routes**. Every sidecar joins the tailnet tagged **`tag:sandcastle`** (`tailscale up --advertise-tags=tag:sandcastle`), so the **recommended zero-touch approval** is a Tailscale ACL `autoApprovers` rule that auto-approves any route advertised by that tag — no API key, no manual step. Add to the tailnet policy:
  ```jsonc
  "tagOwners":     { "tag:sandcastle": ["autogroup:admin"] },
  "autoApprovers": { "routes": { "10.0.0.0/8": ["tag:sandcastle"] } }
  ```
  (Scope the route prefix to your CIDR pool.) On the client side, `sc login` enables `--accept-routes` and **verifies the route actually egresses over the tailnet, halting with guidance if not** (a raw connect to the gateway is not trusted — it can leak via the client's own LAN). Alternative to the tag rule: approve each route manually in the Tailscale admin console.
- **CIDR pools must not overlap the host's own network.** Pick `/16`s clear of the host IP and of `incusbr0`/other bridges (e.g. broker `10.249.0.0/16`, auth-app `10.250.0.0/16`). The allocator only sees existing v2 tenants, not the host subnet — an overlap fails with `dnsmasq: Address already in use`.
- Public DNS: `sc2.thieso2.dev` → the host's public IP (`65.21.132.31`). *(On an IP-less host, front the auth-host via a Cloudflare tunnel + `sc-edge` instead — see `docs/handoff-sandcastle-e2e-tunnel.md`.)*
- `TENANT=e2etest` throughout.

```bash
cd /workspace/sandcastle-incus
export INCUS_CONF=~/.config/incus-admin
set -a; . ./.env.sc2; set +a
TENANT=e2etest
SSHKEY=$(cat ~/.ssh/sandcastle_ed25519.pub)
IMAGE=images:debian/13          # stock upstream image, pulled on demand
SIMULATE_TOKEN=e2e-simulate-$(head -c6 /dev/urandom | base64 | tr -dc a-z0-9)   # simulated-auth mode
```

---

## The two-VM protocol ✅ — bare server VM + bare client VM

**The canonical e2e shape** (validated live 2026-07-06, `e2eserver`/`e2eclient`
on `obelix`): the operator hands over **two bare Debian 13 VMs**. VM 1 becomes
the **sc server** (Incus + auth-app + broker + public ingress); VM 2 becomes the
**client**, where **all client functionality** is installed and tested. Nothing
is mocked and nothing is pre-installed — both machines start from a stock
`images:debian/13/cloud` (or any bare Debian 13 with SSH + sudo).

Operator-supplied inputs: a first-level public hostname on a Cloudflare zone +
either a Cloudflare **API token** (installer creates tunnel + DNS itself) or a
dashboard-created **tunnel token**; a **Tailscale auth key** (the tenant's own
tailnet); optionally a Tailscale **API key** for tenant-side route approval; a
random `SIMULATE_TOKEN` (simulated GitHub — no OAuth app).

**Credentials come from the operator's gitignored `.env*` file** (repo-root
`.env.default`, or a run-specific `.env.sc2` sourced in the prologue) so the
whole e2e runs unattended — the harness sources it (`set -a; . ./.env.default;
set +a`) and never prompts. The Tailscale side is already there
(`SANDCASTLE_E2E_TAILSCALE_AUTHKEY`, `SANDCASTLE_E2E_TAILSCALE_TAG`,
`SANDCASTLE_E2E_TAILSCALE_ROUTES_APPROVED`, plus `SANDCASTLE_TAILSCALE_AUTHKEY`
/ `SANDCASTLE_AUTH_TAILSCALE_AUTHKEY` for install-time keys); keep the
Cloudflare API token for `--cloudflare-api-token` next to them
(`SANDCASTLE_E2E_CLOUDFLARE_API_TOKEN`). Never commit these files.

**Test-lab access (unattended SSH, verified live 2026-07-07).** The lab VMs on
`big` are reachable through host port-forwards, passwordless with the
operator's standard key:

```bash
ssh -p 7001 sc@big.thieso2.dev   # → obelix
ssh -p 7002 sc@big.thieso2.dev   # → asterix
```

Additional passwordless per-VM keys are named `<host>_ed25519` and may live in
`~/.config/sandcastle/` (e.g. `obelix_ed25519`, `asterix_ed25519`,
`e2e-test_ed25519`) or in `~/` — pass one with `ssh -i` when the default key
isn't authorized.

**Unattended by default.** The e2e must run with no human in the loop, so the
server install **always** includes `--simulate-github-token "$SIMULATE_TOKEN"`
— even when real OAuth credentials are also configured: the two modes coexist
(the token-gated `/oauth/github/simulate` endpoint registers *in addition to*
the real OAuth handlers), so humans can still log in with GitHub while the
harness short-circuits OAuth with `sc login --simulate-token … --as <user>`.
The secret gates everything — treat it like a password, generate it fresh per
run, and never enable it on a production install you don't intend to test
against. An attended real-OAuth login is an optional extra verification, not
part of the default protocol.

### Multi-install coexistence + shared identity (validated from scratch 2026-07-07)

Several sandcastles share one Incus host via `--prefix` (e.g. `sc` +
`--prefix id`), each with its own hostname/tunnel, CIDR pool, and Tenant DNS
Suffix. Validated **from scratch** (purged host → `install-incus` → two
`sc-adm install` runs) side by side on one server + one client:

- **Server:** distinct appliances (`sc2-*` vs `id-*`); tunnel installs bind
  **no broker host port** (tenant plane = auth-app token-gated
  `POST /api/projects`), so with a Cloudflare tunnel the broker appliance is
  **not deployed at all** and **nothing binds `:9443` on the host**. Own-tenant
  lookup,
  `tenant.List`, and the DNS reconciler are all prefix-scoped (`meta.KeyV2Prefix`
  on the infra project), so same-named tenants of different installs are
  distinct and each auth-app only sweeps its own sidecars. This scoping covers
  **v1 tenants too**, more strictly: a `kind=tenant` project is **always** a
  legacy install's tenant — same-named or not, whatever the prefix — so its
  `/24` is **occupied**, never adopted as `PreferredCIDR` (the v1 bridge, with
  its dnsmasq on the gateway IP, may still be live; the v2 path never creates
  `kind=tenant` projects). Before this, a first login by a user who owned a v1
  tenant reused the v1 tenant's `/24` and the new install's bridge died with
  `dnsmasq: failed to create listening socket for <gw>: Address already in
  use`. Regression test: `TestProvisionReuseInputsNeverOwnsV1CIDR`. **e2e
  check:** on a host with a v1 `sc-<user>` tenant, first login by the same
  user to any v2 install provisions on a fresh `/24`.
- **Own appliance bridge:** each install creates and owns a NATed bridge
  `<prefix>-net` (`sc2-net`, `id-net`) with an Incus auto-picked subnet and puts
  its auth-app/broker on it — so the appliances share **no** network object with
  v1 or with another install (only the Incus daemon is shared). `--bridge`
  overrides to an existing bridge (e.g. `incusbr0`). Per-tenant bridges are
  unchanged — each tenant still owns its own `/24`. **e2e check:** after two
  installs, `incus network list` shows `sc2-net` and `id-net` as distinct
  managed bridges, and nothing binds `:9443` on the host.
- **Client — shared identity:** all enrollments live in ONE incus config dir
  (`~/.config/sandcastle/incus`) sharing ONE client keypair; each install is a
  plain remote — `sc-<tenant>` (default install), `sc-<prefix>-<tenant>`
  otherwise — so `incus remote switch sc-id-<tenant>` moves between sandcastles
  natively. Each remote is pinned to its install's default project (the shared
  cert's server-side default is ambiguous). Because Incus keys trust by
  fingerprint, `sc login` sends the client's existing cert and each install
  **unions** its projects into that one trust entry. Enrolling the *second*
  install can't redeem the join token (the daemon already trusts the keypair —
  `Client is already trusted`), so `sc login` falls back to a certificate-based
  `incus remote add … --auth-type=tls --accept-certificate --project
  <install-default>`. The explicit `--project` is load-bearing: without it
  `incus remote add` sees both installs' projects through the shared cert and
  **prompts interactively**, which EOFs and hangs the login. Regression test:
  `TestTrustedClientRemoteAddArgsPinsProject`.
- **Proven green:** native `incus launch sc-e2e:web` and `sc-id-e2e:api` both
  RUNNING from the one client; per-install DNS zones with **zero cross-zone
  bleed** (`web.default.e2e` resolves in the sc zone and is NXDOMAIN in the id
  zone, and vice-versa for `api.default.idefix`).

**One client, several sandcastles on the SAME Incus daemon (validated live
2026-07-07, prefixes `tc2` + `tc3`).** Every sidecar's Incus Reach proxies to
the *same* host Incus API, and the shared client cert's trust entry unions the
projects of every install the user enrolled in — so from the daemon's point of
view all of one user's remotes are interchangeable, and only the
`INCUS_PROJECT` pin separates the installs. The CLI therefore scopes every
tenant lookup to the install the **configured remote** belongs to: the remote
name encodes the install (`sc-<prefix>-<tenant>`, `sc-<tenant>` for the
default prefix; inverted by `installPrefixFromRemoteName`, regression test
`TestInstallPrefixFromRemoteName`). Without that scoping, `sc incus`,
`sc create`, and `sc connect` matched the first same-named tenant on the daemon
and silently operated on the *other* install's project.
- **Protocol:** install two sandcastles on one Incus host with distinct
  `--prefix`, distinct `--auth-hostname`, distinct `--cidr-pool`, and log the
  same GitHub user into BOTH from one client machine, choosing a different
  Tenant DNS Suffix per login (e.g. `sc login https://<host-a> --dns-suffix=tcA`,
  `sc login https://<host-b> --dns-suffix=tcB`). Switch installs with
  `sc incus remote switch sc-<prefix>-<tenant>` (or plain `incus remote switch`
  in the shared config dir): the incus current remote is the **single source of
  truth** for which install `sc` targets — the whole CLI (remote, project pin,
  tenant scoping) follows it. `sc config set remote …` writes through to the
  same knob; `SANDCASTLE_REMOTE` still overrides for a single invocation.
- **PASS:** with a machine created in only one install,
  `VERBOSE=1 sc incus ls` under each remote prints
  `INCUS_PROJECT=<that-install's-prefix>-<tenant>-default` and lists only that
  install's machines (the other install's view is empty — no cross-install
  bleed). `sc create`/`sc connect`/`sc delete` land in the install the current
  remote belongs to, and **`sc list` / `sc project list` / `sc status` show that
  same install** — a machine visible to `sc incus ls` but missing from `sc list`
  under the same remote is the name-only-match regression (see appendix). Each login installed its own resolver zone
  (`/etc/resolver/tcA`, `/etc/resolver/tcB` on darwin) pointing at its own
  tenant's CoreDNS, and each tenant got a /24 from its own install's pool.

**Tenant DNS Suffix e2e scenarios (`sc login --dns-suffix=…`).** The suffix is
chosen at first login, is **immutable** per tenant per install
(`TestPlanCreateV2DNSSuffix`), and the existing-suffix lookup is
prefix-scoped so the same user on two installs can (and should) use two
different suffixes (`ProvisionReuseInputs` tests). Run all three:

1. **Fresh suffix.** `sc login https://<host-a> --dns-suffix=tcA` on a fresh
   tenant, then `sc create dev`. **PASS:** the login installs
   `/etc/resolver/tcA` (darwin; `resolvectl` domain on linux) pointing at the
   tenant's CoreDNS; `dev.default.tcA` resolves to the machine's tenant IP from
   the client (`dscacheutil -q host -a name dev.default.tcA` / `getent hosts`),
   and `sc connect dev` works by name.
2. **Persistence + immutability.** Re-run `sc login https://<host-a>` with NO
   `--dns-suffix`. **PASS:** the suffix stays `tcA` (summary + resolver
   unchanged). Then `sc login https://<host-a> --dns-suffix=other` must fail
   fast with `the Tenant DNS Suffix is immutable: … already uses "tcA"` — no
   half-provisioned state, and the tcA zone keeps resolving.
3. **Two installs, one client, two suffixes.** With install A (`tcA`) and
   install B on the same Incus daemon: `sc login https://<host-b>
   --dns-suffix=tcB`. Suffixes MUST be distinct across installs — the client
   resolver path is per-suffix (`/etc/resolver/<suffix>`), so a shared suffix
   would clobber the other install's resolver. **PASS:** both resolver files
   coexist, each pointing at its own tenant's CoreDNS address (from its own
   install's CIDR pool); names in BOTH zones resolve simultaneously without
   switching remotes (DNS is per-suffix, not per-current-remote); cross-zone
   lookups are NXDOMAIN both ways (`dev.default.tcA` absent from tcB's zone
   and vice versa).

Keep CIDR pools distinct across installs sharing a tailnet. Robustness fixes the
from-scratch run surfaced (all in `internal/incusx` + the auth-app): tolerate
the spurious "already running" on a cached-image create; wait for RUNNING
before configuring an appliance/sidecar; start an existing STOPPED sidecar on
re-provision; and — the big one for flaky edges — **provision on a detached
context** so a Cloudflare-tunnel poll timeout can't cancel tenant bring-up
mid-flight.

### Run logging — `logs/<machine-name>.log` (required)

Every install/test run keeps a **verbatim transcript per machine**: each command
that runs on a VM, followed by that command's full output, appended to
`logs/<machine-name>.log` (e.g. `logs/e2eserver.log`, `logs/e2eclient.log`) in
the run's working directory. Drive every remote command through this wrapper —
no untracked side-channel commands:

```bash
mkdir -p logs
# run <machine-name> <ssh-target> <command…> — echoes the command, appends
# command + combined output to logs/<machine-name>.log, and prints it live.
run() {
  local m="$1" t="$2"; shift 2
  { printf '\n$ %s\n' "$*"; ssh "$t" "$@" 2>&1; } | tee -a "logs/$m.log"
}
run e2eserver server sudo sc-adm install-incus            # example
```

**PASS:** after the run, `logs/` contains one log per machine, and every
command from the protocol below appears in the right log with its output —
the logs alone must be enough to replay or debug the run.

### VM 1 — the server

```bash
# from the workstation: the fat binary, then the four names
scp bin/linux-amd64/sandcastle server:/tmp/
ssh server 'sudo install -m0755 /tmp/sandcastle /usr/local/bin/sandcastle &&
  for n in sc sc-adm sandcastle-admin; do sudo ln -sf sandcastle /usr/local/bin/$n; done'

ssh server
sudo sc-adm install-incus                       # Zabbly repo + incus + minimal init
incus config set core.https_address=:8443
sc-adm install \
  --auth-hostname  "$E2E_HOSTNAME" \
  --simulate-github-token "$SIMULATE_TOKEN" \   # ALWAYS — unattended runs; coexists with real --github-* creds
  --admin-github-users thieso2 \
  --ingress cloudflare --cloudflare-api-token "$CLOUDFLARE_API_TOKEN" \
  --cidr-pool 10.254.0.0/16                     # clear of every other deployment on the tailnet
```

**PASS:** `systemctl is-active` reports `active` for `sandcastle-auth-app`,
`caddy`, `cloudflared` (in `sc2-auth-app`) and `sandcastle-broker` (in
`sc2-broker`); tunnel **Healthy**; `curl https://$E2E_HOSTNAME/healthz` → 200.

### VM 2 — the client (all client functionality)

```bash
# install: binary (as above) + the two client prerequisites
apt-get install -y incus-client                                  # sc login shells out to incus
curl -fsSL https://tailscale.com/install.sh | sh
tailscale up --auth-key "$TAILSCALE_AUTH_KEY" --accept-routes    # client must be a tailnet node

# 1. login — provisions the tenant, exercises --dns-suffix
sc login "https://$E2E_HOSTNAME" --simulate-token "$SIMULATE_TOKEN" --as e2edns \
  --tailscale-auth-key "$TAILSCALE_AUTH_KEY" --dns-suffix castle
#    approve the sidecar's advertised /24 (Tailscale admin console, API, or a
#    tag:sandcastle autoApprover)
# PASS: all four routing checks ✓; the tenant's stored suffix is the chosen one
#       (server: incus project get sc2-<tenant> user.sandcastle.v2.suffix)

# 2. projects + machines (flagless broker path, project-scoped refs)
sc create web --detach                       # default project
sc project create backend                    # NO flags: broker URL + cert from the login
sc create backend:api --detach
sc list                                      # FQDN column shows canonical names
# PASS: EVERY machine created above appears in `sc list` immediately (list is
#       scoped to the current remote's install — a same-named tenant of another
#       install on the same daemon must never shadow this one)

# 2b. shared $HOME + /workspace across the project (CT ↔ VM)
sc create vm1 --vm --detach                  # a VM next to the CTs in default
#    wait for both to be RUNNING (sc list), then:
sc incus exec web -- sh -c 'echo from-ct > /workspace/marker; echo home-ct > /home/$USER/hmarker'
sc incus exec vm1 -- sh -c 'cat /workspace/marker; cat /home/$USER/hmarker'   # → from-ct / home-ct
sc incus exec vm1 -- sh -c 'echo from-vm >> /workspace/marker'
sc incus exec web -- cat /workspace/marker   # → from-ct then from-vm
# PASS: the VM reads what the CT wrote on BOTH volumes and vice-versa (one
#       shared home+workspace volume pair per project, security.shifted);
#       the login user can write /workspace; ~/.ssh/authorized_keys lives on
#       the shared /home

# 3. the ADR-0018 DNS battery (sidecar CoreDNS = tenant CIDR .3)
DNS=10.254.0.3
dig +short web.default.castle      @$DNS     # canonical            → machine IP
dig +short web.castle              @$DNS     # default short alias  → same IP
dig +short app1.web.default.castle @$DNS     # wildcard             → same IP
dig +short api.backend.castle      @$DNS     # canonical, 2nd proj  → machine IP
dig +short api.castle              @$DNS     # PASS = NXDOMAIN (no short outside default)
# guest identity + guest resolver (via the server admin socket):
#   incus exec web --project sc2-<tenant>-default -- hostname -f   → web.default.castle
#   incus exec api --project sc2-<tenant>-backend -- getent hosts web.default.castle

# 4. lifecycle + error UX
sc c web -- hostname                         # ensure/start/ssh
sc delete backend:api --yes
sc create nosuch:box        # PASS: "project \"nosuch\" not found in tenant … (projects: …)"
sc delete api:backend --yes # PASS: swapped-reference hint: did you mean "backend:api"?

# 4a. duplicate machine names across projects
# Incus scopes instance DNS names to the *bridge*, not the project, and every
# project of the tenant shares one bridge. A second "web" is therefore created
# but refused a start — and, because the check enumerates instances from the DB
# regardless of state, the surviving default:web can no longer start either
# until the duplicate is gone.
sc create backend:web       # PASS: "Instance DNS name \"web\" already used on network"
sc ls -a                    # PASS: backend:web listed, stopped, no IP
sc start web                # PASS (no tty): "machine \"web\" exists in 2 projects (backend:web, default:web)"
                            # PASS (tty): numbered prompt "Which one? [1-2]"
sc delete backend:web --yes # PASS: qualified reference deletes without asking
sc delete web               # PASS (tty): confirm names it "default:web", not "web"

# 5. suffix immutability
sc login "https://$E2E_HOSTNAME" --simulate-token "$SIMULATE_TOKEN" --as e2edns \
  --force --dns-suffix other
# PASS: "the Tenant DNS Suffix is immutable: tenant … already uses \"castle\""

# 6. verbose service logging + Activity Log (per-user scoping)
#    a) server VM: every request + work span carries a timestamp and duration.
#       journalctl -u sandcastle-auth-app -n 50 --no-pager
#       journalctl -u sandcastle-broker  -n 50 --no-pager
#       PASS: lines like "… auth-app request POST /api/device/poll status=200 dur=…"
#             and spans "… span provision.personal_tenant dur=… user=<tenant>".
#    b) auth-app /logs UI, signed in as a regular (non-admin) user:
#       PASS: the page shows THIS user's requests/spans with timestamps + durations,
#             and shows NO other user's rows and NO system rows.
#    c) auth-app /logs UI, signed in as a Sandcastle Admin:
#       PASS: the page shows ALL users' rows plus system rows (empty user);
#             the ?q= filter narrows in place; a non-admin cannot reach another
#             user's rows even with ?q=.
```

Registration is event-driven: records appear **within seconds** of
`incus launch`/`sc create` (30s reconcile loop as backstop). The admin-plane
counterpart of this protocol is automated in `scripts/e2e-v2.sh` (run it on the
server VM once the stack is up — it refuses to run without the auth-app).

**Teardown:** delete both VMs; remove the tenant sidecar + client devices from
the tailnet; delete the run's Cloudflare tunnel + DNS record.

> Gotcha from the validated run: mid-run binary swaps on the server do NOT
> retro-apply to an already-provisioned tenant — the suffix is immutable, so a
> tenant provisioned before a fix keeps its original suffix. Use a fresh
> `--as <user>` per attempt.

---

## Hermetic harness — a fresh sc2 per run, torn down after 🚧
The real e2e provisions a **throwaway sc2 host** as a VM on `big`, with its **own
nested Incus** and its own sc2 stack (edge + broker + auth-app), and routes a fresh
public hostname to it through the **outer** `sc-edge`. Each run is fully isolated and
leaves nothing behind — no shared appliances, no leftover tenants.

Why it works: **`*.scdev.thieso2.dev` is a public wildcard CNAME to the outer
`sc-edge`.** So a run can claim `<run>.scdev.thieso2.dev`, point the outer sc-edge at
the run's VM, and the whole flow (LE cert, GitHub OAuth, login, machines) happens
against a hostname that resolves publicly and terminates at a **fresh, disposable**
sc2 — a genuine end-to-end path, not a mock.

```bash
RUN=e2e-$(date +%s)                 # unique id per run
HOST=$RUN.scdev.thieso2.dev         # resolves (publicly) to the outer sc-edge

# 1) fresh VM on big with nested virtualization → it becomes its own Incus host
incus launch images:debian/13/cloud big:sc2-$RUN --vm \
  -c security.secureboot=false -c limits.cpu=4 -c limits.memory=8GiB \
  -c security.nesting=true
# wait for cloud-init + agent, capture the VM's bridge IP
for i in $(seq 1 60); do VMIP=$(incus ls big:sc2-$RUN -c4 --format csv 2>/dev/null | grep -oE '10\.[0-9.]+' | head -1); [ -n "$VMIP" ] && break; sleep 3; done

# 2) inside the VM: install Incus + push this fat binary + stand up the sc2 stack.
#    The run's sc2 uses $HOST as its Auth Hostname (so LE + OAuth callback match).
incus exec big:sc2-$RUN -- bash -c 'apt-get update -qq && apt-get install -y -qq incus'
incus file push bin/linux-amd64/sandcastle big:sc2-$RUN/usr/local/bin/sandcastle --mode 0755
#    … init incus, deploy sc-edge/broker/auth-app inside the VM (Phases 1–3 run *in* the VM),
#    with the inner auth app fronted by the inner sc-edge for $HOST.

# 3) route <RUN>.scdev.thieso2.dev → the run's VM via the OUTER sc-edge.
#    SNI passthrough so the VM's inner edge owns the TLS (does its own LE for $HOST):
incus exec big:sc-edge --project infrastructure -- bash -c "
  # add a layer4 SNI-passthrough for $HOST → $VMIP:443, then reload
  systemctl reload caddy"
```

**Teardown (always, even on failure):**
```bash
incus delete -f big:sc2-$RUN                       # removes the whole disposable sc2
incus exec big:sc-edge --project infrastructure -- \
  sed -i "/$HOST/d" /etc/caddy/Caddyfile           # drop the run's route
incus exec big:sc-edge --project infrastructure -- systemctl reload caddy
```

🚧 **To build:** the nested-Incus VM image + an automated inner-stack deploy
(`Phases 1–3` executed *inside* the VM against `$HOST`), and the outer sc-edge
SNI-passthrough entry. Until then, the phases below run against the **persistent**
sc2 on `big` (edge/broker/auth-app + a throwaway `e2etest` tenant) — same flow, but
sharing the long-lived appliances. The hermetic harness makes every run disposable.

> ✅ **Second full validation (2026-07-04 evening, `testzone-vm1`):** the whole
> protocol re-ran from scratch on another fresh isolated VM — including the new
> **Phase 7c** (`sc c` create/start/SSH lifecycle from the client), the **login
> tailnet precheck** (verified refusing on the tailscale-less host), the
> **layered routing diagnosis** (all ✓ on the client node), and **idempotent
> re-login** (`Already logged in …`). The run caught and fixed three real bugs
> (shared-volume idmap shift breaking VM sshd, enroll not saving local
> defaults, `/api/tenants` blind to v2 tenants) — see the appendix. Deliberate
> deviation: distinct CIDR pools (broker `10.251/16`, auth-app `10.252/16`) so
> the run's tailnet routes can't contend with the live `igel` deployment.

> ✅ **Pattern validated by hand (2026-07-04, `sc2iso-vm2`):** the full protocol was
> executed on a fresh, fully isolated Debian 13 VM — own Incus 7.2 (Zabbly), own
> sc-edge (**Cloudflare-tunnel mode**, `PUBLIC_PORTS=0`, API-created tunnel +
> first-level hostname `sc2iso2-<rand>.thieso2.dev`), own auth-app + broker, a
> nested client CT driving everything. All phases green except 8b (unchanged ⚠️).
> In tunnel mode Cloudflare's edge serves a real LE cert, so the Phase 2/7b issuer
> checks pass verbatim; the inner Caddyfile is plain `http://<host>:8080` vhosts.
> What remains 🚧 is only the *automation* of this setup.

> ⚠️ **The real e2e needs TWO VMs, and BOTH must be genuine tailscale nodes**
> — the runnable protocol is [The two-VM protocol](#the-two-vm-protocol---bare-server-vm--bare-client-vm) above.
> `sc login` now refuses to start the device flow on a machine that is not itself
> a tailnet node (see Phase 9 notes), because in a tailnet **subnet-to-subnet does
> not route**: a machine that is merely *resident* in a subnet some other router
> advertises (e.g. a VM on `big` reached via big's subnet routes) can never reach
> the tenant `/24` behind the sidecar's route — and making such a machine a node
> breaks its old inbound path (asymmetric routing: replies leave via its own
> tailscale0 and get dropped by the caller). So the harness topology is:
> **VM 1 = the sc2 host** (Incus + edge + auth-app + broker; the *sidecar* is its
> tailnet presence) and **VM 2 = the client**, a clean machine whose ONLY sandcastle
> path is its own `tailscale up --accept-routes` membership — not a machine that
> other infrastructure already routes to by subnet.

---

## Phase 0 — Teardown (idempotent reset) ✅
Remove any prior test-tenant server state + local client config so the run starts clean.

> ✅ **v2 tenants now have a product teardown path:**
> `sc-adm tenant delete <tenant> --yes --purge` (scope a prefixed install with
> `SANDCASTLE_INCUS_PROJECT_PREFIX=<prefix>`) deletes the app projects
> (machines, images, shared home/workspace volumes, profiles), the infra
> project (sidecar), and the tenant bridge — the manual loop below remains for
> v1 tenants and partial states. Without `--purge` a v2 tenant is refused
> (all-or-nothing). The sidecar's tailnet device is NOT removed (BYO tailnet —
> no server-side API key by design): delete it in the Tailscale admin console
> or use ephemeral keys. **PASS:** after the purge, `incus project list | grep
> <prefix>-<tenant>` and `incus network list | grep <prefix>-<tenant>` are both
> empty, and a same-named tenant of ANOTHER install on the daemon is untouched.

```bash
# server: delete the test tenant's instances + images + custom profiles + projects + bridge.
# NB: v2 projects have their own image store (features.images=true) and a custom
# `sidecar` profile; a project won't delete until BOTH are cleared, so purge them too.
for p in sc2-$TENANT-default sc2-$TENANT; do
  incus list big: --project $p -c n --format csv 2>/dev/null | while read -r n; do
    [ -n "$n" ] && incus delete -f big:$n --project $p 2>/dev/null; done
  incus image list big: --project $p --format csv -c f 2>/dev/null | while read -r fp; do
    [ -n "$fp" ] && incus image delete big:$fp --project $p 2>/dev/null; done
  # shared home/workspace volumes: detach from the default profile FIRST, then delete
  for v in home workspace; do
    incus profile device remove big:default $v --project $p 2>/dev/null
    incus storage volume delete big:default $v --project $p 2>/dev/null
  done
  incus profile list big: --project $p --format csv -c n 2>/dev/null | while read -r pr; do
    [ -n "$pr" ] && [ "$pr" != "default" ] && incus profile delete big:$pr --project $p 2>/dev/null; done
  incus project delete big:$p 2>/dev/null || true
done
incus network delete big:sc2-$TENANT 2>/dev/null || true
# client: wipe the generated local incus config
rm -rf ~/.config/sandcastle/$TENANT
```
**PASS:** no `sc2-e2etest*` projects remain (`incus project list big: | grep e2etest` is empty).

> ⚠️ Purging a v2 project's image store deletes its **only local copy** of the stock
> base image if that image lives solely inside the tenant project. If a later run
> reports `Image not provided for instance creation`, re-cache it into the shared
> store: `incus image copy images:debian/13 big: --project default` (restores
> fingerprint `d31c34fadc08`, which infra projects share via `features.images=false`).

---

## Phase 1 — Deploy the Auth App appliance ✅
The sc2 web API. No host port (fronted by `sc-edge`). Copies the fat binary in.
Stock image is the default (`--base-image images:debian/13`, pulled on demand — no `--base-image` needed).

> ✅ **Host prep is one command too (2026-07-05):** `sudo sc-adm install-incus`
> installs the latest Incus from the Zabbly stable repo on any Debian-based
> host (repo + apt + `incus admin init --minimal` + incus-admin group),
> idempotently — replaces the hand-rolled Zabbly setup script.

> ✅ **One-command install (2026-07-05).** `sc-adm install` now deploys the
> auth-app AND the broker in one shot with a shared `--cidr-pool` (optional,
> default `10.248.0.0/16` — keep pools distinct across installations sharing a
> tailnet) and a `--prefix` (default `sc`) that scopes the whole installation.
> It **refuses to run when an installation under the same prefix already
> exists** (listing what it found) and warns when the pool overlaps a host
> address. Phases 1+3-broker collapse to:
> ```bash
> sc-adm install --auth-hostname "$PUBIC_URL" \
>   --simulate-github-token "$SIMULATE_TOKEN" --admin-github-users thieso2 \
>   --cidr-pool 10.252.0.0/16
> ```
> `--ingress cloudflare|acme|none` builds the public edge INTO the auth-app
> appliance (caddy + cloudflared inside the same container, proxying
> localhost:9444): `--cloudflare-api-token` creates the tunnel + DNS fully
> automatically (validated live 2026-07-05 — healthz 200 over an
> installer-created tunnel); `--cloudflare-tunnel-token` uses a
> dashboard-created tunnel; `acme` binds host :80/:443 with Let's Encrypt.
> The standalone sc-edge recipe is no longer needed for the auth hostname.
> First-login provisioning must succeed on its **first pass** — a
> `Personal Tenant provisioning failed: write /etc/resolv.conf to sidecar: Not
> Found` message is a regression (the sidecar's `/etc/resolv.conf` is a symlink
> on the stock image; file pushes must clear it first).
> The **interactive `login.tailscale.com` URL is the primary join path**: the
> tenant logs in with no key, `sc login` prints the sidecar's URL and **waits**
> for the join, then finishes enrollment automatically.
> (`--tailscale-auth-key tskey-…` at login is for unattended/CI runs only.)
> The URL is durable: the sidecar keeps its pending `tailscale up` (and the URL)
> across awaiting-tailnet polls and across `sc login` re-runs, re-mints a fresh
> URL if the recorded one is lost or stale, and login re-prints whenever the URL
> changes. **PASS:** with no auth key, `sc login` prints a
> `https://login.tailscale.com/…` URL; Ctrl-C + `sc login --force` prints a URL
> again (same one while the pending join is healthy); awaiting polls cycle in
> seconds, not minutes; opening the URL and approving the route lets the waiting
> login complete enrollment on its next poll. The old separate
> `auth-app deploy` + `bootstrap` commands still exist for piecewise installs.

**Simulated GitHub (recommended for e2e — no OAuth app):**
```bash
sc-adm auth-app deploy \
  --auth-hostname "$PUBIC_URL" \
  --simulate-github-token "$SIMULATE_TOKEN" \
  --admin-github-users thieso2 \
  --tailscale-auth-key "$TAILSCALE_AUTH_KEY"
```

**Real OAuth app (alternative):**
```bash
sc-adm auth-app deploy \
  --auth-hostname "$PUBIC_URL" \
  --github-client-id "$GH_CLIENT_ID" \
  --github-client-secret "$GH_CLIENT_SECRET" \
  --admin-github-users thieso2
```
> Note (⚠️): running the wrapper against a multi-address remote can mangle the
> address list. Deployed here by hand into `d31c34fadc08` in project
> `infrastructure` as `sc2-auth-app` (systemd unit `sandcastle-auth-app`,
> listening `:9444`). Both paths yield the same appliance.

**PASS:** `incus exec big:sc2-auth-app --project sc2-infra -- systemctl is-active sandcastle-auth-app` → `active`, listening `:9444`.

---

## Phase 2 — Front it on `sc-edge` (public HTTPS, LE cert, no client certs) ✅
Add a terminate vhost so `https://sc2.thieso2.dev` reverse-proxies to the auth app.

```bash
AUTH_IP=$(incus exec big:sc2-auth-app --project sc2-infra -- \
  ip -4 -o addr show eth0 | grep -oE '10\.196\.38\.[0-9]+' | head -1)
incus exec big:sc-edge --project infrastructure -- bash -c "
  grep -q '$PUBIC_URL' /etc/caddy/Caddyfile || printf '\n%s {\n    reverse_proxy http://%s:9444\n}\n' '$PUBIC_URL' '$AUTH_IP' >> /etc/caddy/Caddyfile
  caddy validate --config /etc/caddy/Caddyfile && systemctl reload caddy"
```
**PASS (all ✅ validated):**
```bash
curl -s --resolve $PUBIC_URL:443:65.21.132.31 https://$PUBIC_URL/healthz -o /dev/null -w '%{http_code}\n'   # 200
# simulated mode: a valid token mints a session (200); a wrong token → 403; unset → 404 (route unregistered)
curl -s --resolve $PUBIC_URL:443:65.21.132.31 -o /dev/null -w '%{http_code}\n' \
  "https://$PUBIC_URL/oauth/github/simulate?token=$SIMULATE_TOKEN&username=thieso2"   # 200
# real-OAuth mode instead: /login/github → 302 to github.com; callback lands at /oauth/github/callback
echo | openssl s_client -servername $PUBIC_URL -connect 65.21.132.31:443 2>/dev/null | openssl x509 -noout -issuer   # issuer=… Let's Encrypt
```

---

## Phase 3 — Provision the tenant (v2, stock Debian sidecar) ✅
Runs inside the broker appliance (has the host socket). Installs CoreDNS (binary) +
Tailscale (apt) on a **stock** Debian sidecar — no `sandcastle/base`. `TAILSCALE_AUTH_KEY`
answers the tailnet question; `--ssh-key` bakes the key into the default-project profile.

```bash
# ensure the broker runs the current fat binary
incus exec big:sc2-broker --project sc2-broker -- systemctl stop sandcastle-broker
incus file push bin/linux-amd64/sandcastle big:sc2-broker/usr/local/bin/sandcastle-admin --project sc2-broker --mode 0755
incus exec big:sc2-broker --project sc2-broker -- systemctl start sandcastle-broker

# create the tenant; capture the enrollment token
OUT=$(incus exec big:sc2-broker --project sc2-broker -- \
  /usr/local/bin/sandcastle-admin tenant create $TENANT \
  --sidecar-image $IMAGE --ssh-key "$SSHKEY" --tailscale-authkey "$TAILSCALE_AUTH_KEY")
TOKEN=$(echo "$OUT" | grep -oE 'token=[A-Za-z0-9+/=]+' | head -1 | cut -d= -f2-)
echo "token len: ${#TOKEN}"
```
**PASS:**
- Projects `sc2-e2etest` (infra) + `sc2-e2etest-default` created; bridge `sc2-e2etest` = `10.249.0.0/24`; sidecar `sc2-e2etest` at `10.249.0.3`. ✅
- CoreDNS: `incus exec big:sc2-e2etest --project sc2-e2etest -- /usr/local/bin/coredns -version` → `CoreDNS-1.14.3`, service `active`. ✅
- `$TOKEN` non-empty (~640 chars). ✅
- ⚠️ **Tailscale readiness race:** the automated `tailscale up` during create can land before `tailscaled` is ready → sidecar shows `Logged out`. Manual `tailscale up --auth-key=$TAILSCALE_AUTH_KEY --advertise-routes=10.249.0.0/24` **succeeds** (joins as `sc2-e2etest`). **Fix pending:** add a `tailscaled` readiness wait/retry in `v2TailscaleUp`.

---

## Phase 4 — Client enrollment: local incus config from scratch ✅
Wipe the local config and regenerate it from the token.

```bash
rm -rf ~/.config/sandcastle/$TENANT
./bin/sc enroll $TENANT --token "$TOKEN"       # NB: add --incus-endpoint https://<host>:8443 when the Incus host is not big
DIR=~/.config/sandcastle/$TENANT/incus
ls "$DIR"                                   # client.crt client.key config.yml servercerts/
INCUS_CONF="$DIR" incus remote list | grep $TENANT
```
**PASS (✅ validated):** `$DIR` recreated with `client.crt`/`client.key`/`config.yml`; two cert-pinned remotes — `e2etest` (base) and `e2etest-default` (project).

---

## Phase 5 — `incus remote switch <project>` works ✅
```bash
export INCUS_CONF=~/.config/sandcastle/$TENANT/incus
incus remote switch $TENANT-default
incus list                                  # empty table (no machines yet), no auth error
export INCUS_CONF=~/.config/incus-admin
```
**PASS (✅ validated):** switch succeeds; `incus list` returns cleanly over the cert-pinned remote.

---

## Phase 6 — Default-project profile: SSH key + user + cloud-init ✅
```bash
incus profile show big:default --project sc2-$TENANT-default
```
**PASS (✅ validated):** `cloud-init.user-data` contains the login user with
`ssh_authorized_keys: [ <your key> ]`, installs `openssh-server`, and
`runcmd: [systemctl, enable, --now, ssh]`; devices include the shared
**`home`** (→ `/home`) and **`workspace`** (→ `/workspace`) volumes.

> **Login user + key provenance.** `sc login` prepares the SSH key itself —
> it uses `~/.ssh/id_ed25519.pub` when present, otherwise generates
> `~/.ssh/sandcastle_ed25519` — and uploads it during the device poll; the
> auth-app stores it on the user and bakes it into this profile on every
> (re-)login, so rotating the key is just `sc login --force`. The profile's
> login **username** is the client's Unix user from device login (`$USER`),
> falling back to the deployment's `--default-unix-user`, then `dev`
> (`root` and invalid names are skipped, not errors). Broker-created tenants
> (`tenant create`) take `--ssh-key` explicitly and default to `dev`.

---

## Phase 7 — Launch a CT **and** a VM via `incus launch`, verify DNS + SSH ✅
The tenant launches machines into their default project. **Use the `/cloud` image
variant** — cloud-init applies the default-project profile (user `dev` + SSH key +
`openssh-server` + `systemctl enable --now ssh`). The plain image has **no cloud-init**,
so there'd be no `dev` user and SSH would be refused.

```bash
Pd="--project sc2-$TENANT-default"
incus launch images:debian/13/cloud big:ct1 $Pd          # container
incus launch images:debian/13/cloud big:vm1 $Pd --vm     # virtual machine
# wait for cloud-init (dev user + sshd) and capture IPs
declare -A IP
for m in ct1 vm1; do
  for i in $(seq 1 45); do
    a=$(incus exec big:$m $Pd -- sh -c 'ip -4 -o addr show | grep -oE "10\.249\.0\.[0-9]+" | head -1' 2>/dev/null)
    r=$(incus exec big:$m $Pd -- sh -c 'id dev >/dev/null 2>&1 && ss -ltn | grep -q ":22 " && echo ready' 2>/dev/null)
    [ "$r" = ready ] && { IP[$m]=$a; break; }; sleep 6
  done
  echo "$m = ${IP[$m]}"
done
# register A-records in the sidecar CoreDNS (now auto-registered by the auth-app reconciler within ~30s; manual step optional)
incus exec big:sc2-$TENANT --project sc2-$TENANT -- bash -c "
  Z=/etc/coredns/zones/db.$TENANT
  grep -q '^ct1 ' \$Z || echo 'ct1 IN A ${IP[ct1]}' >> \$Z
  grep -q '^vm1 ' \$Z || echo 'vm1 IN A ${IP[vm1]}' >> \$Z
  sed -i 's/hostmaster.$TENANT. [0-9]*/hostmaster.$TENANT. 3/' \$Z; systemctl restart coredns"
```
**PASS (✅ validated):**
```bash
# DNS via CoreDNS resolves both
incus exec big:sc2-$TENANT --project sc2-$TENANT -- sh -c 'for n in ct1.'$TENANT' vm1.'$TENANT'; do echo "$n -> $(dig @127.0.0.1 $n +short)"; done'
#   ct1.e2etest -> 10.249.0.89   vm1.e2etest -> 10.249.0.195
# SSH into BOTH as dev with the profile-baked key (sc-dev reaches the tenant bridge via host routing)
ssh -i ~/.ssh/sandcastle_ed25519 dev@${IP[ct1]} 'echo OK $(whoami)@$(hostname) $(uname -r)'   # OK dev@ct1 7.0.x  (container)
ssh -i ~/.ssh/sandcastle_ed25519 dev@${IP[vm1]} 'echo OK $(whoami)@$(hostname) $(uname -r)'   # OK dev@vm1 6.12.x (VM kernel)
```

**Shared `$HOME` + `/workspace` across the project (CT ↔ VM) ✅** — machines in the
same project share `$HOME` and `/workspace` **by default** (per-project storage
volumes), so a file written on the CT is visible on the VM and vice-versa:
```bash
# write on the CT, read on the VM (and the reverse) — same project, shared volume
incus exec big:ct1 $Pd -- sh -c 'echo from-ct > /workspace/marker; echo from-ct-home > /home/dev/hmarker'
incus exec big:vm1 $Pd -- sh -c 'cat /workspace/marker; cat /home/dev/hmarker'   # → from-ct / from-ct-home
ssh -i ~/.ssh/sandcastle_ed25519 dev@${IP[vm1]} 'echo from-vm >> /workspace/marker'
incus exec big:ct1 $Pd -- cat /workspace/marker                                   # → from-ct then from-vm
```
**PASS (✅ validated on Incus 7.2):** the VM reads `from-ct` written by the CT, and the
CT sees the VM's append — `/workspace` is one shared volume per project.
✅ **Built:** `CreateTenantV2` (and `CreateProjectV2` for later app projects) creates
two per-project custom **filesystem** volumes — `workspace` (→ `/workspace`) and
`home` (→ `/home`) — and the `default` profile attaches both as `disk` devices. The
same fs volume attaches to a CT **and** a VM simultaneously (incus shares it via
virtiofs to the VM), so files written on any machine in the project are visible on the
others — including the login user's whole home directory (cloud-init creates it with
the authorized key on the first machine; every later machine sees the same `$HOME`).
Validated 2026-07-04 on `igel` (tenant `hometest`): a CT-written `/home/dev/marker`
read+appended on a VM and back, `authorized_keys` living on the shared volume, `ssh`
active on both.

> ✅ **Auto-registration is now automatic.** A background reconciler in the auth-app
> registers every running machine (incl. freeform `incus launch`) into the sidecar
> CoreDNS zone as `<name>.<suffix>` (~30s). Manual A-record steps below are no longer
> required — query CoreDNS by IP to verify (`dig @<sidecar-ip> <name>.<suffix>`).
> The reconciler compares against the sidecar's **live** zone (not just an in-memory
> cache), so if a sidecar restarts and loses its zone the next pass re-writes it.
> The plain image has no cloud-init → no `dev` user / sshd; always use `images:debian/13/cloud` for tenant machines.
>
> ✅ **Client name resolution is automatic (no Tailscale Split DNS needed).** `sc login`
> gives each Tenant DNS Suffix its **own systemd-resolved link scope** on Linux: a
> persistent unit (`/etc/systemd/system/sandcastle-dns-<suffix>.service`) runs the
> **`sc dns-proxy` daemon**, which creates a dummy link `scdns-<hash>` with a 169.254/16
> link-local address (resolved only activates a link's DNS scope once it carries an
> address), listens on that address (UDP+TCP :53), pins `DNS=<that address>`
> `Domains=~<suffix>` to the link, and forwards every query to the tenant CoreDNS.
> The scope's server must be **on the dummy link itself**: resolved binds a link
> scope's UDP sockets to the link, so pointing the scope straight at the (off-link)
> tenant CoreDNS blackholed UDP — resolved silently degraded that server to TCP and
> then failed ONE lookup after every ~5-min idle period when it re-probed UDP (caught
> live on majestix 2026-07-08; see appendix). macOS uses `/etc/resolver/<suffix>`
> (natively per-domain, no daemon).
> Per-domain DNS routing in systemd-resolved only works ACROSS link scopes — the earlier
> global resolved.conf.d drop-in merged every tenant's server into one flat list where
> only the rotating "current server" is asked, so with two installs one zone always died
> (see appendix). The unit is `PartOf=systemd-resolved.service`, so a resolved restart
> re-applies the scope automatically. **PASS:** after `sc login`,
> `getent hosts <machine>.<project>.<suffix>` returns the machine's private IP with no
> manual config — **for every enrolled suffix simultaneously** — public names still
> resolve, `getent hosts <machine>.<project>.<other-suffix>` is NXDOMAIN (per-install
> isolation), and the FIRST lookup still succeeds both right after
> `systemctl restart systemd-resolved` (wait for the unit to be active again) and after
> a 10-minute DNS-idle period (the resolved re-probe window that used to fail).
> A **Tailscale Split DNS** entry (route `<suffix>` → the sidecar's tailnet IP) remains a
> valid fallback (e.g. if the client can't run privileged resolver setup).

> ✅ **The `sc` CLI now speaks the v2 topology (2026-07-04, validated on `igel`).**
> From an enrolled client, `sc list` shows every instance across the tenant's v2
> projects (flat FQDN `<machine>.<suffix>`, live DHCP IP; freeform `incus launch`
> machines included), `sc create dev` launches `images:debian/13/cloud` into the
> tenant's app project via the default profile (`--vm` for a VM, `--image` to
> override; prints IP + SSH hint, no auto-connect), lifecycle commands
> (`sc delete/start/stop/restart`) act on the freeform instances, and
> `sc incus`/`sc incus-native` scope to the v2 app project (`sc incus-infra` → the
> infra project).

### Phase 7c — `sc` CLI v2 machine lifecycle (from the enrolled client) ✅
Run the whole machine lifecycle through the `sc` CLI — this is the regression net
for the tenant-topology support (each step below failed at least once before being
fixed; see the appendix).

```bash
sc c lc1 -- hostname                           # connect CREATES a missing machine, waits for sshd, SSHes
sc list                                        # lc1 with flat FQDN lc1.<suffix> + live IP
sc incus exec lc1 -- sh -c '
  su - $LOGIN_USER -c "touch /workspace/ok"    # /workspace writable by the login user
  ls /home/$LOGIN_USER/.ssh/authorized_keys'   # $HOME on the shared volume with the key
sc stop lc1
sc c lc1 -- hostname                           # connect STARTS a stopped machine, then SSHes
sc delete lc1 --yes
sc list                                        # lc1 gone
```
**PASS (✅ validated on `igel` 2026-07-04):**
- `sc list` resolves the v2 tenant (no `Sandcastle tenant … not found`) and shows
  machines with `<name>.<suffix>` FQDNs and live IPs.
- The profile's login user (your client Unix username) exists in the machine, can
  **write `/workspace`**, and `~/.ssh/authorized_keys` lives on the shared `/home`.
- `sc c <machine>` creates a missing machine, starts a stopped one, waits for
  sshd, and lands an SSH session as the profile login user.
- `sc delete <machine> --yes` deletes the freeform instance (force-stops first);
  start/stop/restart work; `sc incus` targets the right project.

### Phase 7e — SSH host keys are authoritative, marked, and self-healing (ADR-0020) ✅
`sc c` reads each machine's host keys **over the Incus API** (`GetInstanceFile`),
records them in `~/.ssh/known_hosts` keyed by the Machine Private Hostname (via
`-o HostKeyAlias`), and connects with `StrictHostKeyChecking=yes`. Every line it
writes is tagged `# sandcastle:<remote>/<tenant>`. There is **no** per-tenant
known_hosts file for v2, and **no** IP-keyed entry is ever written.

```bash
M=hk1
sc c $M -- true                                # records ALL host key types, tagged
grep "^$M\." ~/.ssh/known_hosts                # one tagged line per key type

# the reported bug: rebuild the machine, its host key changes
sc delete $M --yes && sc c $M -- true          # must NOT warn; must reclaim the stale name
ssh -o BatchMode=yes $M.default.$SUFFIX true   # bare ssh works, by name
ssh -o BatchMode=yes $M.$SUFFIX true           # short alias too (default project, ADR-0018)

# convergence: bare ssh's UpdateHostKeys must find nothing to add
sc c $M -- true                                # silent — no known_hosts output at all

# maintenance
sc ssh-key purge --dry-run                     # reports, never writes
sc ssh-key purge --yes                         # drops tagged orphans + recycled-IP debris
```
**PASS (✅ validated on `obelix` 2026-07-09):**
- The connect prints `known_hosts: update <fqdn> … was <type> SHA256:…` exactly once
  after a rebuild, then connects. No `REMOTE HOST IDENTIFICATION HAS CHANGED`.
- `~/.ssh/known_hosts` holds **one tagged line per host key type**
  (`ssh-ed25519`, `ecdsa-sha2-nistp256`, `ssh-rsa`), each listing both names:
  `<m>.<project>.<suffix>,<m>.<suffix> … # sandcastle:<remote>/<tenant>`.
- Bare `ssh <m>.<project>.<suffix>` and `ssh <m>.<suffix>` both succeed with no prompt.
- A **second** `sc c` after a bare `ssh` prints nothing about known_hosts — the file
  has converged and OpenSSH's `UpdateHostKeys` has nothing to append. **If this
  regresses, only one key type is being recorded.**
- Untagged IP literals inside the tenant's private CIDR are removed (the CIDR is read
  from the machine's own interface netmask; a restricted cert cannot see the bridge).
  Entries for **other** tenants/installs, `@cert-authority`, `@revoked`, comments, and
  foreign hostnames are untouched.
- The first destructive write of the day leaves `~/.ssh/known_hosts.sc-backup-<date>`.
- A VM whose `incus-agent` is not running is *live but unreadable*: `sc c` falls back to
  `ssh-keyscan` and tags the line ` tofu`; `sc ssh-key purge` leaves its entries alone
  rather than treating them as orphans. A later connect that can read it drops the marker.
- `sc ssh-key purge --dry-run` does not modify the file (compare `shasum` before/after);
  without a TTY and without `--yes` it refuses.

### Phase 7d — second project via broker self-service
A tenant creates additional projects THEMSELVES through the broker's tenant
plane, authenticated by their enrolled client certificate. The broker scaffolds
the project (bridge wiring, default profile with the tenant's user + key, its
own shared `home`/`workspace` volumes) **and extends the tenant's restricted
certificate** to cover it — the admin shortcut (`sc-adm project create`)
scaffolds only.

```bash
# No flags needed after `sc login`: it records the broker URL
# (https://<tenant-gateway>:9443 — the host's :9443 reached over the subnet
# route) and the enrolled remote's client cert is used automatically.
sc project create web
# equivalent explicit form (pre-login enrollments, or overriding):
#   DIR=~/.config/sandcastle/sandcastle-$TENANT/incus
#   sc project create web --broker https://<gateway>:9443 \
#     --cert "$DIR/client.crt" --key "$DIR/client.key"

sc list                        # summary now lists project "web"
sc c web:dev1 -- hostname      # machine lifecycle scoped by project prefix
sc delete web:dev1 --yes
```
**PASS:**
- `sc project create web` works **with no flags** (broker URL from the saved
  login, cert/key from the enrolled remote) and returns the new project +
  writes the per-project remote (`<tenant>-web`) into the enrolled remote's
  incus config.
- `sc list`/`sc c web:dev1` work over the SAME certificate (extension applied,
  no re-login) — machines in `web` use that project's own shared volumes.

---

## Phase 7b — Expose a machine on `sc-edge` (public HTTPS) ✅
Create a machine with a name of your choice, run an app on `:3000`,
find its internal IP with the **admin** incus, and add an `sc-edge` vhost. `sc-edge`
lives in project **`infrastructure`** and reverse-proxies to the machine's internal IP
— the host routes between `incusbr0` and the tenant bridge, so `sc-edge` reaches
`10.249.0.x` directly. `*.scdev.thieso2.dev` is a CNAME to the sc-edge ingress, so any
`<name>.scdev.thieso2.dev` gets a **real Let's Encrypt** cert.

```bash
NAME=e2eweb
HOST=$NAME.scdev.thieso2.dev
# copy a VM image into the tenant project (own image store) + launch a VM
incus image copy big:73ad3e1133c5 big: --project default --target-project sc2-$TENANT-default --alias deb-vm-e2e 2>/dev/null || true
incus launch big:deb-vm-e2e big:$NAME --project sc2-$TENANT-default --vm
# find the internal IP via the ADMIN incus (this is where you read the forward target)
for i in $(seq 1 40); do VM_IP=$(INCUS_CONF=~/.config/incus-admin incus ls big: --project sc2-$TENANT-default \
  -c n4 --format csv 2>/dev/null | awk -F, "/^$NAME,/{print \$2}" | grep -oE '10\.249\.0\.[0-9]+' | head -1); [ -n "$VM_IP" ] && break; sleep 3; done
echo "$NAME -> $VM_IP"
# app on :3000
incus exec big:$NAME --project sc2-$TENANT-default -- bash -c '
  command -v python3 >/dev/null || { apt-get update -qq; apt-get install -y -qq python3; }
  echo hello-from-'$NAME'-VM-3000 > /root/index.html
  systemd-run --unit=app --collect python3 -m http.server 3000 --directory /root'
# wire sc-edge (project infrastructure) → VM:3000
incus exec big:sc-edge --project infrastructure -- bash -c "
  grep -q '$HOST' /etc/caddy/Caddyfile || printf '\n%s {\n    reverse_proxy http://%s:3000\n}\n' '$HOST' '$VM_IP' >> /etc/caddy/Caddyfile
  caddy validate --config /etc/caddy/Caddyfile && systemctl reload caddy"
```
**PASS (✅ validated):**
```bash
sleep 8
curl -s --resolve $HOST:443:65.21.132.31 https://$HOST/ -w '  [%{http_code} verify=%{ssl_verify_result}]\n'
# → hello-from-e2eweb-VM-3000  [200 verify=0]
echo | openssl s_client -servername $HOST -connect 65.21.132.31:443 2>/dev/null | openssl x509 -noout -issuer
# → issuer=… Let's Encrypt
```

---

## Phase 8 — Tenant DNS via CoreDNS (queried at the sidecar tailscale IP) ✅
The tenant sidecar's CoreDNS serves the `<suffix>` zone (`/etc/coredns/zones/db.e2etest`).
Machine A-records live there; query CoreDNS at the **sidecar's tailscale IP** — that's the
address the split-DNS will be served on to tailnet clients later.

```bash
TSIP=$(incus exec big:sc2-$TENANT --project sc2-$TENANT -- tailscale ip -4 | head -1)
# register machine A-records + bump SOA serial + reload (now auto-registered by the auth-app reconciler within ~30s; manual step optional)
incus exec big:sc2-$TENANT --project sc2-$TENANT -- bash -c '
  Z=/etc/coredns/zones/db.e2etest
  grep -q "^'$NAME' " $Z || echo "'$NAME' IN A '$VM_IP'" >> $Z
  sed -i "s/ SOA ns.e2etest. hostmaster.e2etest. [0-9]*/ SOA ns.e2etest. hostmaster.e2etest. 2/" $Z
  systemctl restart coredns; command -v dig >/dev/null || apt-get install -y -qq dnsutils'
# verify resolution AT THE TAILSCALE IP
incus exec big:sc2-$TENANT --project sc2-$TENANT -- dig @$TSIP $NAME.e2etest +short
```
**PASS (✅ validated):** `ns.e2etest → 10.249.0.3`, `web.e2etest → 10.249.0.211`,
`e2eweb.e2etest → 10.249.0.196` — correct IPs resolve at the CoreDNS tailscale IP.
> Note: the split-DNS binding over tailscale is future work; the e2e checks CoreDNS
> resolves the right IPs at its tailnet address.
> ⚠️ Machine A-records are **added manually** above — auto-registration on machine
> create is not built yet.

---

## Phase 8b — Install the tenant CA into local trust (Linux vs macOS) ⚠️
For the **private** HTTPS path (a tenant-CA-signed cert, as opposed to the public
Let's Encrypt cert from `sc-edge` in Phase 7), the client must trust the tenant CA.
The sandcastle utility installs it with **`sc trust install <tenant>`**
(`internal/localtrust`), and the mechanism **differs by OS**:

- **Linux:** writes the CA PEM to `/usr/local/share/ca-certificates/<name>.crt` and runs
  `update-ca-certificates`.
- **macOS:** `security add-trusted-cert -r trustRoot -k ~/Library/Keychains/login.keychain-db <ca.pem>`
  (system-wide uses `/Library/Keychains/System.keychain`). Uninstall:
  `security delete-certificate -c <trust-name>`.

```bash
sc trust install $TENANT            # installs the tenant CA into the OS trust store
sc trust install $TENANT --dry-run  # preview the per-OS plan without changing trust
```
**PASS (target):** after install, `curl https://<machine>.<suffix>/` over the private
tenant path validates **without `-k`**.

**v2 (ADR-0011):** the tenant CA is now issued by the **sidecar leaf signer**
(`sandcastle-admin sidecar tls-sign`), and `sc login` **auto-installs** it — the
v2 login branch fetches `http://<tenant .3>:9443/tls/ca` over the tenant subnet
route and installs it into the local trust store, naming the entry after the
CA's CN (`Sandcastle <suffix> tenant CA`) so same-named tenants of different
installs don't collide. The public `sc-edge` path in Phase 7 needs **no** CA
install (Let's Encrypt is already trusted).

---

## Phase 8c — Machine HTTPS ingress: Caddy + per-machine tenant leaf (ADR-0011) ✅

Every v2 machine terminates HTTPS locally with Caddy, using a per-machine leaf
signed by the tenant CA. The **sidecar** runs `sandcastle-admin sidecar tls-sign`
on `<tenant .3>:9443` — it self-generates the tenant CA on first start (key stays
on the sidecar) and signs leaves for names in its zone. The **default-profile
cloud-init** installs Caddy, fetches this machine's leaf (`GET
/tls/leaf?fqdn=<fqdn>` → `{cert,key}`) *before* starting Caddy, and serves:
HTTP→HTTPS redirect, `/_h`→browse the login user's `$HOME`, `/_w`→browse
`/workspace`, everything else reverse-proxied to `localhost:3000` (Host preserved,
so `*.<machine>` vhosts). Caddy runs as root.

```bash
# On a fresh machine, from any tailnet-connected client (CA already trusted by sc login):
curl -sS https://<machine>.<project>.<suffix>/            # 200, no -k → chains to tenant CA
curl -sI  http://<machine>.<project>.<suffix>/ | head -1  # 308 → https (redirect)
curl -so /dev/null -w '%{http_code}\n' https://<machine>.<project>.<suffix>/_h/  # 200 ($HOME)
curl -so /dev/null -w '%{http_code}\n' https://<machine>.<project>.<suffix>/_w/  # 200 (/workspace)
curl -sS https://foo.<machine>.<project>.<suffix>/         # wildcard vhost → :3000
```

Server-side sanity (no client trust needed):

```bash
# signer up + CA CN is suffix-scoped
incus exec sidecar --project <infra> -- systemctl is-active sandcastle-tls-sign
incus exec sidecar --project <infra> -- openssl x509 -in /etc/sandcastle/ca/ca.crt -noout -subject
#   → CN=Sandcastle <suffix> tenant CA
# leaf SANs cover the machine + wildcard
incus exec <machine> --project <default> -- openssl x509 -in /etc/sandcastle/tls/cert.pem -noout -ext subjectAltName
#   → DNS:<machine>.<project>.<suffix>, DNS:*.<machine>.<project>.<suffix>
```

**PASS (validated 2026-07-08 on idefix `ct2`):** signer self-generated
`Sandcastle idefix tenant CA`; `ct2` fetched a leaf with SANs
`[ct2.default.idefix, *.ct2.default.idefix]`; Caddy served valid HTTPS chained to
the CA (no `-k`), 308-redirected HTTP→HTTPS, proxied to `:3000`, vhosted
`*.ct2.default.idefix`, and (at the time) `/_r` browsed `/` while `/_w` browsed
`/workspace`. The file routes were later scoped to `/_h`→`$HOME` (+ `/_w`), which
also dropped the `/`-root bind-mount workaround.

---

## Phase 9 — Unattended login without GitHub ✅
Two short-circuits bypass the GitHub browser step for CI. **Prefer the simulated-auth
path** (Phase 9a): it needs no real OAuth app at all, is token-gated, and works for any
username. The older `--debug-device-user` hack (Phase 9b) is single-user and untoken.

> **`sc login` behavior (2026-07-04, validated live on the `igel` real deployment):**
> - **Idempotent.** With a saved CLI Auth Token for the same auth host that the
>   auth-app still accepts (`GET /api/tenants`) and an enrolled remote that
>   responds, `sc login` prints `Already logged in at …` and exits — no browser,
>   no new device code. `--force` re-authenticates.
> - **Tailnet precheck.** Unless `--skip-setup` is given, login REFUSES to start
>   the device flow when the machine is not a tailnet node (tailscale missing,
>   logged out, or stopped) — e2e clients must `tailscale up --accept-routes`
>   **before** `sc login`, or pass `--skip-setup`.
> - **Layered routing diagnosis.** The post-login verification prints one ✓/✗
>   line per layer (tailscale up → accept-routes → route offered/primary → probe
>   egresses via the client's own tailnet address) and halts with advice specific
>   to the first broken layer — including the "answered via local address …, NOT
>   the tailnet" case that catches overlapping local networks.

### Phase 9a — Simulated GitHub (recommended) ✅
If the auth-app was deployed with `--simulate-github-token` (Phase 1), one command does
the whole device login offline — no browser, no GitHub, no OAuth app:

```bash
rm -rf ~/.config/sandcastle/$TENANT
./bin/sc login https://$PUBIC_URL --simulate-token "$SIMULATE_TOKEN" --as thieso2 --skip-setup
```
`--simulate-token` drives `/oauth/github/simulate` (token-gated, constant-time compare),
which auto-allowlists `--as <user>`, approves the pending device code, and mints the
session. The auth-app then **provisions the v2 tenant directly** over its mounted host
socket (`EnsurePersonalTenant` → `ensurePersonalTenantV2` → `CreateTenantV2`).

**PASS (✅ validated on vm-thieso, 2026-07-03):** login output ends with `v2 tenant
thieso2 is ready.`, `Approved as thieso2.`, and `Remote "sandcastle-thieso2" enrolled.`
The tenant sidecar (CoreDNS + Tailscale) comes up on **stock `images:debian/13` pulled
from the remote** — no cached image, no `sandcastle/base`. `--skip-setup` skips the
client-side `RunPostLoginSetup` (DNS/trust/`tailscale up`).

### Phase 9b — Legacy `--debug-device-user` short-circuit ✅
The older hidden URL `/debug/device/approve`, enabled when the auth app runs with
`--debug-device-user <gh-user>`; `sc login --debug-approve` uses it. Single fixed user,
not token-gated — kept for back-compat.

```bash
# TEMPORARILY enable on the auth app (revert after!):
incus exec big:sc2-auth-app --project sc2-infra -- bash -c \
  "sed -i \"s/^SANDCASTLE_AUTH_DEBUG_DEVICE_USER=.*/SANDCASTLE_AUTH_DEBUG_DEVICE_USER='thieso2'/\" /etc/sandcastle/auth-app/env && systemctl restart sandcastle-auth-app"

rm -rf ~/.config/sandcastle/$TENANT
./bin/sc login https://$PUBIC_URL --debug-approve --skip-setup

# ALWAYS revert (security):
incus exec big:sc2-auth-app --project sc2-infra -- bash -c \
  "sed -i \"s/^SANDCASTLE_AUTH_DEBUG_DEVICE_USER=.*/SANDCASTLE_AUTH_DEBUG_DEVICE_USER=''/\" /etc/sandcastle/auth-app/env && systemctl restart sandcastle-auth-app"
```
**PASS (✅ validated):** `--debug-approve` auto-approves (no browser); same provisioning
path and end state as Phase 9a.

> ✅ **CIDR allocation (fixed).** Provisioning now derives occupancy with
> `tenant.CIDRAllocationInputs`, which scans **all** managed projects and reads the
> CIDR from both v1 (`kind=tenant`) and v2 (`kind=infra`, `user.sandcastle.v2.cidr`)
> tenants — so a new tenant never reuses another tenant's `/24`, and re-provisioning
> an existing tenant **reuses its own** `/24` (idempotent; via `PreferredCIDR`).
> Earlier this path allocated the pool's first `/24` every time and collided
> (`dnsmasq: failed to create listening socket for <gw>: Address already in use`).
> Still keep the broker and auth-app pools from overlapping **v1** or each other
> (broker `10.249.0.0/16`, auth-app a distinct clean range like `10.250.0.0/16`):
> the allocator sees tenant-owned bridges, not arbitrary foreign/orphaned ones.

---

## Phase 8d — Base images from a running machine (`sc image`) ✅

Turn a hand-customized machine into a reusable base, no image build recipe
(ADR-0019). Save snapshots the rootfs only — the shared `/home` and `/workspace`
volumes are attached devices and are excluded by `incus publish`.

```bash
# customize a machine however you like, then:
sc image save <machine> <name>          # snapshot rootfs → local base alias (machine keeps running)
sc image list                           # NAME / FINGERPRINT / SIZE / SOURCE / CREATED
sc create <new> --image <name>          # launch a fresh machine from the base (fast: packages baked)
sc image rm <name>                      # delete the base
```

Children are generalized on first boot (`sandcastle-generalize`, before sshd):
fresh SSH host keys + machine-id, stale TLS leaf dropped; the caddy-setup then
re-fetches a leaf and rewrites `machine.env`/Caddyfile for the new FQDN.

**PASS (validated 2026-07-08 on obelix):** planted a rootfs marker on `test`,
`sc image save test mybase` → 309.7 MB image (re-save replaced it idempotently),
`sc create probe --image mybase` launched in ~11 s. `probe` carried the marker,
got a fresh SSH host key + its own `probe.default.obelix` FQDN and `probe.*` TLS
leaf; the corrected machine-id reset was validated live (old→new random id).

---

## Summary
**Green today (validated live 2026-07-02):** Phases 0–9 — teardown, auth-app deploy, sc-edge
front, v2 tenant provision (stock Debian sidecar: CoreDNS + Tailscale), client enrollment,
remote switch, profile (SSH+cloud-init), CT **and** VM launched via `incus launch` with DNS +
SSH, a machine **exposed on sc-edge over public HTTPS with a real LE cert** (7b),
**tenant DNS resolving at the CoreDNS tailscale IP**, and **unattended login provisioning the
v2 tenant** (v2-only `EnsurePersonalTenant`). Tailscale-readiness race is **fixed**; the v1
Personal-Tenant login path is **removed**. The one non-green step is **Phase 8b** (private
tenant-CA trust), which stays ⚠️ pending the unbuilt per-tenant tenant-CA cert path.

**Remaining work to make every phase green:**
1. **Machine DNS auto-registration** (Phases 7/8) — write the A-record into `db.e2etest` on machine create (done manually in the e2e today).
2. **Per-tenant tenant-CA cert path** (Phase 8b) — issue a tenant CA + leaf so the *private* HTTPS path exists; then `sc trust install` (Linux/macOS) makes it trusted. (The *public* sc-edge LE path in Phase 7 already works with no CA install.)
3. **Split-DNS over tailscale** (Phase 8) — serve the tenant zone to tailnet clients on the sidecar tailscale IP.
4. ~~**Shared `$HOME` + `/workspace`** (Phase 7)~~ — ✅ done: both per-project volumes are created and attached in the default profile (tenant + later app projects).
5. **Deploy-command polish** — local-image default; fix the multi-address remote mangling so Phases 1/3 run via the CLI, not by-hand. (CIDR allocation across v1+v2 tenants and idempotent re-provision are now fixed — see the appendix.)

Phases 0–9 are validated green today; the items above are refinements, not blockers.

---

## Appendix — Problems encountered & fixes (keep updating)
Log every problem hit while running this e2e and how it was resolved, so the runbook
stays truthful and self-healing.

| Problem | Root cause | Fix |
|---|---|---|
| Appliance services won't start (`systemctl` "not booted with systemd") | Launched from an **OCI** image (app container, no systemd PID1) | Use a **CONTAINER-type systemd** image (`d31c34fadc08` / `images:debian/13`) |
| Caddy re-issues certs / ignores copied ones | Under systemd Caddy has no `$HOME` → wrong storage dir | Pin `storage file_system /root/.local/share/caddy` in the Caddyfile |
| `sc`/`sc-adm` against a multi-address remote → `lookup <addr>,https: no such host` with a concatenated address | **Solved (2026-07-04):** newer incus CLIs store a multi-address token enrollment as a comma-joined `addr` plus `last_working_address`; the vendored incus SDK's `cliconfig` predates that format and treats the joined string as one URL — the `incus` binary works while every SDK-based `sc` call fails | **Fixed:** `incusx.LoadCLIConfig` (used by every SDK connection) normalizes multi-address remotes to `last_working_address` (else the first address) |
| `incus file push` → `text file busy` | Overwriting the binary the appliance is running | `systemctl stop` the service, push, `start` |
| `incus launch big:<fp>` in the tenant project → "Image not found" | Tenant `default` project has its own image store (`features.images=true`) | `incus image copy … --target-project sc2-<tenant>-default` first |
| Sidecar has no CoreDNS/Tailscale | Neither is in Debian apt | CoreDNS **binary** download; Tailscale **official apt repo** (`installV2SidecarPackages`) |
| Sidecar Tailscale shows `Logged out` after create | `tailscale up` ran before `tailscaled` was ready | Readiness wait + retry + `tailscale ip -4` gate in `v2TailscaleUp` |
| `:3000` server won't start / `setsid` hangs `incus exec` | Minimal image lacks `python3`; detached process blocks the exec stream | `apt-get install python3`; start via `systemd-run --unit=app --collect` |
| Machine name doesn't resolve in CoreDNS | No A-record auto-registration on machine create yet | Add the record to `db.e2etest` + bump SOA + `systemctl restart coredns` (auto-reg TODO) |
| Tenant machine SSH refused / no `dev` user | Plain image has no cloud-init → profile user-data never applied | Launch tenant machines from `images:debian/13/cloud` (has cloud-init) |
| `create` / appliance deploy → `Image not provided for instance creation` | Historic: the launch code only resolved *local* aliases/fingerprints, so a stock `images:debian/13` ref wasn't pulled | **Fixed:** `imageInstanceSource` now turns an `images:…`/`ubuntu:…` ref into a simplestreams **pull** — stock images work with no pre-caching. (Bare alias/fingerprint still means a local image; the Incus server needs outbound access to `images.linuxcontainers.org`.) |
| Second v2 tenant → `dnsmasq: failed to create listening socket for <gw>: Address already in use` | Occupancy was `tenant.List`+`OccupiedCIDRs`, which only surfaces v1 `kind=tenant` projects; v2 tenants (`kind=infra`) were invisible, so the allocator re-picked the pool's first `/24` | **Fixed:** `tenant.CIDRAllocationInputs` scans v1+v2 projects for occupancy; also point pools clear of v1 (`10.248.x`) as defense-in-depth |
| Re-provision/login of an existing tenant → `Device IP address … not within network … subnet` | The v2-aware occupancy fix counted the tenant's OWN `/24` as occupied and allocated a fresh one that didn't match the existing bridge | **Fixed:** `CreateRequest.PreferredCIDR` reuses the tenant's existing `/24` (idempotent) |
| Auth-app tailnet key ignored / sidecar `Logged out` | `SANDCASTLE_AUTH_TAILSCALE_AUTHKEY` line in `/etc/sandcastle/auth-app/env` was mangled by repeated `sed` edits (concatenated key + var name) | Rewrite the line cleanly from `.env.sc2`: `sed -i "8s\|.*\|SANDCASTLE_AUTH_TAILSCALE_AUTHKEY='<key>'\|" env`, then restart |
| `sc login`/`ssh` needs the running fat binary on appliances | Appliances still ran a pre-v1-removal binary | `systemctl stop`, `incus file push bin/linux-amd64/sandcastle …/usr/local/bin/sandcastle-admin`, `systemctl start` on `sc2-broker` **and** `sc2-auth-app` |
| SSH `Too many authentication failures` reaching a tenant machine | The local ssh-agent offered many keys before the right one; server cut off at 6 | Add `-o IdentitiesOnly=yes -i ~/.ssh/sandcastle_ed25519` |
| `enroll` enrolls the base remote but project remote fails with `Error: EOF` | `--incus-endpoint` **defaults to `https://big.thieso2.dev:8443`** — wrong/unreachable on any other Incus host | Re-run `sc enroll <tenant> --incus-endpoint https://<host>:8443` (idempotent, no token needed on re-run) |
| `incus config device add <ct> tun unix-char …` on a RUNNING client CT → `Failed to add mount for device inside container` | tun unix-char hot-plug into a live CT fails (Incus 7.2) | `incus stop <ct>` → `device add` → `start` (cold-add works) |
| `dig @127.0.0.1 …` in the sidecar silently empty | stock Debian sidecar has no `dig` (dnsutils not installed by provisioning) | `apt-get install -y dnsutils` in the sidecar before DNS PASS checks (Phase 8 already does; Phase 7 checks need it too) |
| Two LIVE sidecars (different hosts/runs) both hold `enabledRoutes` for the SAME `/24` (e.g. both auth-app stacks allocate `10.250.0.0/24` first) | Every deployment uses the same default CIDR pools, and prune-on-approve only removes *same-hostname* stragglers — a sibling environment's online router survives | Verify your sidecar owns `Self.PrimaryRoutes` after provisioning; for parallel test envs use distinct pools per host, ephemeral tailnet keys, or tear down the sibling before the run |
| Client's ping to the tenant gateway succeeds (sub-ms!) yet login's routing check fails | ANOTHER deployment's bridge on the client's LAN path uses the same `/24` — the "reply" came from the local network, not the tenant (the check rejects non-tailnet egress by design) | Delete/renumber the colliding stale bridges (`incus network delete <sc2-…>` on the old host); the diagnosis now prints the answering local address |
| Making a subnet-resident machine a tailnet node breaks its OLD inbound reachability | Asymmetric routing: callers still reach it via the other router's subnet route, but its replies now leave via its own `tailscale0` and the caller's tailscaled drops them (source not in that node's allowed IPs) | Don't dual-home test clients: a client VM is EITHER reached via someone's subnet route OR is a tailnet node. For the e2e use a dedicated client VM that is a node (see the two-VM note) |
| `sc list`/`sc create`/`sc incus` on a v2 tenant → `Sandcastle tenant … not found` / `permission for project "sc-<tenant>"` | The v1 command family resolved tenants via `kind=tenant` projects and `sc-<tenant>` naming; v2 tenants have neither | **Fixed:** `tenant.List` surfaces v2 tenants from their `kind=project, version=2` Incus projects; `sc create` launches stock cloud images for v2 (`--image`/`--vm`); `sc incus*` scope to the v2 project names |
| `sc login --force` on a v2 tenant WITH machines → `reconcile User SSH Public Key … Instance not found: default-dev3` | Making v2 tenants visible to `tenant.List` armed the auth-app's v1 per-machine key reconciler, which uses v1 `<project>-<machine>` instance naming | **Fixed:** v2 tenants skip the v1 reconcile/stamp (the key lives in the profile; rotation reaches machines via the shared /home) |
| `sc delete <machine>` on a v2 tenant → `Sandcastle tenant … not found` even though `sc list` works | `filterTenantProjects` (tenant-filtered store used by lifecycle/plan paths) dropped every non-`kind=tenant` project, so v2 tenants vanished before the v2 branch could run | **Fixed:** the filter keeps `kind=project`/`kind=infra` projects whose `user.sandcastle.tenant` matches; `sc delete/start/stop/restart` now act on v2 freeform instances (Phase 7c) |
| Login user cannot write `/workspace` (`drwx--x--x root root`) | The shared volume was created with default root ownership; nothing chowned it | **Fixed:** volumes are created with `initial.uid/gid=2000, initial.mode=0775`; pre-existing tenants: one-time `chown 2000:2000 /workspace && chmod 0775 /workspace` from any machine (shared volume → fixes all) |
| On a host WITHOUT idmapped mounts (e.g. a `dir` storage pool), a **VM** cannot SSH when a CT wrote the shared `/home` first — the VM sees it owned by the CT idmap (`1002000`) and sshd StrictModes rejects it | idmapped mounts (needed for `security.shifted`) are absent, so a shared `/home` cannot show consistent ownership to both a CT and a VM | **Fixed:** on such hosts the shared `/home` device is omitted from the default profile (machines get a normal local `/home`, so SSH always works); `/workspace` stays shared. Enable idmapped mounts (a zfs/btrfs pool) for a shared `/home` across CT + VM. |
| SSH into a **VM** fails `Permission denied (publickey)` while the CT works; VM sees `/home/dev` owned by `1002000` | CT writes to the shared volume through its idmap; without `security.shifted` a VM (virtiofs, no shift) sees raw shifted owners → sshd StrictModes rejects the foreign-owned `~` | **Fixed:** shared volumes are created with `security.shifted=true`; pre-existing tenants: stop machines, `incus storage volume set default home security.shifted=true` (and `workspace`), start, then `chown -R 2000:2000 /home/<user>` once from a VM (raw view) |
| `sc list`/`sc c` on a enroll-enrolled client → `tenant is required` | `enroll` never persisted `tenant`/`remote` into `~/.config/sandcastle/config.yml` (only the login path did) | **Fixed:** `enroll` saves the tenant + base remote as local defaults |
| Second `sc login` re-runs the whole device flow instead of `Already logged in` | `/api/tenants` filtered accessibility through the v1 `ListTenantUsers` metadata, which v2 tenants don't have — the saved-token check concluded the tenant was "no longer accessible" | **Fixed:** a v2 personal tenant is accessible to the user whose key names it; also fixes `sc tenant list` for v2 |
| Tenant CIDR ignores `bootstrap --cidr-pool` when created via `incus exec … tenant create` | The flag lands in the broker **service** env (`/etc/sandcastle/broker/env`), but a direct `incus exec` CLI call doesn't inherit an EnvironmentFile | Source it in the exec: `incus exec sc2-broker … -- sh -c '. /etc/sandcastle/broker/env && export SANDCASTLE_CIDR_POOL && sandcastle-admin tenant create …'` |
| `sc create dev` succeeds but the machine does NOT show in `sc list` (two installs, same tenant name on one daemon) | `sc list` (also `sc project`, `sc dns/trust`, `sc status`) resolved the tenant by NAME only over the unscoped `tenant.List` — the other install's same-named tenant sorts first and shadows this one; only create/connect/lifecycle/incus were install-scoped | **Fixed:** those commands resolve via `scopedListTenants` (`tenant.ListForPrefix` keyed on the current remote's install prefix); regression test `TestListMachinesScopedToCurrentInstall` |
| Shared `/home` silently NOT shared (VM can't see the CT's `/home` writes); `home` volume exists but is attached to no profile | `SupportsIdmappedMounts` keyed on `kernel_features["idmapped_mounts"] == "true"`, and **Incus 7.x stopped populating `kernel_features`** (always `{}`) — so every 7.x host looked idmapped-less and provisioning omitted the shared `/home` (and created both volumes unshifted) | **Fixed:** an ABSENT `idmapped_mounts` entry now means supported (the Incus 7.x kernel floor ≥ 5.15 includes it); only an explicit `"false"` disables the shared `/home`. Regression test `TestKernelFeaturesSupportIdmappedMounts`. **e2e check:** after provisioning, `incus profile show default --project <prefix>-<tenant>-default` lists BOTH `home` and `workspace` devices and both volumes have `security.shifted=true` |
| `sc login --force --dns-suffix other` prints the immutable-suffix error, then hangs polling for ~10 min ("device login polling timed out") while the server re-attempts provisioning every poll | A provisioning failure always left the device login `pending` (correct for transient bring-up errors, wrong for deterministic user-input errors) | **Fixed:** terminal errors (`tenant.TerminalProvisionError`: immutable-suffix conflict, rejected suffix) DENY the device login; the client fails fast with `device login denied: <message>` and exit 1. Regression test `TestDevicePollDeniesLoginOnTerminalProvisioningError` |
| Two installs on one client (Linux): only ONE suffix ever resolves via `getent` — direct `dig @<sidecar>` works for both, and which zone dies varies | Per-domain DNS routing in systemd-resolved only works ACROSS link scopes; the global resolved.conf.d drop-ins merged both tenant servers + public upstreams into ONE flat list where only the rotating "current server" is asked — an authoritative NXDOMAIN from the wrong server ends the lookup (and the tenant servers' REFUSED responses rotate the current server onto the public upstream, killing both zones) | **Fixed:** per-suffix link scopes — `sandcastle-dns-<suffix>.service` creates a dummy link (`scdns-<hash>`, 169.254/16 addr — no address = no active scope) pinned to `DNS=<CoreDNS> Domains=~<suffix>`, `PartOf=systemd-resolved.service` so a resolved restart re-applies it; login removes any legacy drop-in. The sidecar CoreDNS also now REFUSES tailnet (100.64/10) sources outside its zone instead of forwarding them upstream (`acl` block) — machines on the bridge keep recursion. Tests: `TestSystemdResolvedUnitCreatesPerSuffixLinkScope`, `TestRenderInitial` |
| `sc-adm tenant delete <v2-tenant> --yes` prints "Deleted runtime resources … durable state preserved" but deletes NOTHING (all v2 projects, machines, sidecar, bridge survive) | `PlanDelete` is v1-shaped: it computes `sc-<tenant>` resource names that don't exist for a v2 tenant, and every per-resource delete is ignore-not-found — a silent no-op reported as success | **Fixed:** `tenant delete` detects a v2 tenant (install-prefix-scoped, `PlanDeleteV2`) and tears down app projects (machines, images, shared volumes, profiles), infra project (sidecar), and bridge via `DeleteTenantV2`; without `--purge` it REFUSES (v2 deletion is all-or-nothing — the shared volumes live in the app projects). Validated live on the `id` install with the `sc2` install's same-named tenant untouched. Tests: `TestPlanDeleteV2` |
| First `sc login` fails at the device poll with `store User SSH Public Key … database is locked (5) (SQLITE_BUSY)` | The new svclog sink writes a request/span row per request into the SAME SQLite DB as the device poll; `OpenDatabase` configured pragmas via a one-off `Exec` (one pooled connection only) and left the default rollback journal with no busy timeout | **Fixed:** pragmas moved into the DSN so every pooled connection gets them — `busy_timeout(10000)`, `journal_mode(WAL)`, `foreign_keys(1)`, `synchronous(NORMAL)`. Test: `TestOpenDatabaseConfiguresWALAndBusyTimeoutOnEveryConnection` |
| Client `getent hosts <machine>.<project>.<suffix>` fails ONE lookup after every resolved restart AND after every ~5-min DNS-idle period, while direct `dig @<CoreDNS>` always works; journal shows `Using degraded feature set UDP…`/`…TCP…` for the tenant server | systemd-resolved binds a link scope's UDP sockets to the scope's interface; the Sandcastle scope lives on a **dummy** link, so resolved's UDP queries to the off-link tenant CoreDNS were transmitted into the dummy and dropped (tcpdump: zero packets on any real interface). resolved only worked after silently degrading the server to TCP — and it re-probes UDP after each idle grace period, failing the caller's lookup each time | **Fixed:** the per-suffix unit now runs the long-lived **`sc dns-proxy`** daemon: it owns the dummy link, listens on the link's OWN 169.254 address (bound-to-link delivery of an on-link address works), pins the resolved scope to that address, and forwards UDP+TCP to the tenant CoreDNS over normal routing — UDP+EDNS0 works natively, no degradation ladder at all. Tests: `TestSystemdResolvedUnitCreatesPerSuffixLinkScope`, `TestServeDNSProxyForwardsUDPAndTCP` |
| Two installs, one client, URL-named remotes (`sc-<install-label>`): `sc list`/`sc incus`/`sc status` under install A's remote show install B's machines/project (`INCUS_PROJECT=<other-install>-…`) | `installPrefixFromRemoteName` only inverts the LEGACY remote shape `sc-<prefix>-<tenant>`; the URL-based names carry no prefix, so it returned "" and every tenant lookup ran **unscoped** — the other install's same-named tenant won, resurrecting the cross-install shadowing | **Fixed:** the install prefix is now derived from the remote's **pinned project** in the shared incus config (`remotes[<remote>].project` = `<prefix>-<tenant>[-<app>]`), with the legacy name-shape inversion as fallback. Test: `TestInstallPrefixFromRemotePinnedProject` |
| `sc-adm tenant create` on a host with the base image already cached returns success in ~5s — but the sidecar has NO CoreDNS/Tailscale (unit `failed`, `/usr/local/bin` empty); every freeform-tenant DNS/SSH check then fails. Re-running create "fixes" it | TWO stacked defects. (1) The SDK's `op.Wait()` on an exec operation only fails when the OPERATION fails — a script that ran and exited nonzero still "succeeds" (the exit code is in `Metadata["return"]`), so every `execSidecar` failure was silently swallowed. (2) With a cached image the container reaches RUNNING in ~1s, and the package-install exec raced the boot: apt had no network yet and failed — invisibly, per (1). A slow first-time image pull (the login-path timing) let the container boot first, masking the race | **Fixed:** `execExitError` surfaces nonzero command exits from every sidecar exec (install, configure, tailscale, CoreDNS restart), and `ensureV2Sidecar` now ends with `waitV2SidecarBoot` — an in-container wait for `systemctl is-system-running` ∈ {running, degraded} and a tenant IPv4 on eth0 — before any provisioning exec. Test: `TestExecExitErrorSurfacesNonzeroCommandExit` |
| Machine record present in the sidecar zone FILE and `coredns` active, yet the name doesn't resolve for up to ~1 min (which check flakes varies run to run) | The DNS reconciler's post-write CoreDNS reload exec ran with no stderr capture and no exit-code check — a failed reload (sidecar mid-provisioning, unit not yet installed) was invisible, and the next pass's live-file compare then SKIPPED the reload forever; CoreDNS's file-plugin ~1-minute reload timer was what eventually served the zone | **Fixed:** the reload exec captures stderr and propagates the command exit code (`execExitError`); a failed reload now surfaces in the auth-app log and leaves `lastZone` unset so the pass retries |
| `scripts/e2e-v2.sh`: RED exit before the summary, spurious canonical-name PASS (an `fe80::` from the machine itself), and its cleanup leaving app projects + the tenant bridge behind (poisoning the next run's CIDR allocation with `dnsmasq: Address already in use`) | Three script defects: `set -o pipefail` killed the script on the first non-resolving `getent` in a pipeline; `getent hosts` prefers AAAA and nss-myhostname answers the machine's OWN canonical name; the manual cleanup predated the v2 shared home/workspace volumes (a project won't delete while they exist) and its stack check predated `sc-adm install`'s `sc2-infra` project | **Fixed:** resolver checks use `getent ahostsv4` with `|| true` guards and 60s patience; cleanup calls `sc-adm tenant delete --yes --purge` first and its manual sweep detaches+deletes the shared volumes; the stack check accepts the auth-app in `sc2-infra` or legacy `infrastructure` |

---

## Appendix — Nested full-stack e2e (client CT drives the tenant via `sc login`)

A self-contained run where **one VM hosts the whole stack** (Incus + sc-edge +
auth-app + broker + the tenant sidecar + tenant machines) and **one nested
container is the tenant's client**, connecting with `sc login` and then exercising
the full tenant workflow. Validated on `sc2nest-vm1` (Debian 13, 4 vCPU / 16 GiB,
nested KVM) fronted by a Cloudflare tunnel.

### Host: latest Incus (Zabbly), not Debian's LTS
Debian 13 apt ships Incus `6.0.4` (LTS). For the newest feature release use the
Zabbly repo — this run used **Incus 7.2**:
```bash
sudo mkdir -p /etc/apt/keyrings
sudo curl -fsSL https://pkgs.zabbly.com/key.asc -o /etc/apt/keyrings/zabbly.asc
sudo tee /etc/apt/sources.list.d/zabbly-incus-stable.sources >/dev/null <<SRC
Enabled: yes
Types: deb
URIs: https://pkgs.zabbly.com/incus/stable
Suites: trixie
Components: main
Architectures: amd64
Signed-By: /etc/apt/keyrings/zabbly.asc
SRC
sudo apt-get update && sudo apt-get install -y incus
sudo usermod -aG incus-admin "$USER" && sudo incus admin init --minimal
```
Then deploy sc-edge (tunnel-only), auth-app, and broker as in Phases 1–2. The VM's
own `/dev/kvm` lets the tenant create **nested VMs** (`incus launch … --vm`).

### Client CT: prerequisites
The nested client container needs three things:
- **`incus-client`** — `sc login` shells out to `incus remote add` (`apt-get install -y incus-client`).
- **`tailscale`** with a **`/dev/net/tun`** device — the enrolled Incus remote is the
  sidecar's *tailnet* IP (ADR-0017), so the client must be on the tenant's tailnet:
  ```bash
  incus config device add <client> tun unix-char path=/dev/net/tun
  incus config set <client> security.nesting=true && incus restart <client>
  # inside the CT:
  curl -fsSL https://tailscale.com/install.sh | sh
  tailscale up --auth-key=$TAILSCALE_AUTH_KEY --accept-routes --hostname=<client>
  ```
- the **`sc`** binary (`incus file push bin/linux-amd64/sandcastle <client>/usr/local/bin/sc`).

### The run (from inside the client CT)
```bash
sc login https://$PUBIC_URL --simulate-token "$SIMULATE_TOKEN" --as thieso2 --skip-setup
export INCUS_CONF=~/.config/sandcastle/sandcastle-thieso2/incus
incus list --project sc2-thieso2-default                     # empty — reaches the sidecar over tailnet
incus launch images:debian/13/cloud deva --project sc2-thieso2-default        # CT
incus launch images:debian/13/cloud devb --project sc2-thieso2-default --vm   # nested VM
# … dig @<sidecar-bridge-ip> deva.thieso2 ; ssh dev@<ip> ; curl http://<ip>:3000 ; /workspace shared …
incus delete -f deva devb --project sc2-thieso2-default
```

**PASS (✅ validated):**
- `sc login` provisions the tenant + sidecar and enrols the remote at
  `https://<sidecar-tailnet-ip>:8443`; `incus list/launch/delete` of both a **CT and a
  nested VM** work over that remote.
- **DNS** auto-registration: `deva.thieso2`/`devb.thieso2` resolve at the sidecar CoreDNS.
- **SSH** as `dev` and an **HTTP app on :3000** respond on each machine.
- **Shared `/workspace`** — a file written on the CT is read on the VM and vice-versa.

### ⚠️ The one gating requirement: approve the tenant subnet route
The client reaches tenant machines (`10.x.x.N`: SSH, HTTP, and CoreDNS at `.3`) **only
once the sidecar's advertised `/24` subnet route is approved** on the tailnet. Every
sidecar is tagged **`tag:sandcastle`**, so the clean fix is the `autoApprovers` ACL
(see Prerequisites) — then the route approves the moment the sidecar advertises it, and
the client (with `--accept-routes`) reaches every machine. Without approval the sidecar's
own IP (`.3`) is reachable but machines behind it are not (`No route to host`), and
`sc login` (without `--skip-setup`) correctly **halts** at the routing check. Alternative:
approve manually in the Tailscale admin console. (There is deliberately no server-side
API-key path — approval is a tenant action on the tenant's tailnet.)

> **Stale sidecar devices blackhole an approved route.** Each teardown+rebuild (or a
> re-register) leaves the previous sidecar as a **dead tailnet device with the same
> hostname**, still advertising the tenant's `/24`. With several duplicates, Tailscale's
> subnet-router primary election can pick an **offline** one — so the route reads as
> approved yet the client still gets `No route to host`. When approving by hand,
> delete the old `sc2-<tenant>` devices in the admin console (or use an **ephemeral**
> auth key so they self-remove when offline). Symptom to recognise: the device's
> `enabledRoutes` shows the `/24` but the node's `Self.PrimaryRoutes` is `null`.

> **Co-located caveat:** in this all-in-one-VM topology the machines are also directly
> reachable from the **VM host** (host routing to the tenant bridge), which is handy for
> validation; but a *real* remote client depends on the approved subnet route, so that is
> what this appendix tests.
