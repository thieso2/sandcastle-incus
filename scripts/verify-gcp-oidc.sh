#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/verify-gcp-oidc.sh --tenant TENANT (--auth-hostname HOST | --issuer ISSUER) [options]

Displays and verifies the Google Cloud Workload Identity Federation setup for
one Sandcastle tenant. This script is read-only.

Required:
  --tenant TENANT              Sandcastle tenant slug.
  --auth-hostname HOST         Auth Hostname, for example big.thieso2.dev.
                               The issuer becomes https://HOST/t/TENANT.
  --issuer ISSUER              Full issuer URI. Overrides --auth-hostname.

Options:
  --project PROJECT_ID         GCP project ID. Defaults to gcloud config project.
  --pool-id POOL_ID            Defaults to sandcastle-TENANT.
  --provider-id PROVIDER_ID    Defaults to sandcastle.
  --service-account NAME       Service account name or email. Defaults to sandcastle-TENANT.
  --machine-project PROJECT    Verify a single-machine subject binding.
                               Requires --machine.
  --machine MACHINE            Verify a single-machine subject binding.
  --skip-http                  Do not fetch Sandcastle discovery/JWKS endpoints.
  -h, --help                   Show this help.

Examples:
  scripts/verify-gcp-oidc.sh \
    --tenant thieso2 \
    --auth-hostname big.thieso2.dev

  scripts/verify-gcp-oidc.sh \
    --tenant thieso2 \
    --auth-hostname big.thieso2.dev \
    --machine-project default \
    --machine codex
USAGE
}

tenant=""
auth_hostname="${SANDCASTLE_AUTH_HOSTNAME:-}"
issuer=""
project_id=""
pool_id=""
provider_id="sandcastle"
service_account=""
machine_project=""
machine_name=""
skip_http=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tenant)
      tenant="${2:-}"
      shift 2
      ;;
    --auth-hostname)
      auth_hostname="${2:-}"
      shift 2
      ;;
    --issuer)
      issuer="${2:-}"
      shift 2
      ;;
    --project)
      project_id="${2:-}"
      shift 2
      ;;
    --pool-id)
      pool_id="${2:-}"
      shift 2
      ;;
    --provider-id)
      provider_id="${2:-}"
      shift 2
      ;;
    --service-account)
      service_account="${2:-}"
      shift 2
      ;;
    --machine-project)
      machine_project="${2:-}"
      shift 2
      ;;
    --machine)
      machine_name="${2:-}"
      shift 2
      ;;
    --skip-http)
      skip_http=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "error: unknown argument $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_value() {
  local name="$1"
  local value="$2"
  if [[ -z "$value" ]]; then
    echo "error: $name is required" >&2
    usage >&2
    exit 2
  fi
}

trim_dots() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  value="${value%.}"
  printf '%s' "$value"
}

