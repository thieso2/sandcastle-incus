# sc2 End-to-End Test Protocol

A re-executable runbook that validates the full sc2 (v2) feature set: appliance
deploy → tenant provisioning (stock Debian sidecar) → client enrollment → machine
+ DNS → per-tenant HTTPS. Every step lists the command and the **PASS** criterion.

Run it top-to-bottom. **Phase 0 (teardown)** makes it idempotent — re-running
starts from a clean slate.

## Status legend
- ✅ **validated** live on `big` (2026-07-01)
- ⚠️ **partial** — works but has a known rough edge (noted inline)
- 🚧 **to build** — feature not implemented yet; step documents the target

## Prerequisites
- Run from a host with the **admin** Incus remote: `export INCUS_CONF=~/.config/incus-admin` (remote `big`).
- The one **fat binary** built static for linux: `bin/linux-amd64/sandcastle` (`make build` with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 BIN_DIR=bin/linux-amd64`). It is copied into every appliance.
- A **stock systemd base image** present locally on `big` — Debian trixie **container** (`d31c34fadc08`). No custom `sandcastle/base` image is used.
- `.env.sc2` at repo root with: `GH_CLIENT_ID`, `GH_CLIENT_SECRET`, `PUBIC_URL=sc2.thieso2.dev`, `TAILSCALE_AUTH_KEY`.
- Public DNS: `sc2.thieso2.dev` → the host's public IP (`65.21.132.31`).
- `TENANT=e2etest` throughout.

```bash
cd /workspace/sandcastle-incus
export INCUS_CONF=~/.config/incus-admin
set -a; . ./.env.sc2; set +a
TENANT=e2etest
SSHKEY=$(cat ~/.ssh/sandcastle_ed25519.pub)
IMAGE=d31c34fadc08          # stock debian trixie container
```

---

## Phase 0 — Teardown (idempotent reset) ✅
Remove any prior test-tenant server state + local client config so the run starts clean.

```bash
# server: delete the test tenant's instances + projects + bridge
for p in sc2-$TENANT-default sc2-$TENANT; do
  incus list big: --project $p -c n --format csv 2>/dev/null | while read -r n; do
    incus delete -f big:$n --project $p 2>/dev/null; done
  incus profile delete big:default --project $p 2>/dev/null || true
  incus project delete big:$p 2>/dev/null || true
done
incus network delete big:sc2-$TENANT 2>/dev/null || true
# client: wipe the generated local incus config
rm -rf ~/.config/sandcastle/$TENANT
```
**PASS:** no `sc2-e2etest*` projects remain (`incus project list big: | grep e2etest` is empty).

---

## Phase 1 — Deploy the Auth App appliance ✅
The sc2 web API. No host port (fronted by `sc-edge`). Copies the fat binary in.
Uses `.env.sc2` for all answers (GitHub OAuth); a stock image (no `--base-image` needed).

```bash
sc-adm auth-app deploy \
  --auth-hostname "$PUBIC_URL" \
  --github-client-id "$GH_CLIENT_ID" \
  --github-client-secret "$GH_CLIENT_SECRET" \
  --admin-github-users thieso2 \
  --base-image $IMAGE
```
> Note (⚠️): running the wrapper against a multi-address remote can mangle the
> address list. Deployed here by hand into `d31c34fadc08` in project
> `infrastructure` as `sc2-auth-app` (systemd unit `sandcastle-auth-app`,
> listening `:9444`). Both paths yield the same appliance.

**PASS:** `incus exec big:sc2-auth-app --project infrastructure -- systemctl is-active sandcastle-auth-app` → `active`, listening `:9444`.

---

## Phase 2 — Front it on `sc-edge` (public HTTPS, LE cert, no client certs) ✅
Add a terminate vhost so `https://sc2.thieso2.dev` reverse-proxies to the auth app.

```bash
AUTH_IP=$(incus exec big:sc2-auth-app --project infrastructure -- \
  ip -4 -o addr show eth0 | grep -oE '10\.196\.38\.[0-9]+' | head -1)
incus exec big:sc-edge --project infrastructure -- bash -c "
  grep -q '$PUBIC_URL' /etc/caddy/Caddyfile || printf '\n%s {\n    reverse_proxy http://%s:9444\n}\n' '$PUBIC_URL' '$AUTH_IP' >> /etc/caddy/Caddyfile
  caddy validate --config /etc/caddy/Caddyfile && systemctl reload caddy"
```
**PASS (all ✅ validated):**
```bash
curl -s --resolve $PUBIC_URL:443:65.21.132.31 https://$PUBIC_URL/healthz -o /dev/null -w '%{http_code}\n'   # 200
curl -s --resolve $PUBIC_URL:443:65.21.132.31 -D - https://$PUBIC_URL/login/github -o /dev/null | grep -i location   # 302 → github.com …client_id=<GH_CLIENT_ID>
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
  /usr/local/bin/sandcastle-admin tenant create-v2 $TENANT \
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
./bin/sc connect-v2 $TENANT --token "$TOKEN"
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
**PASS (✅ validated):** `cloud-init.user-data` contains user `dev` with
`ssh_authorized_keys: [ <your key> ]`, installs `openssh-server`, and
`runcmd: [systemctl, enable, --now, ssh]`.

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
# register A-records in the sidecar CoreDNS (auto-registration on create is TODO)
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
> ⚠️ Auto-registration of machine A-records on create is TODO (added manually above).
> The plain `d31c34fadc08` image has no cloud-init → no `dev` user / sshd; always use `images:debian/13/cloud` for tenant machines.

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
# register machine A-records + bump SOA serial + reload (auto-registration on create is TODO)
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
⚠️ **Depends on the per-tenant tenant-CA cert path**, which the v2 sidecar does not yet
issue (see remaining work). The public `sc-edge` path in Phase 7 needs **no** CA install
(Let's Encrypt is already trusted).

---

## Phase 9 — Unattended login via the debug short-circuit ✅ (mechanism) / ⚠️ (provisioning)
The hidden debug URL already exists: `/debug/device/approve`, enabled when the auth
app runs with `--debug-device-user <gh-user>`; `sc login --debug-approve` uses it to
bypass the GitHub browser step for CI.

```bash
# TEMPORARILY enable on the auth app (revert after!):
incus exec big:sc2-auth-app --project infrastructure -- bash -c \
  "sed -i \"s/^SANDCASTLE_AUTH_DEBUG_DEVICE_USER=.*/SANDCASTLE_AUTH_DEBUG_DEVICE_USER='thieso2'/\" /etc/sandcastle/auth-app/env && systemctl restart sandcastle-auth-app"

rm -rf ~/.config/sandcastle/$TENANT
./bin/sc login https://$PUBIC_URL --debug-approve --skip-setup

# ALWAYS revert (security):
incus exec big:sc2-auth-app --project infrastructure -- bash -c \
  "sed -i \"s/^SANDCASTLE_AUTH_DEBUG_DEVICE_USER=.*/SANDCASTLE_AUTH_DEBUG_DEVICE_USER=''/\" /etc/sandcastle/auth-app/env && systemctl restart sandcastle-auth-app"
```
**PASS (✅ validated):** device flow starts, `--debug-approve` auto-approves (no browser).
⚠️ **Provisioning-on-login pending:** the auth app currently runs **v1** `EnsurePersonalTenant`
which fails on a stale `sandcastle/base:latest` alias. **Fix pending (decided):** on
login, the auth app **delegates to the broker** (`TenantProvisionerAdapter` →
`CreateTenantV2`) so login provisions the same v2 default-project + sidecar as Phase 3,
then the existing client `RunPostLoginSetup` does DNS/trust/`tailscale up`.

---

## Summary
**Green today (validated live):** Phases 0–8 — teardown, auth-app deploy, sc-edge front,
v2 tenant provision (stock Debian sidecar: CoreDNS + Tailscale), client enrollment, remote
switch, profile (SSH+cloud-init), **fresh VM connected to sc-edge with a real LE cert**, and
**tenant DNS resolving at the CoreDNS tailscale IP**. Tailscale-readiness race is **fixed**.

**Remaining work to make every phase green:**
1. **Auth-app → broker delegation** (Phase 9) — replace v1 `EnsurePersonalTenant` with the broker/v2 path so unattended login provisions the same v2 tenant.
2. **Machine DNS auto-registration** (Phase 8) — write the A-record into `db.e2etest` on machine create (done manually in the e2e today).
3. **Per-tenant tenant-CA cert path** (Phase 8b) — issue a tenant CA + leaf so the *private* HTTPS path exists; then `sc trust install` (Linux/macOS) makes it trusted. (The *public* sc-edge LE path in Phase 7 already works with no CA install.)
4. **Split-DNS over tailscale** (Phase 8) — serve the tenant zone to tailnet clients on the sidecar tailscale IP.
5. **Deploy-command polish** — local-image default + fix the multi-address remote mangling so Phases 1/3 run via the CLI, not by-hand.
4. **DNS record-on-create** (Phase 7) — confirm machine A-records land in `db.e2etest`.
5. **Deploy-command polish** — local-image default + fix the multi-address remote mangling so Phases 1/3 run via the CLI, not by-hand.

Phases 0–6 are validated green today; 7–9 are the build frontier.

---

## Appendix — Problems encountered & fixes (keep updating)
Log every problem hit while running this e2e and how it was resolved, so the runbook
stays truthful and self-healing.

| Problem | Root cause | Fix |
|---|---|---|
| Appliance services won't start (`systemctl` "not booted with systemd") | Launched from an **OCI** image (app container, no systemd PID1) | Use a **CONTAINER-type systemd** image (`d31c34fadc08` / `images:debian/13`) |
| Caddy re-issues certs / ignores copied ones | Under systemd Caddy has no `$HOME` → wrong storage dir | Pin `storage file_system /root/.local/share/caddy` in the Caddyfile |
| `sc-adm … ` against `big` → "no such host" with a concatenated address | The wrapper mangles the multi-address remote string | Drive via `incus` + broker `exec` directly (wrapper fix pending) |
| `incus file push` → `text file busy` | Overwriting the binary the appliance is running | `systemctl stop` the service, push, `start` |
| `incus launch big:<fp>` in the tenant project → "Image not found" | Tenant `default` project has its own image store (`features.images=true`) | `incus image copy … --target-project sc2-<tenant>-default` first |
| Sidecar has no CoreDNS/Tailscale | Neither is in Debian apt | CoreDNS **binary** download; Tailscale **official apt repo** (`installV2SidecarPackages`) |
| Sidecar Tailscale shows `Logged out` after create | `tailscale up` ran before `tailscaled` was ready | Readiness wait + retry + `tailscale ip -4` gate in `v2TailscaleUp` |
| `:3000` server won't start / `setsid` hangs `incus exec` | Minimal image lacks `python3`; detached process blocks the exec stream | `apt-get install python3`; start via `systemd-run --unit=app --collect` |
| Machine name doesn't resolve in CoreDNS | No A-record auto-registration on machine create yet | Add the record to `db.e2etest` + bump SOA + `systemctl restart coredns` (auto-reg TODO) |
| Tenant machine SSH refused / no `dev` user | Plain image has no cloud-init → profile user-data never applied | Launch tenant machines from `images:debian/13/cloud` (has cloud-init) |
