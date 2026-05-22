package machine

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkloadIdentityFilesInjectRuntimeSecretEndpointAndGCPConfig(t *testing.T) {
	files, err := WorkloadIdentityFiles(&WorkloadIdentityRequest{
		TokenEndpoint: "https://auth.example.com/internal/workload/token",
		RuntimeSecret: "secret",
		GCP: &GCPWorkloadIdentityConfig{
			Audience:                       "//iam.googleapis.com/projects/123/locations/global/workloadIdentityPools/pool/providers/provider",
			ServiceAccountImpersonationURL: "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/test@example.iam.gserviceaccount.com:generateAccessToken",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]File{}
	for _, file := range files {
		byPath[file.Path] = file
	}
	if string(byPath[WorkloadRuntimeSecretPath].Content) != "secret\n" || byPath[WorkloadRuntimeSecretPath].Mode != 0o600 {
		t.Fatalf("runtime secret file = %#v", byPath[WorkloadRuntimeSecretPath])
	}
	if !strings.Contains(string(byPath[WorkloadProfileEnvPath].Content), "GOOGLE_APPLICATION_CREDENTIALS") {
		t.Fatalf("env file = %s", byPath[WorkloadProfileEnvPath].Content)
	}
	var credential map[string]any
	if err := json.Unmarshal(byPath[GCPCredentialPath].Content, &credential); err != nil {
		t.Fatal(err)
	}
	if credential["type"] != "external_account" || credential["audience"] == "" {
		t.Fatalf("credential = %#v", credential)
	}
}

func TestWorkloadIdentityFilesAreOptInOnly(t *testing.T) {
	files, err := WorkloadIdentityFiles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("files = %#v", files)
	}
}
