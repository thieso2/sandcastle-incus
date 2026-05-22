package authapp

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMockGitHubOAuthExchangesRegisteredProfile(t *testing.T) {
	ctx := context.Background()
	mock := NewMockGitHubOAuth(GitHubProfile{Login: "OctoCat", ID: "583231", Email: "octo@example.com"})

	token, err := mock.ExchangeCode(ctx, GitHubOAuth{}, "octocat")
	if err != nil {
		t.Fatal(err)
	}
	if token != "octocat" {
		t.Fatalf("token = %q", token)
	}
	profile, err := mock.Profile(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Login != "OctoCat" || profile.ID != "583231" || profile.Email != "octo@example.com" {
		t.Fatalf("profile = %#v", profile)
	}
	verified, err := mock.VerifyUsername(ctx, "OCTOCAT")
	if err != nil {
		t.Fatal(err)
	}
	if verified.Login != "OctoCat" {
		t.Fatalf("verified = %#v", verified)
	}
}

func TestMockGitHubOAuthRejectsUnknownAndDeniedCodes(t *testing.T) {
	ctx := context.Background()
	mock := NewMockGitHubOAuth(GitHubProfile{Login: "octocat", ID: "583231"})

	if _, err := mock.ExchangeCode(ctx, GitHubOAuth{}, "hubot"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("unknown code error = %v", err)
	}
	if _, err := mock.VerifyUsername(ctx, "hubot"); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("unknown user error = %v", err)
	}
	denied := errors.New("mock denied")
	mock.DenyCode("octocat", denied)
	if _, err := mock.ExchangeCode(ctx, GitHubOAuth{}, "octocat"); !errors.Is(err, denied) {
		t.Fatalf("denied code error = %v", err)
	}
}
