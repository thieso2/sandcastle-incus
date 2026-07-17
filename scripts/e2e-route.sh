#!/usr/bin/env bash
# Non-interactive end-to-end test for `sc route` (Spec #111).
#
# Exercises the whole public-route chain WITHOUT public DNS, inbound-from-internet,
# or a real ACME server, by deploying the appliance in hermetic route-TLS mode
# (`--route-tls internal`, Caddy's self-signed CA) and reaching route sites on the
# LAN with `curl --resolve … -k`. Proves: publish → registry + proxy device +
# Caddyfile site → host :443 → Caddy → per-route proxy device → machine app; the
# `ask` cert-gate denies unknown hosts; delete tears it all down. Login is
# untouched throughout.
#
# Prereqs (a test install, NOT production):
#   - An install whose auth-app was deployed with:
#         --route-ingress acme --route-base-domain <ROUTE_BASE_DOMAIN>
#         --route-tls internal        (hermetic: self-signed route certs)
#         --debug-device-user <TENANT>   (headless token minting)
#   - `jq`, `curl`, and an `incus` remote reachable as $SANDCASTLE_REMOTE.
#
# Env:
#   SANDCASTLE_REMOTE        incus remote for the host (e.g. sc2v2)         [required]
#   SC_AUTH_HOST             auth-app base URL (e.g. https://sc2.example.dev) [required]
#   SC_TENANT                tenant + debug-device user (default: thieso2)
#   SC_PROJECT               tenant app project short name (default: home)
#   SC_ROUTE_BASE_DOMAIN     route base domain the install was deployed with  [required]
#   SC_APPLIANCE_INSTANCE    auth-app instance (default: sc2-auth-app)
#   SC_APPLIANCE_PROJECT     auth-app incus project (default: sc2-infra)
#   SC_HOST_IP               host IP that :443 is reachable on for --resolve  [required]
#   SC_ROUTE_TOKEN           tenant CLI token; if unset, minted via debug flow
set -euo pipefail

: "${SANDCASTLE_REMOTE:?set SANDCASTLE_REMOTE}"
: "${SC_AUTH_HOST:?set SC_AUTH_HOST (auth-app base URL)}"
: "${SC_ROUTE_BASE_DOMAIN:?set SC_ROUTE_BASE_DOMAIN}"
: "${SC_HOST_IP:?set SC_HOST_IP (host IP :443 is reachable on)}"
TENANT="${SC_TENANT:-thieso2}"
PROJECT="${SC_PROJECT:-home}"
INSTANCE="${SC_APPLIANCE_INSTANCE:-sc2-auth-app}"
INFRA="${SC_APPLIANCE_PROJECT:-sc2-infra}"
REMOTE="$SANDCASTLE_REMOTE"
SC_PREFIX="${SC_PREFIX:-sc2}"
APPPROJ="${SC_PREFIX}-${TENANT}-${PROJECT}" # <prefix>-<tenant>-<project>
MACHINE="e2eroute"
HOST="${MACHINE}.${TENANT}.${SC_ROUTE_BASE_DOMAIN}"
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }
cleanup() {
  curl -fsS -X DELETE "$SC_AUTH_HOST/api/routes?hostname=$HOST" -H "Authorization: Bearer $TOKEN" >/dev/null 2>&1 || true
  incus delete "${REMOTE}:${MACHINE}" --project "$APPPROJ" --force >/dev/null 2>&1 || true
}
trap cleanup EXIT

# --- 1. mint a tenant token (headless debug device flow) unless supplied --------
TOKEN="${SC_ROUTE_TOKEN:-}"
if [ -z "$TOKEN" ]; then
  DEV=$(curl -fsS -X POST "$SC_AUTH_HOST/api/device/start")
  DEVICE_CODE=$(echo "$DEV" | jq -r .device_code)
  USER_CODE=$(echo "$DEV" | jq -r .user_code)
  curl -fsS -X POST "$SC_AUTH_HOST/debug/device/approve" --data-urlencode "user_code=$USER_CODE" >/dev/null
  for _ in $(seq 1 30); do
    POLL=$(curl -fsS -X POST "$SC_AUTH_HOST/api/device/poll" -H 'Content-Type: application/json' -d "{\"device_code\":\"$DEVICE_CODE\"}" || true)
    TOKEN=$(echo "$POLL" | jq -r '.cli_auth_token // empty')
    [ -n "$TOKEN" ] && break
    sleep 2
  done
  [ -n "$TOKEN" ] || fail "could not mint a tenant token via the debug device flow (server needs --debug-device-user $TENANT)"
