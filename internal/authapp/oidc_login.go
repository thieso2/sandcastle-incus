package authapp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OIDCLoginConfig configures browser login through an external OIDC Identity
// Provider (authorization code flow with PKCE). An empty ClientSecret makes
// the Auth App a public client.
type OIDCLoginConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	DisplayName  string
	// RedirectURL is https://<Auth Hostname>/oauth/oidc/callback.
	RedirectURL string
	HTTPClient  *http.Client
}

// OIDCLogin is a configured OIDC login provider. It loads the provider's
// discovery document on first use and caches it; signing keys are cached by
// kid and refetched when a token names an unknown one.
type OIDCLogin struct {
	config OIDCLoginConfig

	mu            sync.Mutex
	endpoints     *oidcEndpoints
	keys          []oidcJWK
	keysFetchedAt time.Time
}

type oidcEndpoints struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
}

const (
	oidcLoginScope       = "openid email profile"
	oidcKeyRefetchPeriod = time.Minute
	oidcClockSkew        = 2 * time.Minute
	oidcDefaultName      = "SSO"
)

// NewOIDCLogin validates the configuration without contacting the provider.
func NewOIDCLogin(config OIDCLoginConfig) (*OIDCLogin, error) {
	config.Issuer = strings.TrimSpace(config.Issuer)
	config.ClientID = strings.TrimSpace(config.ClientID)
	config.ClientSecret = strings.TrimSpace(config.ClientSecret)
	config.DisplayName = strings.TrimSpace(config.DisplayName)
	if config.DisplayName == "" {
		config.DisplayName = oidcDefaultName
	}
	if err := requireOIDCURL("OIDC issuer", config.Issuer); err != nil {
		return nil, err
	}
	if config.ClientID == "" {
		return nil, fmt.Errorf("OIDC client id is required with an OIDC issuer")
	}
	if strings.TrimSpace(config.RedirectURL) == "" {
		return nil, fmt.Errorf("OIDC login needs the Auth Hostname for its redirect URL")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &OIDCLogin{config: config}, nil
}

// requireOIDCURL accepts https URLs, and http only on loopback so a local
// test provider works.
func requireOIDCURL(label string, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%s %q is not an absolute URL", label, value)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		host := parsed.Hostname()
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("%s %q must use https", label, value)
}

func oidcRedirectURL(authHostname string) string {
	if authHostname == "" {
		return ""
	}
	return "https://" + authHostname + "/oauth/oidc/callback"
}

func (o *OIDCLogin) DisplayName() string { return o.config.DisplayName }

// Discover loads (or returns the cached) discovery document. A failure is not
// cached, so the next login attempt retries.
func (o *OIDCLogin) Discover(ctx context.Context) (*oidcEndpoints, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.endpoints != nil {
		return o.endpoints, nil
	}
	discoveryURL := strings.TrimRight(o.config.Issuer, "/") + "/.well-known/openid-configuration"
	var doc oidcEndpoints
	if err := o.getJSON(ctx, discoveryURL, &doc); err != nil {
		return nil, fmt.Errorf("OIDC discovery for %s: %w", o.config.Issuer, err)
	}
	if doc.Issuer != o.config.Issuer {
		return nil, fmt.Errorf("OIDC discovery for %s names issuer %q", o.config.Issuer, doc.Issuer)
	}
	for label, value := range map[string]string{
		"authorization_endpoint": doc.AuthorizationEndpoint,
		"token_endpoint":         doc.TokenEndpoint,
		"jwks_uri":               doc.JWKSURI,
	} {
		if err := requireOIDCURL("OIDC "+label, value); err != nil {
			return nil, err
		}
	}
	if len(doc.CodeChallengeMethods) > 0 && !containsString(doc.CodeChallengeMethods, "S256") {
		return nil, fmt.Errorf("OIDC provider %s does not support PKCE S256", o.config.Issuer)
	}
	o.endpoints = &doc
	return o.endpoints, nil
}

func (o *OIDCLogin) getJSON(ctx context.Context, target string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := o.config.HTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", target, response.Status)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(out)
}

// signingKeys returns the cached keys for kid, refetching the JWKS when kid
// is unknown and the last fetch is older than oidcKeyRefetchPeriod.
func (o *OIDCLogin) signingKeys(ctx context.Context, jwksURI string, kid string, now time.Time) ([]oidcJWK, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if matches := matchJWKs(o.keys, kid); len(matches) > 0 {
		return matches, nil
	}
	if !o.keysFetchedAt.IsZero() && now.Sub(o.keysFetchedAt) < oidcKeyRefetchPeriod {
		return nil, fmt.Errorf("no OIDC signing key %q", kid)
	}
	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := o.getJSON(ctx, jwksURI, &set); err != nil {
		return nil, fmt.Errorf("fetch OIDC signing keys: %w", err)
	}
	var keys []oidcJWK
	for _, raw := range set.Keys {
		if key, err := parseOIDCJWK(raw); err == nil {
			keys = append(keys, key)
		}
	}
	o.keys = keys
	o.keysFetchedAt = now
	if matches := matchJWKs(o.keys, kid); len(matches) > 0 {
		return matches, nil
	}
	return nil, fmt.Errorf("no OIDC signing key %q", kid)
}

func matchJWKs(keys []oidcJWK, kid string) []oidcJWK {
	var out []oidcJWK
	for _, key := range keys {
		if kid == "" || key.KID == kid {
			out = append(out, key)
		}
	}
	return out
}

func (o *OIDCLogin) authorizeURL(endpoints *oidcEndpoints, state string, nonce string, verifier string) (string, error) {
	target, err := url.Parse(endpoints.AuthorizationEndpoint)
	if err != nil {
		return "", err
	}
	query := target.Query()
	query.Set("response_type", "code")
	query.Set("client_id", o.config.ClientID)
	query.Set("redirect_uri", o.config.RedirectURL)
	query.Set("scope", oidcLoginScope)
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", pkceChallenge(verifier))
	query.Set("code_challenge_method", "S256")
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// exchangeCode redeems the authorization code and returns the raw ID token.
func (o *OIDCLogin) exchangeCode(ctx context.Context, endpoints *oidcEndpoints, code string, verifier string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", o.config.RedirectURL)
	form.Set("code_verifier", verifier)
	if o.config.ClientSecret == "" {
		form.Set("client_id", o.config.ClientID)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoints.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	if o.config.ClientSecret != "" {
		// RFC 6749 §2.3.1: form-encode both parts before Basic encoding.
		request.SetBasicAuth(url.QueryEscape(o.config.ClientID), url.QueryEscape(o.config.ClientSecret))
	}
	response, err := o.config.HTTPClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var payload struct {
		IDToken     string `json:"id_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	_ = json.Unmarshal(data, &payload)
	if response.StatusCode != http.StatusOK || payload.Error != "" {
		return "", fmt.Errorf("OIDC token exchange failed: %s %s", response.Status, firstNonEmpty(payload.Description, payload.Error))
	}
	if payload.IDToken == "" {
		return "", fmt.Errorf("OIDC token exchange returned no id_token")
	}
	return payload.IDToken, nil
}

func createOIDCState(ctx context.Context, db *sql.DB, verifier string, nonce string, now time.Time) (string, error) {
	state, err := randomToken(24)
	if err != nil {
		return "", err
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO oauth_states (state, created_at, expires_at, pkce_verifier, nonce)
VALUES (?, ?, ?, ?, ?)
`, state, now.UTC().Format(time.RFC3339), now.Add(10*time.Minute).UTC().Format(time.RFC3339), verifier, nonce); err != nil {
		return "", fmt.Errorf("create OAuth state: %w", err)
	}
	return state, nil
}

// consumeOIDCState deletes the state and returns its PKCE verifier and nonce.
// GitHub states carry no verifier and never match.
func consumeOIDCState(ctx context.Context, db *sql.DB, state string, now time.Time) (string, string, error) {
	var verifier, nonce, expiresAtText string
	err := db.QueryRowContext(ctx, `
DELETE FROM oauth_states WHERE state = ? AND pkce_verifier <> ''
RETURNING pkce_verifier, nonce, expires_at
`, state).Scan(&verifier, &nonce, &expiresAtText)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("OAuth state is invalid or already used")
	}
	if err != nil {
		return "", "", err
	}
	expiresAt, err := time.Parse(time.RFC3339, expiresAtText)
	if err != nil || !now.Before(expiresAt) {
		return "", "", fmt.Errorf("OAuth state expired")
	}
	return verifier, nonce, nil
}

func (h handler) oidcLoginStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	endpoints, err := h.oidcLogin.Discover(r.Context())
	if err != nil {
		http.Error(w, h.oidcLogin.DisplayName()+" sign-in is unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	verifier, err := randomToken(32)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	nonce, err := randomToken(24)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	state, err := createOIDCState(r.Context(), h.db, verifier, nonce, timeNow())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	target, err := h.oidcLogin.authorizeURL(endpoints, state, nonce, verifier)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	setLoginNextCookie(w, r.URL.Query().Get("next"))
	http.Redirect(w, r, target, http.StatusFound)
}

func (h handler) oidcLoginCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	if providerError := strings.TrimSpace(query.Get("error")); providerError != "" {
		http.Error(w, h.oidcLogin.DisplayName()+" sign-in failed: "+firstNonEmpty(query.Get("error_description"), providerError), http.StatusForbidden)
		return
	}
	code := strings.TrimSpace(query.Get("code"))
	state := strings.TrimSpace(query.Get("state"))
	if code == "" || state == "" {
		http.Error(w, "OIDC code and state are required", http.StatusBadRequest)
		return
	}
	verifier, nonce, err := consumeOIDCState(r.Context(), h.db, state, timeNow())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	endpoints, err := h.oidcLogin.Discover(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	rawIDToken, err := h.oidcLogin.exchangeCode(r.Context(), endpoints, code, verifier)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	claims, err := h.oidcLogin.verifyIDToken(r.Context(), endpoints, rawIDToken, nonce, timeNow())
	if err != nil {
		http.Error(w, "invalid ID token: "+err.Error(), http.StatusForbidden)
		return
	}
	user, err := ResolveOIDCLogin(r.Context(), h.db, endpoints.Issuer, claims.Subject, claims.Email, claims.EmailVerified == nil || *claims.EmailVerified, timeNow())
	if errors.Is(err, errOIDCLoginDenied) {
		http.Error(w, fmt.Sprintf("%s account %s is not allowlisted for Sandcastle", h.oidcLogin.DisplayName(), firstNonEmpty(claims.Email, claims.Subject)), http.StatusForbidden)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.startWebSession(w, r, user.UserKey)
}

// loginURL is where an unauthenticated browser goes to sign in. Without OIDC
// it is the GitHub login, exactly as before OIDC existed.
func (h handler) loginURL(next string) string {
	path := "/login/github"
	if h.oidcLogin != nil {
		path = "/login"
		if strings.TrimSpace(h.githubOAuth.ClientID) == "" {
			path = "/login/oidc"
		}
	}
	return withNext(path, next)
}

func (h handler) loginLabel() string {
	switch h.loginURL("") {
	case "/login/github":
		return "Sign in with GitHub"
	case "/login/oidc":
		return "Sign in with " + h.oidcLogin.DisplayName()
	}
	return "Sign in"
}

// loginChooser is /login, registered only when OIDC login is configured.
func (h handler) loginChooser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	next := safeLocalRedirect(r.URL.Query().Get("next"))
	if strings.TrimSpace(h.githubOAuth.ClientID) == "" {
		http.Redirect(w, r, withNext("/login/oidc", next), http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = loginChooserTemplate.Execute(w, struct {
		GitHubURL string
		OIDCURL   string
		OIDCName  string
	}{
		GitHubURL: withNext("/login/github", next),
		OIDCURL:   withNext("/login/oidc", next),
		OIDCName:  h.oidcLogin.DisplayName(),
	})
}

func withNext(path string, next string) string {
	if next == "" {
		return path
	}
	return path + "?next=" + url.QueryEscape(next)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

var loginChooserTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <link rel="stylesheet" href="/style.css">
  <title>Sandcastle Sign-in</title>
</head>
<body>
  <main>
    <h1>Sign in to Sandcastle</h1>
    <p><a href="{{.OIDCURL}}">Sign in with {{.OIDCName}}</a></p>
    <p><a href="{{.GitHubURL}}">Sign in with GitHub</a></p>
  </main>
</body>
</html>
`))
