package machine

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	WorkloadDir                = "/var/lib/sandcastle/workload"
	WorkloadRuntimeSecretPath  = WorkloadDir + "/runtime-secret"
	WorkloadTokenEndpointPath  = WorkloadDir + "/token-endpoint"
	WorkloadGCPAudiencePath    = WorkloadDir + "/gcp-audience"
	WorkloadTenantPath         = WorkloadDir + "/tenant"
	WorkloadProjectPath        = WorkloadDir + "/project"
	WorkloadMachinePath        = WorkloadDir + "/machine"
	GCPCredentialPath          = WorkloadDir + "/gcp-credential.json"
	WorkloadTokenHelperPath    = "/usr/local/bin/sandcastle-workload-token"
	WorkloadProfileEnvPath     = "/etc/profile.d/sandcastle-workload-identity.sh"
	workloadSecretFileMode     = 0o600
	workloadMetadataFileMode   = 0o644
	workloadExecutableFileMode = 0o755
	gcpCredentialFileMode      = 0o600
	workloadProfileEnvFileMode = 0o644
)

type WorkloadIdentityRequest struct {
	TokenEndpoint string
	RuntimeSecret string
	Tenant        string
	Project       string
	Machine       string
	GCP           *GCPWorkloadIdentityConfig
}

type GCPWorkloadIdentityConfig struct {
	Audience                       string
	SubjectTokenType               string
	ServiceAccountImpersonationURL string
}

func WorkloadIdentityFiles(request *WorkloadIdentityRequest) ([]File, error) {
	if request == nil {
		return nil, nil
	}
	if strings.TrimSpace(request.TokenEndpoint) == "" {
		return nil, fmt.Errorf("workload identity token endpoint is required")
	}
	if strings.TrimSpace(request.RuntimeSecret) == "" {
		return nil, fmt.Errorf("workload identity runtime secret is required")
	}
	files := []File{
		{Path: WorkloadRuntimeSecretPath, Content: []byte(strings.TrimSpace(request.RuntimeSecret) + "\n"), Mode: workloadSecretFileMode},
		{Path: WorkloadTokenEndpointPath, Content: []byte(strings.TrimSpace(request.TokenEndpoint) + "\n"), Mode: workloadMetadataFileMode},
		{Path: WorkloadTokenHelperPath, Content: []byte(workloadTokenHelperScript()), Mode: workloadExecutableFileMode},
		{Path: WorkloadProfileEnvPath, Content: []byte(workloadProfileEnv(request)), Mode: workloadProfileEnvFileMode},
	}
	if request.Tenant != "" {
		files = append(files, File{Path: WorkloadTenantPath, Content: []byte(request.Tenant + "\n"), Mode: workloadMetadataFileMode})
	}
	if request.Project != "" {
		files = append(files, File{Path: WorkloadProjectPath, Content: []byte(request.Project + "\n"), Mode: workloadMetadataFileMode})
	}
	if request.Machine != "" {
		files = append(files, File{Path: WorkloadMachinePath, Content: []byte(request.Machine + "\n"), Mode: workloadMetadataFileMode})
	}
	if request.GCP != nil {
		audience := strings.TrimSpace(request.GCP.Audience)
		if audience == "" {
			return nil, fmt.Errorf("GCP workload identity audience is required")
		}
		files = append(files, File{Path: WorkloadGCPAudiencePath, Content: []byte(audience + "\n"), Mode: workloadMetadataFileMode})
		credential, err := GCPExternalAccountCredential(*request.GCP, WorkloadTokenHelperPath)
		if err != nil {
			return nil, err
		}
		files = append(files, File{Path: GCPCredentialPath, Content: credential, Mode: gcpCredentialFileMode})
	}
	return files, nil
}

func GCPExternalAccountCredential(config GCPWorkloadIdentityConfig, executablePath string) ([]byte, error) {
	audience := strings.TrimSpace(config.Audience)
	if audience == "" {
		return nil, fmt.Errorf("GCP workload identity audience is required")
	}
	subjectTokenType := strings.TrimSpace(config.SubjectTokenType)
	if subjectTokenType == "" {
		subjectTokenType = "urn:ietf:params:oauth:token-type:jwt"
	}
	payload := map[string]any{
		"type":                              "external_account",
		"audience":                          audience,
		"subject_token_type":                subjectTokenType,
		"token_url":                         "https://sts.googleapis.com/v1/token",
		"credential_source":                 map[string]any{"executable": map[string]any{"command": strings.TrimSpace(executablePath) + " gcp-executable", "timeout_millis": 30000}},
		"service_account_impersonation_url": strings.TrimSpace(config.ServiceAccountImpersonationURL),
	}
	if payload["service_account_impersonation_url"] == "" {
		delete(payload, "service_account_impersonation_url")
	}
	return json.MarshalIndent(payload, "", "  ")
}

