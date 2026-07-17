#!/usr/bin/env bash
# v2 MVP end-to-end (ADR-0016), run against a live Incus host.
#
# Proves: sc-adm tenant create stands up the topology (infra project +
# sidecar with CoreDNS + shared bridge + default app project + cloud-init
# profile); sc-adm project create adds a second app project served by the
# SAME sidecar; native `incus launch` of a cloud image into each project yields
# a machine reachable at <machine>.<suffix> via the sidecar CoreDNS with
# cloud-init login; and the USER CLI lifecycle works: `sc create`/`sc delete`
# with project-scoped refs, clear errors for unknown projects, and the
# swapped-reference hint. Tears everything down at the end.
#
# Required env:
#   SANDCASTLE_REMOTE   single-address Incus remote the Go SDK can use (e.g. bigv2)
#   SC_ADM              path to the sandcastle-admin binary (default ./bin/sc-adm)
#   SC                  path to the user sc binary (default ./bin/sc)
# Optional:
#   V2_TENANT (default e2ev2)  V2_POOL (default 10.252.0.0/16)
#   V2_SIDECAR_IMAGE (system-container base image alias/fingerprint; required)
#   V2_IMAGE (default images:debian/13/cloud)
set -euo pipefail

TENANT="${V2_TENANT:-e2ev2}"
POOL="${V2_POOL:-10.252.0.0/16}"
SC_ADM="${SC_ADM:-./bin/sc-adm}"
SC="${SC:-./bin/sc}"
IMAGE="${V2_IMAGE:-images:debian/13/cloud}"
SIDECAR_IMAGE="${V2_SIDECAR_IMAGE:?set V2_SIDECAR_IMAGE to a system-container base image}"
: "${SANDCASTLE_REMOTE:?set SANDCASTLE_REMOTE to a single-address Incus remote}"
export SANDCASTLE_REMOTE

WORK="$(mktemp -d)"
KEY="$WORK/id"
ssh-keygen -t ed25519 -N '' -f "$KEY" -C "$TENANT-e2e" >/dev/null

INFRA="sc2-${TENANT}"
DEF="sc2-${TENANT}-default"
BACK="sc2-${TENANT}-backend"
FAILED=0

