package authapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCloudIdentityConfigsAreUserOwnedCRUD(t *testing.T) {
	db := authDBForTest(t)
	config, err := UpsertCloudIdentityConfig(context.Background(), db, CloudIdentityConfig{
		UserKey:     "octocat",
		Name:        "prod",
		GCPAudience: "//iam.googleapis.com/projects/123/locations/global/workloadIdentityPools/pool/providers/provider",
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.UserKey != "octocat" || config.Provider != "gcp" {
		t.Fatalf("config = %#v", config)
	}
	other, err := ListCloudIdentityConfigs(context.Background(), db, "hubot")
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("other user configs = %#v", other)
	}
	if _, err := UpsertCloudIdentityConfig(context.Background(), db, CloudIdentityConfig{
		UserKey:     "octocat",
		Name:        "prod",
		GCPAudience: "updated",
	}); err != nil {
		t.Fatal(err)
	}
	configs, err := ListCloudIdentityConfigs(context.Background(), db, "octocat")
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || configs[0].GCPAudience != "updated" {
		t.Fatalf("configs = %#v", configs)
	}
	if err := DeleteCloudIdentityConfig(context.Background(), db, "octocat", "prod"); err != nil {
		t.Fatal(err)
	}
	configs, err = ListCloudIdentityConfigs(context.Background(), db, "octocat")
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 0 {
		t.Fatalf("deleted configs = %#v", configs)
	}
}

func TestMachineWorkloadIdentitySelectsUserOwnedCloudConfig(t *testing.T) {
	db := authDBForTest(t)
	if _, err := UpsertCloudIdentityConfig(context.Background(), db, CloudIdentityConfig{
		UserKey:                           "octocat",
		Name:                              "prod",
		GCPAudience:                       "aud",
		GCPSubjectTokenType:               "jwt",
		GCPServiceAccountImpersonationURL: "impersonate",
	}); err != nil {
		t.Fatal(err)
	}
	request, err := MachineWorkloadIdentityForCloudConfig(context.Background(), db, "octocat", "prod", "https://auth.example.com/internal/workload/token", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if request.TokenEndpoint == "" || request.RuntimeSecret != "secret" || request.GCP.Audience != "aud" || request.GCP.ServiceAccountImpersonationURL != "impersonate" {
		t.Fatalf("request = %#v", request)
	}
	if _, err := MachineWorkloadIdentityForCloudConfig(context.Background(), db, "hubot", "prod", "endpoint", "secret"); err == nil {
		t.Fatal("expected owner isolation error")
	}
}

func TestCloudIdentityConfigUIUsesSessionOwner(t *testing.T) {
	db := authDBForTest(t)
	if _, err := AllowlistGitHubUser(context.Background(), db, GitHubProfile{Login: "octocat", ID: "1"}); err != nil {
		t.Fatal(err)
	}
	sessionID, err := CreateSession(context.Background(), db, "octocat", timeNow())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, HandlerOptions{})
	request := httptest.NewRequest(http.MethodPost, "/cloud-identities", strings.NewReader("name=prod&gcp_audience=aud"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "sandcastle_session", Value: sessionID})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("save = %d %q", response.Code, response.Body.String())
	}
	configs, err := ListCloudIdentityConfigs(context.Background(), db, "octocat")
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || configs[0].Name != "prod" {
		t.Fatalf("configs = %#v", configs)
	}
}