func workloadProfileEnv(req *WorkloadIdentityRequest) string {
	lines := []string{
		"export SANDCASTLE_WORKLOAD_RUNTIME_SECRET_FILE=" + shellQuote(WorkloadRuntimeSecretPath),
		"export SANDCASTLE_WORKLOAD_TOKEN_ENDPOINT_FILE=" + shellQuote(WorkloadTokenEndpointPath),
	}
	if req.Tenant != "" {
		lines = append(lines, "export SANDCASTLE_TENANT="+shellQuote(req.Tenant))
	}
	if req.Project != "" {
		lines = append(lines, "export SANDCASTLE_PROJECT="+shellQuote(req.Project))
	}
	if req.Machine != "" {
		lines = append(lines, "export SANDCASTLE_MACHINE="+shellQuote(req.Machine))
	}
	if req.GCP != nil {
		lines = append(lines,
			"export SANDCASTLE_WORKLOAD_AUDIENCE_FILE="+shellQuote(WorkloadGCPAudiencePath),
			"export GOOGLE_EXTERNAL_ACCOUNT_ALLOW_EXECUTABLES='1'",
			"export GOOGLE_APPLICATION_CREDENTIALS="+shellQuote(GCPCredentialPath),
			"export CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE="+shellQuote(GCPCredentialPath),
		)
	}
	return strings.Join(lines, "\n") + "\n"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func workloadTokenHelperScript() string {
	return `#!/bin/sh
mode="${1:-token}"
explicit_audience="${2:-}"
case "$mode" in
  token|gcp-executable) ;;
  *) explicit_audience="$mode"; mode="token" ;;
esac
runtime_secret_file="${SANDCASTLE_WORKLOAD_RUNTIME_SECRET_FILE:-/var/lib/sandcastle/workload/runtime-secret}"
token_endpoint_file="${SANDCASTLE_WORKLOAD_TOKEN_ENDPOINT_FILE:-/var/lib/sandcastle/workload/token-endpoint}"
audience_file="${SANDCASTLE_WORKLOAD_AUDIENCE_FILE:-/var/lib/sandcastle/workload/gcp-audience}"
tenant_file="${SANDCASTLE_WORKLOAD_TENANT_FILE:-/var/lib/sandcastle/workload/tenant}"
project_file="${SANDCASTLE_WORKLOAD_PROJECT_FILE:-/var/lib/sandcastle/workload/project}"
machine_file="${SANDCASTLE_WORKLOAD_MACHINE_FILE:-/var/lib/sandcastle/workload/machine}"

endpoint="$(cat "$token_endpoint_file")"
runtime_secret="$(cat "$runtime_secret_file")"
tenant="$(cat "$tenant_file")"
project="$(cat "$project_file")"
machine="$(cat "$machine_file")"
audience="${explicit_audience:-${SANDCASTLE_WORKLOAD_AUDIENCE:-}}"
if [ -z "$audience" ] && [ -f "$audience_file" ]; then
  audience="$(cat "$audience_file")"
fi
if [ -z "$audience" ]; then
  printf '%s\n' "workload identity audience is required" >&2
  exit 1
fi

request="$(
  TENANT="$tenant" PROJECT="$project" MACHINE="$machine" RUNTIME_SECRET="$runtime_secret" AUDIENCE="$audience" python3 - <<'PY'
import json
import os

print(json.dumps({
    "tenant": os.environ["TENANT"],
    "project": os.environ["PROJECT"],
    "machine": os.environ["MACHINE"],
    "runtime_secret": os.environ["RUNTIME_SECRET"],
    "audience": os.environ["AUDIENCE"],
}))
PY
)"

if ! response="$(printf '%s' "$request" | curl -fsS -X POST "$endpoint" -H 'Content-Type: application/json' --data-binary @- 2>&1)"; then
  if [ "$mode" = "gcp-executable" ]; then
    RESPONSE_ERROR="$response" python3 - <<'PY'
import json
import os

print(json.dumps({
    "version": 1,
    "success": False,
    "code": "SANDCASTLE_WORKLOAD_TOKEN_REQUEST_FAILED",
    "message": os.environ.get("RESPONSE_ERROR", ""),
}))
PY
  fi
  printf '%s\n' "$response" >&2
  exit 1
fi

case "$mode" in
  token)
    RESPONSE="$response" python3 - <<'PY'
import json
import os

print(json.loads(os.environ["RESPONSE"])["access_token"])
PY
    ;;
  gcp-executable)
    RESPONSE="$response" python3 - <<'PY'
import json
import os
import time

body = json.loads(os.environ["RESPONSE"])
print(json.dumps({
    "version": 1,
    "success": True,
    "token_type": "urn:ietf:params:oauth:token-type:jwt",
    "id_token": body["access_token"],
    "expiration_time": int(time.time()) + int(body.get("expires_in", 900)) - 30,
}))
PY
    ;;
  *)
    printf 'unknown mode: %s\n' "$mode" >&2
    exit 2
    ;;
esac
`
}