log()  { printf '\n=== %s ===\n' "$*"; }
pass() { printf 'PASS: %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*"; FAILED=1; }

cleanup() {
  log "cleanup ${TENANT}"
  # Product path first: tenant delete --purge tears down app projects (incl.
  # the shared home/workspace volumes that block a bare project delete), the
  # infra project, and the bridge, all-or-nothing.
  "$SC_ADM" tenant delete "$TENANT" --yes --purge >/dev/null 2>&1 || true
  # Manual sweep for partial states the product path refuses to touch.
  for p in "$DEF" "$BACK" "$INFRA"; do
    for i in $(incus list --project "$p" -c n --format csv 2>/dev/null); do
      incus delete "$i" --project "$p" --force 2>/dev/null || true
    done
    # a project is only removable once empty: drop non-default profiles, any
    # images it owns (app projects run features.images=true), and the shared
    # home/workspace volumes (detach from the default profile FIRST).
    for v in home workspace sc-platform sc-local; do
      incus profile device remove default "$v" --project "$p" 2>/dev/null || true
      incus storage volume delete default "$v" --project "$p" 2>/dev/null || true
    done
    for prof in $(incus profile list --project "$p" --format csv -c n 2>/dev/null | grep -v '^default$'); do
      incus profile delete "$prof" --project "$p" 2>/dev/null || true
    done
    if [ "$(incus project get "$p" features.images 2>/dev/null)" = "true" ]; then
      for f in $(incus image list --project "$p" --format csv -c f 2>/dev/null); do
        incus image delete "$f" --project "$p" 2>/dev/null || true
      done
    fi
    incus project delete "$p" 2>/dev/null || true
  done
  incus network delete "$INFRA" --project default 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

# resolve a name with the machine's OWN resolver (no override): proves the
# bridge hands the sidecar CoreDNS out via DHCP (ADR-0018) AND the record
# exists in the zone. Retries briefly — registration is event-driven and lands
# within seconds. Returns the A record, empty on NXDOMAIN.
resolve_in_machine() {
  local host="$1" via="$2" proj="$3" ip="" i
  # ahostsv4, not hosts: `getent hosts` prefers AAAA and nss-myhostname answers
  # a machine's OWN canonical name with a useless fe80:: link-local, masking a
  # missing zone record. Registration on a fresh tenant can take a full
  # reconcile period plus sidecar exec time, so allow 60s.
  for i in $(seq 1 30); do
    ip="$(incus exec "$via" --project "$proj" -- getent ahostsv4 "$host" 2>/dev/null | awk 'NR==1{print $1}' || true)"
    [ -n "$ip" ] && break
    sleep 2
  done
  printf '%s' "$ip"
}

# The full stack is required (ADR-0018: no harness shortcuts): DNS records are
# registered ONLY by the auth-app reconciler; the dnsmasq fallthrough is gone.
log "full stack present?"
# sc-adm install puts the auth-app appliance in <prefix>-infra (default
# sc2-infra); the plain "infrastructure" project is the pre-install legacy
# layout — accept either.
if incus exec sc2-auth-app --project sc2-infra -- systemctl is-active sandcastle-auth-app >/dev/null 2>&1 \
  || incus exec sc2-auth-app --project infrastructure -- systemctl is-active sandcastle-auth-app >/dev/null 2>&1; then
  pass "auth-app is running (DNS reconciler active)"
else
  fail "auth-app appliance is not running — deploy the full stack first (sc-adm install); DNS assertions cannot pass without its reconciler"
  echo "v2 e2e: RED"; exit 1
fi

log "create tenant ${TENANT} (pool ${POOL})"
"$SC_ADM" tenant create "$TENANT" \
  --cidr-pool "$POOL" --sidecar-image "$SIDECAR_IMAGE" \
  --ssh-key "$(cat "$KEY.pub")" >/dev/null
DNS_ADDR="$(incus project get "$INFRA" user.sandcastle.v2.cidr | sed 's#0/[0-9]*#3#')"
[ -n "$DNS_ADDR" ] && pass "tenant created (sidecar DNS ${DNS_ADDR})" || fail "tenant create"

log "second project via scaffolding"
"$SC_ADM" project create "$TENANT" backend >/dev/null && pass "project backend created" || fail "project create"

# launch one machine into each project; assert the ADR-0018 naming contract
# with the machine's own resolver, then SSH via the canonical name's IP.
# $4 = the project SHORT name ("default"/"backend").
check_machine() {
  local name="$1" proj="$2" short="$3"
  log "launch ${name} into ${proj}"
  incus launch "$IMAGE" "$name" --project "$proj" >/dev/null
  incus exec "$name" --project "$proj" -- cloud-init status --wait >/dev/null 2>&1 || true
  local canonical="${name}.${short}.${TENANT}"
  local ip; ip="$(resolve_in_machine "$canonical" "$name" "$proj")"
  if [ -n "$ip" ]; then pass "resolves ${canonical} -> ${ip} (machine's own resolver)"; else fail "DNS ${canonical}"; return; fi
  # wildcard under the canonical name
  local wip; wip="$(resolve_in_machine "app1.${canonical}" "$name" "$proj")"
  [ "$wip" = "$ip" ] && pass "wildcard app1.${canonical} -> ${wip}" || fail "wildcard app1.${canonical} (got '${wip}')"
  # short alias: default project ONLY
  local sip; sip="$(incus exec "$name" --project "$proj" -- getent ahostsv4 "${name}.${TENANT}" 2>/dev/null | awk 'NR==1{print $1}' || true)"
  if [ "$short" = "default" ]; then
    [ "$sip" = "$ip" ] && pass "short ${name}.${TENANT} -> ${sip} (default project alias)" || fail "short ${name}.${TENANT} (got '${sip}')"
  else
    [ -z "$sip" ] && pass "short ${name}.${TENANT} is NXDOMAIN (non-default project)" || fail "short ${name}.${TENANT} must NOT resolve (got '${sip}')"
  fi
  # guest identity: hostname -f = canonical Machine Private Hostname
  local fq; fq="$(incus exec "$name" --project "$proj" -- hostname -f 2>/dev/null || true)"
  [ "$fq" = "$canonical" ] && pass "guest hostname -f = ${fq}" || fail "guest hostname -f (got '${fq}', want ${canonical})"
  local out; out="$(ssh -i "$KEY" -o IdentitiesOnly=yes -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null -o ConnectTimeout=8 "dev@${ip}" 'echo OK:$(id -u)' 2>/dev/null || true)"
  case "$out" in
    OK:2000) pass "ssh dev@${canonical} -> ${out}" ;;
    *) fail "ssh ${name} (got '${out}')" ;;
  esac
}

