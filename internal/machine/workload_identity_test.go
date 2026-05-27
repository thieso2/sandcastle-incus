package machine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
	if !strings.Contains(string(byPath[WorkloadProfileEnvPath].Content), "GOOGLE_EXTERNAL_ACCOUNT_ALLOW_EXECUTABLES") {
		t.Fatalf("env file = %s", byPath[WorkloadProfileEnvPath].Content)
	}
	if string(byPath[WorkloadGCPAudiencePath].Content) == "" {
		t.Fatalf("audience file = %#v", byPath[WorkloadGCPAudiencePath])
	}
	if byPath[WorkloadTokenHelperPath].Mode != 0o755 || !strings.Contains(string(byPath[WorkloadTokenHelperPath].Content), "gcp-executable") {
		t.Fatalf("token helper = %#v", byPath[WorkloadTokenHelperPath])
	}
	var credential map[string]any
	if err := json.Unmarshal(byPath[GCPCredentialPath].Content, &credential); err != nil {
		t.Fatal(err)
	}
	if credential["type"] != "external_account" || credential["audience"] == "" {
		t.Fatalf("credential = %#v", credential)
	}
	source, ok := credential["credential_source"].(map[string]any)
	if !ok {
		t.Fatalf("credential source = %#v", credential["credential_source"])
	}
	executable, ok := source["executable"].(map[string]any)
	if !ok {
		t.Fatalf("credential source = %#v", source)
	}
	if !strings.Contains(executable["command"].(string), WorkloadTokenHelperPath+" gcp-executable") {
		t.Fatalf("executable source = %#v", executable)
	}
	if _, ok := source["file"]; ok {
		t.Fatalf("credential source self-references a file: %#v", source)
	}
	if byPath[GCPCredentialPath].Mode != 0o600 {
		t.Fatalf("credential file mode = %#v", byPath[GCPCredentialPath])
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

func TestWorkloadIdentityFilesIncludeTokenHelperWithoutGCP(t *testing.T) {
	files, err := WorkloadIdentityFiles(&WorkloadIdentityRequest{
		TokenEndpoint: "https://auth.example.com/internal/workload/token",
		RuntimeSecret: "secret",
		Tenant:        "acme",
		Project:       "default",
		Machine:       "codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]File{}
	for _, file := range files {
		byPath[file.Path] = file
	}
	if byPath[WorkloadTokenHelperPath].Mode != 0o755 {
		t.Fatalf("helper file = %#v", byPath[WorkloadTokenHelperPath])
	}
	if _, ok := byPath[GCPCredentialPath]; ok {
		t.Fatalf("unexpected GCP credential file: %#v", byPath[GCPCredentialPath])
	}
}

func TestWorkloadTokenHelperExecutableRequestsAudienceToken(t *testing.T) {
	var request map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"access_token":"jwt-token","expires_in":900}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "runtime-secret"), "secret\n", 0o600)
	writeTestFile(t, filepath.Join(dir, "token-endpoint"), server.URL+"\n", 0o644)
	writeTestFile(t, filepath.Join(dir, "gcp-audience"), "aud\n", 0o644)
	writeTestFile(t, filepath.Join(dir, "tenant"), "acme\n", 0o644)
	writeTestFile(t, filepath.Join(dir, "project"), "default\n", 0o644)
	writeTestFile(t, filepath.Join(dir, "machine"), "codex\n", 0o644)
	helperPath := filepath.Join(dir, "sandcastle-workload-token")
	writeTestFile(t, helperPath, workloadTokenHelperScript(), 0o755)

	cmd := exec.Command(helperPath, "gcp-executable")
	cmd.Env = append(os.Environ(),
		"SANDCASTLE_WORKLOAD_RUNTIME_SECRET_FILE="+filepath.Join(dir, "runtime-secret"),
		"SANDCASTLE_WORKLOAD_TOKEN_ENDPOINT_FILE="+filepath.Join(dir, "token-endpoint"),
		"SANDCASTLE_WORKLOAD_AUDIENCE_FILE="+filepath.Join(dir, "gcp-audience"),
		"SANDCASTLE_WORKLOAD_TENANT_FILE="+filepath.Join(dir, "tenant"),
		"SANDCASTLE_WORKLOAD_PROJECT_FILE="+filepath.Join(dir, "project"),
		"SANDCASTLE_WORKLOAD_MACHINE_FILE="+filepath.Join(dir, "machine"),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper failed: %v\n%s", err, output)
	}
	if request["tenant"] != "acme" || request["project"] != "default" || request["machine"] != "codex" || request["runtime_secret"] != "secret" || request["audience"] != "aud" {
		t.Fatalf("request = %#v", request)
	}
	var executable struct {
		Version        int    `json:"version"`
		Success        bool   `json:"success"`
		TokenType      string `json:"token_type"`
		IDToken        string `json:"id_token"`
		ExpirationTime int64  `json:"expiration_time"`
	}
	if err := json.Unmarshal(output, &executable); err != nil {
		t.Fatalf("output = %s: %v", output, err)
	}
	if !executable.Success || executable.IDToken != "jwt-token" || executable.TokenType != "urn:ietf:params:oauth:token-type:jwt" || executable.ExpirationTime == 0 {
		t.Fatalf("executable output = %#v", executable)
	}
}

func writeTestFile(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
