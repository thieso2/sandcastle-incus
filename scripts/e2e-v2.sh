#!/usr/bin/env bash
# v2 MVP end-to-end (ADR-0016), run against a live Incus host.
#
# Proves: sc-adm tenant create-v2 stands up the topology (infra project +
# sidecar with CoreDNS + shared bridge + default app project + cloud-init
# profile); sc-adm project create-v2 adds a second app project served by the
# SAME sidecar; native `incus launch` of a cloud image into each project yields
# a machine reachable at <machine>.<suffix> via the sidecar CoreDNS with
# cloud-init login. Tears everything down at the end.
#
# Required env:
#   SANDCASTLE_REMOTE   single-address Incus remote the Go SDK can use (e.g. bigv2)
#   SC_ADM              path to the sandcastle-admin binary (default ./bin/sc-adm)
# Optional:
#   V2_TENANT (default e2ev2)  V2_POOL (default 10.252.0.0/16)
#   V2_SIDECAR_IMAGE (system-container base image alias/fingerprint; required)
#   V2_IMAGE (default images:debian/13/cloud)
set -euo pipefail

TENANT="${V2_TENANT:-e2ev2}"
POOL="${V2_POOL:-10.252.0.0/16}"
SC_ADM="${SC_ADM:-./bin/sc-adm}"
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
    incus project delete "$p" 2>/dev/null || true
  done
  incus network delete "$INFRA" --project default 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

# resolve <name>.<suffix> against the sidecar CoreDNS (.3) via a real resolver
# (exec inside a machine on the bridge), returns the A record.
resolve_via_coredns() {
  local host="$1" via="$2" proj="$3"
  incus exec "$via" --project "$proj" -- sh -c \
    "printf 'nameserver ${DNS_ADDR}\n' >/etc/resolv.conf; getent hosts ${host}" 2>/dev/null | awk '{print $1}'
}

log "create tenant ${TENANT} (pool ${POOL})"
"$SC_ADM" tenant create-v2 "$TENANT" \
  --cidr-pool "$POOL" --sidecar-image "$SIDECAR_IMAGE" \
  --ssh-key "$(cat "$KEY.pub")" >/dev/null
DNS_ADDR="$(incus project get "$INFRA" user.sandcastle.v2.cidr | sed 's#0/[0-9]*#3#')"
[ -n "$DNS_ADDR" ] && pass "tenant created (sidecar DNS ${DNS_ADDR})" || fail "tenant create"

log "second project via scaffolding"
"$SC_ADM" project create-v2 "$TENANT" backend >/dev/null && pass "project backend created" || fail "project create"

# launch one machine into each project and assert DNS + SSH
check_machine() {
  local name="$1" proj="$2"
  log "launch ${name} into ${proj}"
  incus launch "$IMAGE" "$name" --project "$proj" >/dev/null
  incus exec "$name" --project "$proj" -- cloud-init status --wait >/dev/null 2>&1 || true
  local ip; ip="$(resolve_via_coredns "${name}.${TENANT}" "$name" "$proj")"
  if [ -n "$ip" ]; then pass "CoreDNS resolves ${name}.${TENANT} -> ${ip}"; else fail "DNS ${name}.${TENANT}"; return; fi
  local out; out="$(ssh -i "$KEY" -o IdentitiesOnly=yes -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null -o ConnectTimeout=8 "dev@${ip}" 'echo OK:$(hostname):$(id -u)' 2>/dev/null || true)"
  case "$out" in
    OK:${name}:2000) pass "ssh dev@${name}.${TENANT} -> ${out}" ;;
    *) fail "ssh ${name} (got '${out}')" ;;
  esac
}

check_machine web "$DEF"
check_machine api "$BACK"

log "result"
if [ "$FAILED" = 0 ]; then echo "v2 e2e: GREEN"; else echo "v2 e2e: RED"; fi
exit "$FAILED"