check_machine web "$DEF" default
check_machine api "$BACK" backend

# ── /.sc shared-scripts volume (ADR-0022, spec #127) ────────────────────────
# Platform payload present + consistent across projects; platform RO / local RW
# in machines; broken local payload fails safe; a central volume write reaches
# a RUNNING machine with no re-create; payload-sync detects + heals drift.
scv() { incus exec "$1" --project "$2" -- sh -c "$3"; }
log "/.sc: payload + per-layer writability"
V_WEB="$(scv web "$DEF" 'cat /.sc/platform/VERSION' 2>/dev/null || true)"
V_API="$(scv api "$BACK" 'cat /.sc/platform/VERSION' 2>/dev/null || true)"
if [ -n "$V_WEB" ] && [ "$V_WEB" = "$V_API" ]; then
  pass "platform payload present + consistent across projects ($V_WEB)"
else fail "platform payload (web='$V_WEB' api='$V_API')"; fi
if scv web "$DEF" 'touch /.sc/platform/e2e-probe' 2>/dev/null; then
  fail "/.sc/platform is writable inside a machine"
else pass "/.sc/platform is read-only inside machines"; fi
if scv api "$BACK" 'echo sc-local-marker > /.sc/local/e2e-marker && grep -q sc-local-marker /.sc/local/e2e-marker' 2>/dev/null; then
  pass "/.sc/local is writable from a machine"
else fail "/.sc/local write"; fi
# shims present on a fresh machine (baked by cloud-init)
if scv web "$DEF" 'grep -q "Sandcastle /.sc shim" /etc/ssh/sshrc && grep -q "Sandcastle /.sc shim" /etc/zsh/zshrc && grep -q "Sandcastle /.sc shim" /etc/bash.bashrc' 2>/dev/null; then
  pass "stable /.sc shims baked (sshrc + zshrc + bash.bashrc)"
else fail "/.sc shims missing on a fresh machine"; fi

log "/.sc: broken local payload fails safe"
scv api "$BACK" 'mkdir -p /.sc/local/shell && printf "this is not a shell script\n" > /.sc/local/shell/rc.sh' 2>/dev/null || true
out="$(scv api "$BACK" 'su - dev -c "zsh -i -c \"echo shell-ok\"" 2>/dev/null' 2>/dev/null || true)"
if printf '%s' "$out" | grep -q shell-ok; then
  pass "interactive shell survives a broken /.sc/local script"
else fail "broken /.sc/local script broke the shell (out='$out')"; fi
scv api "$BACK" 'rm -f /.sc/local/shell/rc.sh' 2>/dev/null || true

