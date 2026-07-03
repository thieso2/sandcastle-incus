package authapp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type GitHubOAuth struct {
	ClientID     string
	ClientSecret string
}

type GitHubClient interface {
	ExchangeCode(context.Context, GitHubOAuth, string) (string, error)
	Profile(context.Context, string) (GitHubProfile, error)
	VerifyUsername(context.Context, string) (GitHubProfile, error)
}

type GitHubProfile struct {
	Login string
	ID    string
	Email string
}

type HTTPGitHubClient struct {
	HTTPClient *http.Client
}

func (c HTTPGitHubClient) ExchangeCode(ctx context.Context, oauth GitHubOAuth, code string) (string, error) {
	if strings.TrimSpace(oauth.ClientID) == "" || strings.TrimSpace(oauth.ClientSecret) == "" {
		return "", fmt.Errorf("GitHub OAuth client id and secret are required")
	}
	body, _ := json.Marshal(map[string]string{
		"client_id":     oauth.ClientID,
		"client_secret": oauth.ClientSecret,
		"code":          code,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://github.com/login/oauth/access_token", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client().Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("GitHub token exchange failed: %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Error != "" {
		return "", fmt.Errorf("GitHub token exchange failed: %s", firstNonEmpty(payload.Description, payload.Error))
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("GitHub token exchange returned no access token")
	}
	return payload.AccessToken, nil
}

func (c HTTPGitHubClient) Profile(ctx context.Context, accessToken string) (GitHubProfile, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return GitHubProfile{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := c.client().Do(request)
	if err != nil {
		return GitHubProfile{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return GitHubProfile{}, fmt.Errorf("GitHub profile lookup failed: %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	var payload struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return GitHubProfile{}, err
	}
	if strings.TrimSpace(payload.Login) == "" {
		return GitHubProfile{}, fmt.Errorf("GitHub profile did not include a login")
	}
	return GitHubProfile{
		Login: payload.Login,
		ID:    strconv.FormatInt(payload.ID, 10),
		Email: strings.TrimSpace(payload.Email),
	}, nil
}

func (c HTTPGitHubClient) VerifyUsername(ctx context.Context, username string) (GitHubProfile, error) {
	normalized := NormalizeGitHubUsername(username)
	if err := ValidateGitHubUsername(normalized); err != nil {
		return GitHubProfile{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/users/"+url.PathEscape(normalized), nil)
	if err != nil {
		return GitHubProfile{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := c.client().Do(request)
	if err != nil {
		return GitHubProfile{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return GitHubProfile{}, fmt.Errorf("GitHub user %s was not found", normalized)
	}
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return GitHubProfile{}, fmt.Errorf("GitHub user lookup failed: %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	var payload struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
		Email string `json:"email"`
		Type  string `json:"type"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return GitHubProfile{}, err
	}
	if !strings.EqualFold(payload.Type, "User") {
		return GitHubProfile{}, fmt.Errorf("GitHub login %s is %s, want User", normalized, payload.Type)
	}
	if strings.TrimSpace(payload.Login) == "" {
		return GitHubProfile{}, fmt.Errorf("GitHub profile did not include a login")
	}
	return GitHubProfile{
		Login: payload.Login,
		ID:    strconv.FormatInt(payload.ID, 10),
		Email: strings.TrimSpace(payload.Email),
	}, nil
}

func (c HTTPGitHubClient) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// SimulatedGitHubClient is a GitHubClient that fabricates deterministic profiles
// without ever contacting GitHub. It is installed ONLY when the auth-app runs in
// simulated-GitHub mode (a shared secret token is configured) so an end-to-end
// deployment needs no real GitHub OAuth app. It must never be used in production —
// it will happily "verify" any well-formed username.
type SimulatedGitHubClient struct{}

func (SimulatedGitHubClient) ExchangeCode(_ context.Context, _ GitHubOAuth, code string) (string, error) {
	// The simulated flow carries the username as the OAuth "code"; the returned
	// "access token" is just the normalized username (see Profile).
	username := NormalizeGitHubUsername(code)
	if username == "" {
		return "", fmt.Errorf("simulated GitHub: empty code")
	}
	return username, nil
}

func (SimulatedGitHubClient) Profile(_ context.Context, accessToken string) (GitHubProfile, error) {
	return SimulatedGitHubProfile(accessToken)
}

func (SimulatedGitHubClient) VerifyUsername(_ context.Context, username string) (GitHubProfile, error) {
	return SimulatedGitHubProfile(username)
}

// SimulatedGitHubProfile builds a deterministic synthetic profile for a username:
// the GitHub id is a stable hash of the name so repeated logins are the same
// account, and the email is a clearly-fake `.invalid` address.
func SimulatedGitHubProfile(username string) (GitHubProfile, error) {
	normalized := NormalizeGitHubUsername(username)
	if err := ValidateGitHubUsername(normalized); err != nil {
		return GitHubProfile{}, err
	}
	sum := sha256.Sum256([]byte("sandcastle-simulated-github:" + normalized))
	id := binary.BigEndian.Uint64(sum[:8]) >> 1 // keep it positive
	return GitHubProfile{
		Login: normalized,
		ID:    strconv.FormatUint(id, 10),
		Email: normalized + "@simulated.invalid",
	}, nil
}

func GitHubAuthorizeURL(clientID string, state string) string {
	query := url.Values{}
	query.Set("client_id", clientID)
	query.Set("scope", "read:user user:email")
	query.Set("state", state)
	return "https://github.com/login/oauth/authorize?" + query.Encode()
}

func randomToken(bytesLen int) (string, error) {
	data := make([]byte, bytesLen)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
