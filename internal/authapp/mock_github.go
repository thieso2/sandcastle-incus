package authapp

import (
	"context"
	"fmt"
)

type MockGitHubOAuth struct {
	Profiles map[string]GitHubProfile
	Tokens   map[string]string
	Errors   map[string]error
}

func NewMockGitHubOAuth(profiles ...GitHubProfile) *MockGitHubOAuth {
	mock := &MockGitHubOAuth{
		Profiles: map[string]GitHubProfile{},
		Tokens:   map[string]string{},
		Errors:   map[string]error{},
	}
	for _, profile := range profiles {
		mock.AddProfile(profile)
	}
	return mock
}

func (m *MockGitHubOAuth) AddProfile(profile GitHubProfile) {
	if m.Profiles == nil {
		m.Profiles = map[string]GitHubProfile{}
	}
	if m.Tokens == nil {
		m.Tokens = map[string]string{}
	}
	normalized := NormalizeGitHubUsername(profile.Login)
	m.Profiles[normalized] = profile
	m.Tokens[normalized] = normalized
}

func (m *MockGitHubOAuth) DenyCode(code string, err error) {
	if m.Errors == nil {
		m.Errors = map[string]error{}
	}
	m.Errors[code] = err
}

func (m *MockGitHubOAuth) ExchangeCode(ctx context.Context, oauth GitHubOAuth, code string) (string, error) {
	if err := m.Errors[code]; err != nil {
		return "", err
	}
	token, ok := m.Tokens[NormalizeGitHubUsername(code)]
	if !ok {
		return "", fmt.Errorf("mock GitHub OAuth code %s is not registered", code)
	}
	return token, nil
}

func (m *MockGitHubOAuth) Profile(ctx context.Context, accessToken string) (GitHubProfile, error) {
	profile, ok := m.Profiles[NormalizeGitHubUsername(accessToken)]
	if !ok {
		return GitHubProfile{}, fmt.Errorf("mock GitHub token %s is not registered", accessToken)
	}
	return profile, nil
}

func (m *MockGitHubOAuth) VerifyUsername(ctx context.Context, username string) (GitHubProfile, error) {
	profile, ok := m.Profiles[NormalizeGitHubUsername(username)]
	if !ok {
		return GitHubProfile{}, fmt.Errorf("mock GitHub user %s was not found", username)
	}
	return profile, nil
}
