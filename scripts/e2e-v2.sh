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
  for p in "$DEF" "$BACK" "$INFRA"; do
    for i in $(incus list --project "$p" -c n --format csv 2>/dev/null); do
      incus delete "$i" --project "$p" --force 2>/dev/null || true
    done
    # a project is only removable once empty: drop non-default profiles and any
    # images it owns (app projects run features.images=true).
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
  for i in $(seq 1 15); do
    ip="$(incus exec "$via" --project "$proj" -- getent hosts "$host" 2>/dev/null | awk '{print $1}' | head -1)"
    [ -n "$ip" ] && break
    sleep 2
  done
  printf '%s' "$ip"
}

# The full stack is required (ADR-0018: no harness shortcuts): DNS records are
# registered ONLY by the auth-app reconciler; the dnsmasq fallthrough is gone.
log "full stack present?"
if incus exec sc2-auth-app --project infrastructure -- systemctl is-active sandcastle-auth-app >/dev/null 2>&1; then
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
  local sip; sip="$(incus exec "$name" --project "$proj" -- getent hosts "${name}.${TENANT}" 2>/dev/null | awk '{print $1}' | head -1)"
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