normalize_issuer() {
  local value="$1"
  value="$(trim_dots "$value")"
  if [[ "$value" != http://* && "$value" != https://* ]]; then
    value="https://${value}"
  fi
  value="${value%/}"
  printf '%s' "$value"
}

pass() {
  echo "PASS: $*"
}

fail() {
  failed=1
  echo "FAIL: $*" >&2
}

info() {
  echo "INFO: $*"
}

json_value() {
  local json="$1"
  local expr="$2"
  python3 -c 'import json,sys; data=json.load(sys.stdin); value=eval(sys.argv[1], {}, {"data": data}); print("" if value is None else value)' "$expr" <<<"$json"
}

json_contains() {
  local json="$1"
  local expr="$2"
  local want="$3"
  python3 -c 'import json,sys; data=json.load(sys.stdin); values=eval(sys.argv[1], {}, {"data": data}); sys.exit(0 if sys.argv[2] in values else 1)' "$expr" "$want" <<<"$json"
}

policy_has_member() {
  local json="$1"
  local role="$2"
  local member="$3"
  python3 -c '
import json
import sys

data = json.load(sys.stdin)
role = sys.argv[1]
member = sys.argv[2]
for binding in data.get("bindings", []):
    if binding.get("role") == role and member in binding.get("members", []):
        sys.exit(0)
sys.exit(1)
' "$role" "$member" <<<"$json"
}

require_value "--tenant" "$tenant"

if ! command -v gcloud >/dev/null 2>&1; then
  echo "error: gcloud is required on PATH" >&2
  exit 1
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "error: python3 is required on PATH" >&2
  exit 1
fi

if [[ -z "$project_id" ]]; then
  project_id="$(gcloud config get-value project 2>/dev/null || true)"
fi
require_value "--project or active gcloud project" "$project_id"

if [[ -z "$issuer" ]]; then
  require_value "--auth-hostname or --issuer" "$auth_hostname"
  issuer="$(normalize_issuer "$auth_hostname")/t/${tenant}"
else
  issuer="$(normalize_issuer "$issuer")"
fi

if [[ -z "$pool_id" ]]; then
  pool_id="sandcastle-${tenant}"
fi
if [[ -z "$service_account" ]]; then
  service_account="sandcastle-${tenant}"
fi

if [[ -n "$machine_project" && -z "$machine_name" ]]; then
  echo "error: --machine-project requires --machine" >&2
  exit 2
fi
if [[ -n "$machine_name" && -z "$machine_project" ]]; then
  echo "error: --machine requires --machine-project" >&2
  exit 2
fi

failed=0
project_number="$(gcloud projects describe "$project_id" --format='value(projectNumber)')"
provider_audience="//iam.googleapis.com/projects/${project_number}/locations/global/workloadIdentityPools/${pool_id}/providers/${provider_id}"
provider_resource="projects/${project_number}/locations/global/workloadIdentityPools/${pool_id}/providers/${provider_id}"

if [[ "$service_account" == *@* ]]; then
  service_account_email="$service_account"
else
  service_account_email="${service_account}@${project_id}.iam.gserviceaccount.com"
fi

if [[ -n "$machine_name" ]]; then
  member="principal://iam.googleapis.com/projects/${project_number}/locations/global/workloadIdentityPools/${pool_id}/subject/machine:${tenant}/${machine_project}/${machine_name}"
else
  member="principalSet://iam.googleapis.com/projects/${project_number}/locations/global/workloadIdentityPools/${pool_id}/attribute.tenant/${tenant}"
fi

cat <<EOF
Sandcastle GCP OIDC verification

GCP project:              ${project_id}
Project number:           ${project_number}
Tenant:                   ${tenant}
Issuer URI:               ${issuer}
Pool ID:                  ${pool_id}
Provider ID:              ${provider_id}
Provider resource:        ${provider_resource}
Expected audience:        ${provider_audience}
Service account:          ${service_account_email}
Expected IAM member:      ${member}

EOF

if gcloud iam workload-identity-pools describe "$pool_id" \
  --project="$project_id" \
  --location="global" >/dev/null 2>&1; then
  pass "Workload Identity Pool exists: $pool_id"
else
  fail "Workload Identity Pool is missing: $pool_id"
fi

provider_json=""
if provider_json="$(gcloud iam workload-identity-pools providers describe "$provider_id" \
  --project="$project_id" \
  --location="global" \
  --workload-identity-pool="$pool_id" \
  --format=json 2>/dev/null)"; then
  pass "OIDC provider exists: $provider_id"

  actual_issuer="$(json_value "$provider_json" 'data.get("oidc", {}).get("issuerUri", "")')"
  if [[ "$actual_issuer" == "$issuer" ]]; then
    pass "Provider issuer matches"
  else
    fail "Provider issuer is '$actual_issuer', want '$issuer'"
  fi

  if json_contains "$provider_json" 'data.get("oidc", {}).get("allowedAudiences", [])' "$provider_audience"; then
    pass "Provider allowed audience includes expected audience"
  else
    fail "Provider allowed audiences do not include '$provider_audience'"
    info "Actual allowed audiences: $(json_value "$provider_json" 'data.get("oidc", {}).get("allowedAudiences", [])')"
  fi

  actual_mapping="$(json_value "$provider_json" 'data.get("attributeMapping", {})')"
  info "Provider attribute mapping: $actual_mapping"

  actual_condition="$(json_value "$provider_json" 'data.get("attributeCondition", "")')"
  if [[ "$actual_condition" == *"assertion.tenant=='${tenant}'"* ]]; then
    pass "Provider attribute condition restricts tenant"
  else
    fail "Provider attribute condition does not restrict tenant as expected: $actual_condition"
  fi
else
  fail "OIDC provider is missing: $provider_id"
fi

if gcloud iam service-accounts describe "$service_account_email" \
  --project="$project_id" >/dev/null 2>&1; then
  pass "Service account exists: $service_account_email"
else
  fail "Service account is missing: $service_account_email"
fi

policy_json=""
if policy_json="$(gcloud iam service-accounts get-iam-policy "$service_account_email" \
  --project="$project_id" \
  --format=json 2>/dev/null)"; then
  if policy_has_member "$policy_json" "roles/iam.workloadIdentityUser" "$member"; then
    pass "Service account allows expected Workload Identity member"
  else
    fail "Service account does not allow expected Workload Identity member"
  fi
else
  fail "Could not read service account IAM policy"
fi

if [[ "$skip_http" -eq 0 ]]; then
  if command -v curl >/dev/null 2>&1; then
    discovery_url="${issuer}/.well-known/openid-configuration"
    jwks_url="${issuer}/.well-known/jwks.json"
    info "Fetching discovery: $discovery_url"
    discovery_json=""
    if discovery_json="$(curl -fsSL "$discovery_url" 2>/dev/null)"; then
      pass "Discovery endpoint is reachable"
      discovered_issuer="$(json_value "$discovery_json" 'data.get("issuer", "")')"
      if [[ "$discovered_issuer" == "$issuer" ]]; then
        pass "Discovery issuer matches"
      else
        fail "Discovery issuer is '$discovered_issuer', want '$issuer'"
      fi
      discovered_jwks="$(json_value "$discovery_json" 'data.get("jwks_uri", "")')"
      info "Discovery JWKS URI: $discovered_jwks"
    else
      fail "Discovery endpoint is not reachable: $discovery_url"
    fi

    info "Fetching JWKS: $jwks_url"
    jwks_json=""
    if jwks_json="$(curl -fsSL "$jwks_url" 2>/dev/null)"; then
      key_count="$(json_value "$jwks_json" 'len(data.get("keys", []))')"
      if [[ "$key_count" -gt 0 ]]; then
        pass "JWKS endpoint has $key_count key(s)"
      else
        fail "JWKS endpoint returned no keys"
      fi
    else
      fail "JWKS endpoint is not reachable: $jwks_url"
    fi
  else
    fail "curl is required for HTTP checks; rerun with --skip-http to skip"
  fi
fi

cat <<EOF

Sandcastle Cloud Identity audience:
${provider_audience}

Service account impersonation URL:
https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/${service_account_email}:generateAccessToken
EOF

if [[ "$failed" -eq 0 ]]; then
  echo
  echo "OIDC setup verification passed."
  exit 0
fi

echo
echo "OIDC setup verification failed." >&2
exit 1
