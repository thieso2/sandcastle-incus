#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/e2e.sh <tier>

Tiers:
  unit       Run all Incus-free Go tests.
  gated     Run e2e package with gates/default skips.
  local     Run unprivileged local e2e flows with SANDCASTLE_E2E=1.
  incus     Run destructive real-Incus e2e flows. Requires SANDCASTLE_E2E=1.
  tailscale Run real Tailscale routed-access e2e. Requires SANDCASTLE_E2E=1, image source env, and auth key env.
  images    Run real image build e2e. Requires SANDCASTLE_E2E=1 and image build env.
  all       Run unit, gated, local, incus, tailscale, and images tiers.

Examples:
  scripts/e2e.sh unit
  SANDCASTLE_E2E=1 SANDCASTLE_E2E_REMOTE=local scripts/e2e.sh incus
  SANDCASTLE_E2E=1 SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13 SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13 SANDCASTLE_E2E_TAILSCALE_AUTHKEY=tskey-auth-... scripts/e2e.sh tailscale
  SANDCASTLE_E2E=1 SANDCASTLE_E2E_IMAGE_BUILD=1 scripts/e2e.sh images
USAGE
}

require_e2e() {
  if [[ "${SANDCASTLE_E2E:-}" != "1" ]]; then
    echo "error: set SANDCASTLE_E2E=1 to run destructive e2e tier '$1'" >&2
    exit 2
  fi
}

run() {
  echo "+ $*"
  "$@"
}

run_unit() {
  run go test ./...
}

run_gated() {
  run go test ./internal/e2e -count=1 -v
}

run_local() {
  SANDCASTLE_E2E=1 run go test ./internal/e2e -run 'TestLocalDNSInstallForwardRefreshUninstallE2E' -count=1 -v
}

run_incus() {
  require_e2e incus
  run go test ./internal/e2e -run 'Test(IncusProjectListingSmoke|DisposableProjectCreateAndPurge|DisposableInfrastructureCreateAndDelete|RouteBrokerAuthorizedMutationE2E|ImageSync.*AliasE2E|ProjectDNSE2E|SandboxLifecycleE2E|HostOverrideE2E|LocalTrustInstallUninstallE2E|CLIAddDetachE2E|CLIEnterCommandE2E|RestrictedUser(Token|GrantAccess|SandboxLifecycle)E2E)' -count=1 -v
}

run_images() {
  require_e2e images
  run go test ./internal/e2e -run 'Test(ImageBuildBaseE2E|ImageBuildAIE2E)' -count=1 -v
}

run_tailscale() {
  require_e2e tailscale
  run go test ./internal/e2e -run 'TestTailscaleAttachmentE2E' -count=1 -v
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
  incus)
    run_incus
    ;;
  tailscale)
    run_tailscale
    ;;
  images)
    run_images
    ;;
  all)
    run_unit
    run_gated
    run_local
    run_incus
    run_tailscale
    run_images
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
