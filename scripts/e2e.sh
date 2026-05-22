#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/e2e.sh <tier>

Tiers:
  unit       Run all Incus-free Go tests.
  gated     Run e2e package with gates/default skips.
  local     Run unprivileged local e2e flows with SANDCASTLE_E2E=1.
  local-vm  Run local DNS/trust/hosts e2e intended for disposable VMs. Requires SANDCASTLE_E2E=1 and SANDCASTLE_E2E_LOCAL_VM=1.
  incus     Run destructive real-Incus e2e flows. Requires SANDCASTLE_E2E=1.
  restricted Run restricted-client HTTPS remote e2e. Requires SANDCASTLE_E2E=1, non-local SANDCASTLE_E2E_REMOTE, and image source env.
  tailscale Run real Tailscale routed-access e2e. Requires SANDCASTLE_E2E=1, image source env, and auth key env.
  images    Run real image build e2e. Requires SANDCASTLE_E2E=1, image build env, and pinned AI CLI versions.
  route-broker Run route broker mTLS mutation e2e. Requires SANDCASTLE_E2E=1, image source env, and broker socket env.
  public-routes Run public route broker mutation e2e. Requires SANDCASTLE_E2E=1, image source env, broker socket env, and public route env.
  cleanup   Remove managed disposable e2e projects for SANDCASTLE_E2E_RUN_ID. Requires SANDCASTLE_E2E=1 and an explicit run id.
  all       Run unit, gated, local, local-vm, incus, restricted, tailscale, images, route-broker, and public-routes tiers.

Examples:
  scripts/e2e.sh unit
  SANDCASTLE_E2E=1 SANDCASTLE_E2E_REMOTE=local scripts/e2e.sh incus
  SANDCASTLE_E2E=1 SANDCASTLE_E2E_LOCAL_VM=1 scripts/e2e.sh local-vm
  SANDCASTLE_E2E=1 SANDCASTLE_E2E_REMOTE=remote-incus SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13 SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13 scripts/e2e.sh restricted
  SANDCASTLE_E2E=1 SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13 SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13 SANDCASTLE_E2E_TAILSCALE_AUTHKEY=tskey-auth-... scripts/e2e.sh tailscale
  SANDCASTLE_E2E=1 SANDCASTLE_E2E_IMAGE_BUILD=1 SANDCASTLE_E2E_CODEX_VERSION=... SANDCASTLE_E2E_CLAUDE_CODE_VERSION=... SANDCASTLE_E2E_GEMINI_CLI_VERSION=... scripts/e2e.sh images
  SANDCASTLE_E2E=1 SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET=/var/lib/incus/unix.socket SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13 SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13 scripts/e2e.sh route-broker
  SANDCASTLE_E2E=1 SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET=/var/lib/incus/unix.socket SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13 SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13 SANDCASTLE_E2E_PUBLIC_DOMAIN=e2e.example.com SANDCASTLE_E2E_INFRA_HOST=203.0.113.10 SANDCASTLE_E2E_LETSENCRYPT_EMAIL=ops@example.com scripts/e2e.sh public-routes
  SANDCASTLE_E2E=1 SANDCASTLE_E2E_RUN_ID=e2e-20260520-120000 scripts/e2e.sh cleanup
USAGE
}

require_e2e() {
  if [[ "${SANDCASTLE_E2E:-}" != "1" ]]; then
    echo "error: set SANDCASTLE_E2E=1 to run destructive e2e tier '$1'" >&2
    exit 2
  fi
}

require_env() {
  local tier="$1"
  local name="$2"
  if [[ -z "${!name:-}" ]]; then
    echo "error: set $name to run e2e tier '$tier'" >&2
    exit 2
  fi
}

ensure_run_id() {
  local tier="$1"
  if [[ -z "${SANDCASTLE_E2E_RUN_ID:-}" ]]; then
    export SANDCASTLE_E2E_RUN_ID="e2e-$(date -u +%Y%m%d-%H%M%S)-$$"
  fi
  echo "SANDCASTLE_E2E_RUN_ID=$SANDCASTLE_E2E_RUN_ID"
}

run() {
  echo "+ $*"
  "$@"
}

run_unit() {
  run env -i HOME="$HOME" PATH="$PATH" USER="${USER:-}" SANDCASTLE_E2E=0 go test ./...
}

run_gated() {
  run env -i HOME="$HOME" PATH="$PATH" USER="${USER:-}" SANDCASTLE_E2E=0 go test ./internal/e2e -count=1 -v
}

run_local() {
  export SANDCASTLE_E2E=1
  ensure_run_id local
  run go test ./internal/e2e -run 'TestLocalDNSInstallForwardRefreshUninstallE2E' -count=1 -v
}