fi
pass "have a tenant token"

# --- 2. a machine running an app on :3000 --------------------------------------
# CONTAINER images only: the project also caches VM variants, and launching a
# VM image without --vm yields an instance that never boots (no IP, caught live).
IMG=$(incus image list "${REMOTE}:" --project "$APPPROJ" -c ft --format csv 2>/dev/null | awk -F, '$2=="CONTAINER"{print $1; exit}')
[ -n "$IMG" ] || fail "no cached container image in $APPPROJ to launch from"
incus launch "${REMOTE}:${IMG}" "${REMOTE}:${MACHINE}" --project "$APPPROJ" >/dev/null
for _ in $(seq 1 40); do
  # List unfiltered and match client-side: a name-filtered
  # `incus list <remote>:<name>` returns an empty set on Incus 7.2 (caught
  # live), and under pipefail an empty poll's grep exit 1 would kill the
  # script (set -e) — hence the `|| true`.
  MIP=$(incus list "${REMOTE}:" --project "$APPPROJ" --format csv -c n4 2>/dev/null | awk -F, -v m="$MACHINE" '$1==m{print $2}' | grep -oE '10\.[0-9.]+' | head -1 || true)
  [ -n "$MIP" ] && break; sleep 3
done
[ -n "$MIP" ] || fail "machine $MACHINE never got an IP"
incus exec "${REMOTE}:${MACHINE}" --project "$APPPROJ" -- bash -c \
  'echo e2e-route-ok > /root/index.html; systemd-run --unit=app --collect python3 -m http.server 3000 --directory /root' >/dev/null
pass "machine $MACHINE up at $MIP with app on :3000"

# --- 3. publish the route ------------------------------------------------------
RESP=$(curl -fsS -X POST "$SC_AUTH_HOST/api/routes" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"tenant\":\"$TENANT\",\"project\":\"$PROJECT\",\"machine\":\"$MACHINE\",\"backendPort\":3000}")
echo "$RESP" | jq -e --arg h "$HOST" '.hostname==$h and .status=="live"' >/dev/null \
  || fail "publish response unexpected: $RESP"
pass "published $HOST (status live)"

# --- 4. the plumbing: Caddyfile site + proxy device ----------------------------
incus exec "${REMOTE}:${INSTANCE}" --project "$INFRA" -- grep -q "$HOST {" /etc/caddy/Caddyfile \
  || fail "Caddyfile has no site for $HOST"
incus config device list "${REMOTE}:${INSTANCE}" --project "$INFRA" | grep -q scroute \
  || fail "no per-route scroute-… proxy device on the appliance"
pass "Caddyfile site + proxy device present"

# --- 5. public HTTPS (self-signed in hermetic mode) ----------------------------
BODY=""
for _ in $(seq 1 8); do
  BODY=$(curl -sS -k --max-time 20 --resolve "${HOST}:443:${SC_HOST_IP}" "https://${HOST}/" 2>/dev/null || true)
  [ -n "$BODY" ] && break; sleep 4
done
[ "$BODY" = "e2e-route-ok" ] || fail "route did not serve the app body (got: '$BODY')"
pass "https://$HOST served the app body over the full chain"

# --- 6. ask gate: unknown host denied ------------------------------------------
CODE=$(incus exec "${REMOTE}:${INSTANCE}" --project "$INFRA" -- \
  curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:9444/api/routes/ask?domain=nope.${SC_ROUTE_BASE_DOMAIN}")
[ "$CODE" = "403" ] || fail "ask endpoint should deny an unregistered host (got $CODE)"
pass "ask gate denies unregistered hosts (403)"

# --- 7. delete tears it down ---------------------------------------------------
curl -fsS -X DELETE "$SC_AUTH_HOST/api/routes?hostname=$HOST" -H "Authorization: Bearer $TOKEN" >/dev/null
incus exec "${REMOTE}:${INSTANCE}" --project "$INFRA" -- grep -q "$HOST {" /etc/caddy/Caddyfile \
  && fail "route site still in Caddyfile after delete" || true
pass "delete removed the route site"

echo "ALL PASS — sc route non-interactive e2e"
