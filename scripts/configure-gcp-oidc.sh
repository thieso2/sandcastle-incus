#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/configure-gcp-oidc.sh --tenant TENANT (--auth-hostname HOST | --issuer ISSUER) [options]

Configures Google Cloud Workload Identity Federation for one Sandcastle tenant
using the currently selected gcloud project by default.

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
  --display-name NAME          Display name prefix. Defaults to Sandcastle TENANT.
  --role ROLE                  Project IAM role to grant to the service account.
                               Can be repeated, for example --role roles/storage.objectAdmin.
  --machine-project PROJECT    Restrict impersonation to one Sandcastle project.
                               Requires --machine.
  --machine MACHINE            Restrict impersonation to one machine subject.
  --skip-api-enable            Do not enable required Google APIs.
  -h, --help                   Show this help.

Examples:
  scripts/configure-gcp-oidc.sh \
    --tenant acme \
    --auth-hostname big.thieso2.dev

  scripts/configure-gcp-oidc.sh \
    --tenant acme \
    --auth-hostname auth.big.thieso2.dev \
    --role roles/storage.objectAdmin

  scripts/configure-gcp-oidc.sh \
    --tenant acme \
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
display_name=""
machine_project=""
machine_name=""
skip_api_enable=0
roles=()

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
    --display-name)
      display_name="${2:-}"
      shift 2
      ;;
    --role)
      roles+=("${2:-}")
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
    --skip-api-enable)
      skip_api_enable=1
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

run() {
  echo "+ $*"
  "$@"
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

require_value "--tenant" "$tenant"

if ! command -v gcloud >/dev/null 2>&1; then
  echo "error: gcloud is required on PATH" >&2
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
if [[ -z "$display_name" ]]; then
  display_name="Sandcastle ${tenant}"
fi

if [[ -n "$machine_project" && -z "$machine_name" ]]; then
  echo "error: --machine-project requires --machine" >&2
  exit 2
fi
if [[ -n "$machine_name" && -z "$machine_project" ]]; then
  echo "error: --machine requires --machine-project" >&2
  exit 2
fi

for role in "${roles[@]+"${roles[@]}"}"; do
  require_value "--role value" "$role"
done

project_number="$(gcloud projects describe "$project_id" --format='value(projectNumber)')"
provider_audience="//iam.googleapis.com/projects/${project_number}/locations/global/workloadIdentityPools/${pool_id}/providers/${provider_id}"
provider_resource="projects/${project_number}/locations/global/workloadIdentityPools/${pool_id}/providers/${provider_id}"

if [[ "$skip_api_enable" -eq 0 ]]; then
  run gcloud services enable \
    iam.googleapis.com \
    iamcredentials.googleapis.com \
    sts.googleapis.com \
    cloudresourcemanager.googleapis.com \
    --project="$project_id"
fi

if gcloud iam workload-identity-pools describe "$pool_id" \
  --project="$project_id" \
  --location="global" >/dev/null 2>&1; then
  echo "Workload Identity Pool already exists: $pool_id"
else
  run gcloud iam workload-identity-pools create "$pool_id" \
    --project="$project_id" \
    --location="global" \
    --display-name="$display_name" \
    --description="Sandcastle workload identities for ${tenant}"
fi

attribute_mapping="google.subject=assertion.sub,attribute.tenant=assertion.tenant,attribute.project=assertion.project,attribute.machine=assertion.machine"
attribute_condition="assertion.tenant=='${tenant}'"

if gcloud iam workload-identity-pools providers describe "$provider_id" \
  --project="$project_id" \
  --location="global" \
  --workload-identity-pool="$pool_id" >/dev/null 2>&1; then
  run gcloud iam workload-identity-pools providers update-oidc "$provider_id" \
    --project="$project_id" \
    --location="global" \
    --workload-identity-pool="$pool_id" \
    --issuer-uri="$issuer" \
    --allowed-audiences="$provider_audience" \
    --attribute-mapping="$attribute_mapping" \
    --attribute-condition="$attribute_condition"
else
  run gcloud iam workload-identity-pools providers create-oidc "$provider_id" \
    --project="$project_id" \
    --location="global" \
    --workload-identity-pool="$pool_id" \
    --display-name="Sandcastle" \
    --issuer-uri="$issuer" \
    --allowed-audiences="$provider_audience" \
    --attribute-mapping="$attribute_mapping" \
    --attribute-condition="$attribute_condition"
fi

if [[ "$service_account" == *@* ]]; then
  service_account_email="$service_account"
  if ! gcloud iam service-accounts describe "$service_account_email" \
    --project="$project_id" >/dev/null 2>&1; then
    echo "error: service account $service_account_email does not exist" >&2
    exit 1
  fi
else
  service_account_email="${service_account}@${project_id}.iam.gserviceaccount.com"
  if gcloud iam service-accounts describe "$service_account_email" \
    --project="$project_id" >/dev/null 2>&1; then
    echo "Service account already exists: $service_account_email"
  else
    run gcloud iam service-accounts create "$service_account" \
      --project="$project_id" \
      --display-name="$display_name"
  fi
fi

if [[ -n "$machine_name" ]]; then
  member="principal://iam.googleapis.com/projects/${project_number}/locations/global/workloadIdentityPools/${pool_id}/subject/machine:${tenant}/${machine_project}/${machine_name}"
else
  member="principalSet://iam.googleapis.com/projects/${project_number}/locations/global/workloadIdentityPools/${pool_id}/attribute.tenant/${tenant}"
fi

run gcloud iam service-accounts add-iam-policy-binding "$service_account_email" \
  --project="$project_id" \
  --role="roles/iam.workloadIdentityUser" \
  --member="$member"

for role in "${roles[@]+"${roles[@]}"}"; do
  run gcloud projects add-iam-policy-binding "$project_id" \
    --member="serviceAccount:${service_account_email}" \
    --role="$role"
done

cat <<EOF

Configured Sandcastle GCP Workload Identity Federation.

GCP project:              ${project_id}
Project number:           ${project_number}
Issuer URI:               ${issuer}
Provider resource:        ${provider_resource}
Sandcastle GCP audience:  ${provider_audience}
Service account:          ${service_account_email}
Impersonation member:     ${member}

Use this Sandcastle Cloud Identity audience:
${provider_audience}

Use this service account impersonation URL when needed:
https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/${service_account_email}:generateAccessToken

Note: this provider accepts the audience printed above. Sandcastle workload
JWTs must include an aud claim equal to that value.
EOF