run_local_vm() {
  require_e2e local-vm
  ensure_run_id local-vm
  if [[ "${SANDCASTLE_E2E_LOCAL_VM:-}" != "1" ]]; then
    echo "error: set SANDCASTLE_E2E_LOCAL_VM=1 to run disposable-VM local mutation tier 'local-vm'" >&2
    exit 2
  fi
  run go test ./internal/e2e -run 'Test(LocalDNS.*E2E|LocalTrust.*E2E|HostOverrideHostsFileE2E)' -count=1 -v
}

run_incus() {
  require_e2e incus
  ensure_run_id incus
  run go test ./internal/e2e -run 'Test(TenantListingSmoke|DisposableTenantCreateAndPurge|DisposableInfrastructureCreateAndDelete|RouteBrokerAuthorizedMutationE2E|ImageSync.*AliasE2E|TenantDNSE2E|MachineLifecycleE2E|HostOverrideE2E|LocalTrustInstallUninstallE2E|CLICreate.*E2E|CLIConnectCommandE2E)' -count=1 -v
}

run_restricted() {
  require_e2e restricted
  ensure_run_id restricted
  require_env restricted SANDCASTLE_E2E_REMOTE
  if [[ "${SANDCASTLE_E2E_REMOTE}" == "local" ]]; then
    echo "error: set SANDCASTLE_E2E_REMOTE to a configured HTTPS Incus remote, not 'local', to run e2e tier 'restricted'" >&2
    exit 2
  fi
  require_env restricted SANDCASTLE_E2E_BASE_IMAGE_SOURCE
  require_env restricted SANDCASTLE_E2E_AI_IMAGE_SOURCE
  run go test ./internal/e2e -run 'TestRestrictedUser(Token|GrantAccess|MachineLifecycle)E2E' -count=1 -v
}

run_images() {
  require_e2e images
  ensure_run_id images
  require_env images SANDCASTLE_E2E_IMAGE_BUILD
  if [[ "${SANDCASTLE_E2E_IMAGE_BUILD:-}" != "1" ]]; then
    echo "error: set SANDCASTLE_E2E_IMAGE_BUILD=1 to run real image build tier 'images'" >&2
    exit 2
  fi
  require_env images SANDCASTLE_E2E_CODEX_VERSION
  require_env images SANDCASTLE_E2E_CLAUDE_CODE_VERSION
  require_env images SANDCASTLE_E2E_GEMINI_CLI_VERSION
  run go test ./internal/e2e -run 'Test(ImageBuildBaseE2E|ImageBuildAIE2E)' -count=1 -v
}

run_tailscale() {
  require_e2e tailscale
  ensure_run_id tailscale
  require_env tailscale SANDCASTLE_E2E_BASE_IMAGE_SOURCE
  require_env tailscale SANDCASTLE_E2E_AI_IMAGE_SOURCE
  require_env tailscale SANDCASTLE_E2E_TAILSCALE_AUTHKEY
  run go test ./internal/e2e -run 'TestTailscaleAttachmentE2E' -count=1 -v
}

require_route_broker_env() {
  local tier="$1"
  require_env "$tier" SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET
  require_env "$tier" SANDCASTLE_E2E_BASE_IMAGE_SOURCE
  require_env "$tier" SANDCASTLE_E2E_AI_IMAGE_SOURCE
}

run_route_broker() {
  require_e2e route-broker
  ensure_run_id route-broker
  require_route_broker_env route-broker
  run go test ./internal/e2e -run 'TestRouteBrokerAuthorizedMutationE2E' -count=1 -v
}

run_public_routes() {
  require_e2e public-routes
  ensure_run_id public-routes
  require_route_broker_env public-routes
  require_env public-routes SANDCASTLE_E2E_PUBLIC_DOMAIN
  require_env public-routes SANDCASTLE_E2E_INFRA_HOST
  require_env public-routes SANDCASTLE_E2E_LETSENCRYPT_EMAIL
  run go test ./internal/e2e -run 'TestRouteBrokerAuthorizedMutationE2E' -count=1 -v
}

run_cleanup() {
  require_e2e cleanup
  require_env cleanup SANDCASTLE_E2E_RUN_ID
  run go test ./internal/e2e -run 'TestCleanupDisposableResourcesE2E' -count=1 -v
}

tier="${1:-}"
case "$tier" in
  unit)
    run_unit
    ;;
  gated)
    run_gated
    ;;
  local)
    run_local
    ;;
  local-vm)
    run_local_vm
    ;;
  incus)
    run_incus
    ;;
  restricted)
    run_restricted
    ;;
  tailscale)
    run_tailscale
    ;;
  images)
    run_images
    ;;
  route-broker)
    run_route_broker
    ;;
  public-routes)
    run_public_routes
    ;;
  cleanup)
    run_cleanup
    ;;
  all)
    run_unit
    run_gated
    run_local
    run_local_vm
    run_incus
    run_restricted
    run_tailscale
    run_images
    run_route_broker
    run_public_routes
    ;;
  -h|--help|help|"")
    usage
    ;;
  *)
    echo "error: unknown e2e tier '$tier'" >&2
    usage >&2
    exit 2
    ;;
esac
