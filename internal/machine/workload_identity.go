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
	GCPCredentialPath          = WorkloadDir + "/gcp-credential.json"
	WorkloadProfileEnvPath     = "/etc/profile.d/sandcastle-workload-identity.sh"
	workloadSecretFileMode     = 0o600
	workloadMetadataFileMode   = 0o644
	workloadProfileEnvFileMode = 0o644
)

type WorkloadIdentityRequest struct {
	TokenEndpoint string
	RuntimeSecret string
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
		{Path: WorkloadProfileEnvPath, Content: []byte(workloadProfileEnv(request.GCP)), Mode: workloadProfileEnvFileMode},
	}
	if request.GCP != nil {
		credential, err := GCPExternalAccountCredential(*request.GCP, GCPCredentialPath)
		if err != nil {
			return nil, err
		}
		files = append(files, File{Path: GCPCredentialPath, Content: credential, Mode: workloadMetadataFileMode})
	}
	return files, nil
}

func GCPExternalAccountCredential(config GCPWorkloadIdentityConfig, credentialPath string) ([]byte, error) {
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
		"credential_source":                 map[string]any{"file": credentialPath, "format": map[string]string{"type": "text"}},
		"service_account_impersonation_url": strings.TrimSpace(config.ServiceAccountImpersonationURL),
	}
	if payload["service_account_impersonation_url"] == "" {
		delete(payload, "service_account_impersonation_url")
	}
	return json.MarshalIndent(payload, "", "  ")
}

func workloadProfileEnv(gcp *GCPWorkloadIdentityConfig) string {
	lines := []string{
		"export SANDCASTLE_WORKLOAD_RUNTIME_SECRET_FILE=" + shellQuote(WorkloadRuntimeSecretPath),
		"export SANDCASTLE_WORKLOAD_TOKEN_ENDPOINT_FILE=" + shellQuote(WorkloadTokenEndpointPath),
	}
	if gcp != nil {
		lines = append(lines,
			"export GOOGLE_APPLICATION_CREDENTIALS="+shellQuote(GCPCredentialPath),
			"export CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE="+shellQuote(GCPCredentialPath),
		)
	}
	return strings.Join(lines, "\n") + "\n"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