log "/.sc: central update reaches a RUNNING machine; payload-sync heals"
SPOOL="$(incus profile device get default sc-platform pool --project "$DEF" 2>/dev/null || echo default)"
if incus config device add web e2e-scrw disk pool="$SPOOL" source=sc-platform path=/mnt/e2e-scrw --project "$DEF" >/dev/null 2>&1 &&
   scv web "$DEF" 'echo tampered > /mnt/e2e-scrw/VERSION'; then
  V_NOW="$(scv web "$DEF" 'cat /.sc/platform/VERSION' 2>/dev/null || true)"
  [ "$V_NOW" = "tampered" ] && pass "central volume write visible on the running machine (no re-create)" \
    || fail "running machine did not observe the central write (got '$V_NOW')"
  if "$SC_ADM" tenant payload-sync "$TENANT" --check 2>/dev/null | grep -q STALE; then
    pass "payload-sync --check detects drift"
  else fail "payload-sync --check missed the drift"; fi
  "$SC_ADM" tenant payload-sync "$TENANT" >/dev/null 2>&1 || true
  V_RESTORED="$(scv web "$DEF" 'cat /.sc/platform/VERSION' 2>/dev/null || true)"
  [ "$V_RESTORED" = "$V_WEB" ] && pass "payload-sync restored the payload centrally ($V_RESTORED)" \
    || fail "payload-sync did not restore (got '$V_RESTORED', want '$V_WEB')"
  incus config device remove web e2e-scrw --project "$DEF" >/dev/null 2>&1 || true
else
  fail "could not attach sc-platform RW for the tamper test"
fi

log "/.sc: fleet shim bootstrap is idempotent"
FLEET="$(dirname "$0")/fix-agent-forwarding.sh"
# capture-then-grep: `script | grep -q` + pipefail turns grep's early exit
# (SIGPIPE into the still-writing script) into a spurious pipeline failure.
FLEET_OK=0
"$FLEET" --project "$DEF" web >/dev/null 2>&1 && FLEET_OK=1
FLEET_OUT="$("$FLEET" --project "$DEF" web 2>/dev/null || true)"
if [ "$FLEET_OK" = 1 ] && printf '%s' "$FLEET_OUT" | grep -q 'already has the /.sc shim'; then
  pass "fix-agent-forwarding.sh runs + re-run reports already-present"
else fail "fleet shim bootstrap (first_ok=$FLEET_OK out='$(printf '%s' "$FLEET_OUT" | tail -3)')"; fi

# machine lifecycle through the USER CLI (`sc create` / `sc delete`, v2 path):
# project-scoped references must resolve, a bad project must fail fast with the
# tenant's project list (not an Incus permission error for a project that does
# not exist), and a swapped project:machine reference must suggest the fix.
log "machine lifecycle via sc create/delete"
export SANDCASTLE_TENANT="$TENANT"
if "$SC" create backend:dev1 --image "$IMAGE" --detach >/dev/null 2>&1 &&
   incus info dev1 --project "$BACK" >/dev/null 2>&1; then
  pass "sc create backend:dev1"
else fail "sc create backend:dev1"; fi
# /.sc/local written earlier on api must be visible on this second machine of
# the same project (shared volume; spec #127 story 3).
mk=""
for i in $(seq 1 30); do
  mk="$(incus exec dev1 --project "$BACK" -- cat /.sc/local/e2e-marker 2>/dev/null || true)"
  [ -n "$mk" ] && break
  sleep 2
done
[ "$mk" = "sc-local-marker" ] && pass "/.sc/local marker visible on a second machine" \
  || fail "/.sc/local marker not visible on dev1 (got '$mk')"
if out="$("$SC" create nosuch:dev1 2>&1)"; then fail "sc create nosuch:dev1 unexpectedly succeeded"
elif printf '%s' "$out" | grep -q 'not found in tenant'; then pass "unknown project fails with the project list"
else fail "unknown-project error unclear: $out"; fi
if out="$("$SC" delete dev1:backend --yes 2>&1)"; then fail "swapped reference unexpectedly succeeded"
elif printf '%s' "$out" | grep -q 'did you mean "backend:dev1"'; then pass "swapped reference suggests backend:dev1"
else fail "swap hint missing: $out"; fi
if "$SC" delete backend:dev1 --yes >/dev/null 2>&1 &&
   ! incus info dev1 --project "$BACK" >/dev/null 2>&1; then
  pass "sc delete backend:dev1"
else fail "sc delete backend:dev1"; fi
unset SANDCASTLE_TENANT

log "result"
if [ "$FAILED" = 0 ]; then echo "v2 e2e: GREEN"; else echo "v2 e2e: RED"; fi
exit "$FAILED"
