package authapp

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeIdPClientID = "sandcastle-test"

// fakeIdP is an in-process OIDC provider: discovery, an ES256 JWKS, and a
// token endpoint that checks the PKCE verifier against the challenge the
// browser carried to the authorize endpoint.
type fakeIdP struct {
	t      *testing.T
	server *httptest.Server
	key    *ecdsa.PrivateKey
	kid    string

	mu     sync.Mutex
	grants map[string]fakeIdPGrant
	// pkceFailures counts token requests whose verifier did not match.
	pkceFailures int
}

type fakeIdPGrant struct {
	challenge   string
	redirectURI string
	claims      map[string]any
	signer      *ecdsa.PrivateKey
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	idp := &fakeIdP{t: t, key: key, kid: "ring-1", grants: map[string]fakeIdPGrant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.issuer(),
			"authorization_endpoint":                idp.issuer() + "/authorize",
			"token_endpoint":                        idp.issuer() + "/token",
			"jwks_uri":                              idp.issuer() + "/jwks",
			"response_types_supported":              []string{"code"},
			"code_challenge_methods_supported":      []string{"S256"},
			"id_token_signing_alg_values_supported": []string{"ES256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		public := idp.key.PublicKey
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "EC", "crv": "P-256", "use": "sig", "alg": "ES256", "kid": idp.kid,
			"x": base64.RawURLEncoding.EncodeToString(public.X.FillBytes(make([]byte, 32))),
			"y": base64.RawURLEncoding.EncodeToString(public.Y.FillBytes(make([]byte, 32))),
		}}})
	})
	mux.HandleFunc("/token", idp.token)
	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func (idp *fakeIdP) issuer() string { return idp.server.URL }

func (idp *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("client_id") != fakeIdPClientID {
		http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	idp.mu.Lock()
	grant, ok := idp.grants[r.Form.Get("code")]
	delete(idp.grants, r.Form.Get("code"))
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	pkceOK := ok && base64.RawURLEncoding.EncodeToString(sum[:]) == grant.challenge
	if ok && !pkceOK {
		idp.pkceFailures++
	}
	idp.mu.Unlock()
	if !ok || !pkceOK || r.Form.Get("redirect_uri") != grant.redirectURI {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{
		"access_token": "unused",
		"token_type":   "Bearer",
		"id_token":     signES256(idp.t, grant.signer, idp.kid, grant.claims),
	})
}

func signES256(t *testing.T, key *ecdsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "typ": "JWT", "kid": kid})
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// oidcAccount is what the fake IdP asserts for the next login.
type oidcAccount struct {
	subject string
	email   string
	// mutate edits the claims (or the signer) to forge a bad token.
	mutate func(claims map[string]any, grant *fakeIdPGrant)
}

func (idp *fakeIdP) login(t *testing.T, handler http.Handler, next string, account oidcAccount) *httptest.ResponseRecorder {
	t.Helper()
	return idp.loginWith(t, handler, next, account, nil)
}

// loginWith drives /login/oidc, the provider's authorize step, and the
// callback. beforeCallback runs between authorize and callback.
func (idp *fakeIdP) loginWith(t *testing.T, handler http.Handler, next string, account oidcAccount, beforeCallback func()) *httptest.ResponseRecorder {
	t.Helper()
	target := "/login/oidc"
	if next != "" {
		target += "?next=" + url.QueryEscape(next)
	}
	start := httptest.NewRecorder()
	handler.ServeHTTP(start, httptest.NewRequest(http.MethodGet, target, nil))
	if start.Code != http.StatusFound {
		t.Fatalf("/login/oidc = %d %q", start.Code, start.Body.String())
	}
	authorize, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := authorize.Query()
	if got := authorize.Scheme + "://" + authorize.Host + authorize.Path; got != idp.issuer()+"/authorize" {
		t.Fatalf("authorize endpoint = %q", got)
	}
	for key, want := range map[string]string{
		"response_type":         "code",
		"client_id":             fakeIdPClientID,
		"redirect_uri":          "https://auth.example.com/oauth/oidc/callback",
		"scope":                 "openid email profile",
		"code_challenge_method": "S256",
	} {
		if query.Get(key) != want {
			t.Fatalf("authorize %s = %q, want %q", key, query.Get(key), want)
		}
	}
	if query.Get("state") == "" || query.Get("nonce") == "" || query.Get("code_challenge") == "" {
		t.Fatalf("authorize query = %v", query)
	}
	now := time.Now()
	grant := fakeIdPGrant{
		challenge:   query.Get("code_challenge"),
		redirectURI: query.Get("redirect_uri"),
		signer:      idp.key,
		claims: map[string]any{
			"iss":   idp.issuer(),
			"aud":   fakeIdPClientID,
			"sub":   account.subject,
			"email": account.email,
			"name":  "Test User",
			"nonce": query.Get("nonce"),
			"iat":   now.Unix(),
			"exp":   now.Add(5 * time.Minute).Unix(),
		},
	}
	if account.mutate != nil {
		account.mutate(grant.claims, &grant)
	}
	code, _ := randomToken(16)
	idp.mu.Lock()
	idp.grants[code] = grant
	idp.mu.Unlock()
	if beforeCallback != nil {
		beforeCallback()
	}
	callback := httptest.NewRequest(http.MethodGet, "/oauth/oidc/callback?code="+code+"&state="+url.QueryEscape(query.Get("state")), nil)
	for _, cookie := range start.Result().Cookies() {
		callback.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, callback)
	return response
}

func oidcHandlerForTest(t *testing.T, db *sql.DB, idp *fakeIdP, options HandlerOptions) http.Handler {
	t.Helper()
	login, err := NewOIDCLogin(OIDCLoginConfig{
		Issuer:      idp.issuer(),
		ClientID:    fakeIdPClientID,
		DisplayName: "Ring",
		RedirectURL: oidcRedirectURL("auth.example.com"),
	})
	if err != nil {
		t.Fatal(err)
	}
	options.AuthHostname = "auth.example.com"
	options.OIDCLogin = login
	return NewHandler(db, options)
}

func sessionUserForTest(t *testing.T, db *sql.DB, response *httptest.ResponseRecorder) string {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "sandcastle_session" {
			user, err := UserForSession(context.Background(), db, cookie.Value, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			return user.UserKey
		}
	}
	return ""
}

func assertOIDCLoginDenied(t *testing.T, db *sql.DB, response *httptest.ResponseRecorder, wantCode int) {
	t.Helper()
	if response.Code != wantCode {
		t.Fatalf("callback = %d %q, want %d", response.Code, response.Body.String(), wantCode)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "sandcastle_session" {
			t.Fatalf("denied login set a session cookie")
		}
	}
	var sessions int
	if err := db.QueryRow("SELECT count(*) FROM web_sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 {
		t.Fatalf("web sessions = %d, want 0", sessions)
	}
}

func TestOIDCLoginBindsSubjectByEmailThenMatchesBySubject(t *testing.T) {
	db := authDBForTest(t)
	idp := newFakeIdP(t)
	handler := oidcHandlerForTest(t, db, idp, HandlerOptions{})
	if _, err := AllowlistOIDCUser(context.Background(), db, idp.issuer(), "Alice", "Alice@Example.com", false); err != nil {
		t.Fatal(err)
	}

	first := idp.login(t, handler, "/device?user_code=ABCD-EFGH", oidcAccount{subject: "sub-alice", email: "alice@example.com"})
	if first.Code != http.StatusFound || first.Header().Get("Location") != "/device?user_code=ABCD-EFGH" {
		t.Fatalf("first login = %d location %q body %q", first.Code, first.Header().Get("Location"), first.Body.String())
	}
	if got := sessionUserForTest(t, db, first); got != "alice" {
		t.Fatalf("session user = %q", got)
	}
	var subject string
	if err := db.QueryRow("SELECT subject FROM oidc_identities WHERE user_key = 'alice'").Scan(&subject); err != nil {
		t.Fatal(err)
	}
	if subject != "sub-alice" {
		t.Fatalf("bound subject = %q", subject)
	}

	second := idp.login(t, handler, "", oidcAccount{subject: "sub-alice", email: "alice@renamed.example"})
	if second.Code != http.StatusFound || second.Header().Get("Location") != "/" {
		t.Fatalf("second login = %d location %q body %q", second.Code, second.Header().Get("Location"), second.Body.String())
	}
	if got := sessionUserForTest(t, db, second); got != "alice" {
		t.Fatalf("second session user = %q", got)
	}
}

func TestOIDCLoginRejectsUnknownEmail(t *testing.T) {
	db := authDBForTest(t)
	idp := newFakeIdP(t)
	handler := oidcHandlerForTest(t, db, idp, HandlerOptions{})
	response := idp.login(t, handler, "", oidcAccount{subject: "sub-x", email: "stranger@example.com"})
	assertOIDCLoginDenied(t, db, response, http.StatusForbidden)
}

func TestOIDCLoginBoundIdentityIsNotMatchedByEmailForAnotherSubject(t *testing.T) {
	db := authDBForTest(t)
	idp := newFakeIdP(t)
	handler := oidcHandlerForTest(t, db, idp, HandlerOptions{})
	if _, err := AllowlistOIDCUser(context.Background(), db, idp.issuer(), "alice", "alice@example.com", false); err != nil {
		t.Fatal(err)
	}
	if first := idp.login(t, handler, "", oidcAccount{subject: "sub-alice", email: "alice@example.com"}); first.Code != http.StatusFound {
		t.Fatalf("binding login = %d %q", first.Code, first.Body.String())
	}
	if _, err := db.Exec("DELETE FROM web_sessions"); err != nil {
		t.Fatal(err)
	}
	takeover := idp.login(t, handler, "", oidcAccount{subject: "sub-mallory", email: "alice@example.com"})
	assertOIDCLoginDenied(t, db, takeover, http.StatusForbidden)
}

func TestOIDCLoginRejectsForgedTokens(t *testing.T) {
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(map[string]any, *fakeIdPGrant){
		"bad signature": func(_ map[string]any, grant *fakeIdPGrant) { grant.signer = otherKey },
		"wrong aud":     func(claims map[string]any, _ *fakeIdPGrant) { claims["aud"] = "someone-else" },
		"wrong iss":     func(claims map[string]any, _ *fakeIdPGrant) { claims["iss"] = "https://evil.example" },
		"expired": func(claims map[string]any, _ *fakeIdPGrant) {
			claims["iat"] = time.Now().Add(-time.Hour).Unix()
			claims["exp"] = time.Now().Add(-30 * time.Minute).Unix()
		},
		"wrong nonce":   func(claims map[string]any, _ *fakeIdPGrant) { claims["nonce"] = "replayed" },
		"multi aud azp": func(claims map[string]any, _ *fakeIdPGrant) { claims["aud"] = []string{fakeIdPClientID, "other"} },
		"no sub":        func(claims map[string]any, _ *fakeIdPGrant) { claims["sub"] = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			db := authDBForTest(t)
			idp := newFakeIdP(t)
			handler := oidcHandlerForTest(t, db, idp, HandlerOptions{})
			if _, err := AllowlistOIDCUser(context.Background(), db, idp.issuer(), "alice", "alice@example.com", false); err != nil {
				t.Fatal(err)
			}
			response := idp.login(t, handler, "", oidcAccount{subject: "sub-alice", email: "alice@example.com", mutate: mutate})
			assertOIDCLoginDenied(t, db, response, http.StatusForbidden)
			if !strings.Contains(response.Body.String(), "invalid ID token") {
				t.Fatalf("body = %q", response.Body.String())
			}
		})
	}
}

func TestOIDCLoginSendsPKCEVerifier(t *testing.T) {
	db := authDBForTest(t)
	idp := newFakeIdP(t)
	handler := oidcHandlerForTest(t, db, idp, HandlerOptions{})
	if _, err := AllowlistOIDCUser(context.Background(), db, idp.issuer(), "alice", "alice@example.com", false); err != nil {
		t.Fatal(err)
	}
	response := idp.loginWith(t, handler, "", oidcAccount{subject: "sub-alice", email: "alice@example.com"}, func() {
		if _, err := db.Exec("UPDATE oauth_states SET pkce_verifier = 'not-the-verifier'"); err != nil {
			t.Fatal(err)
		}
	})
	assertOIDCLoginDenied(t, db, response, http.StatusBadGateway)
	if idp.pkceFailures != 1 {
		t.Fatalf("PKCE failures = %d, want 1", idp.pkceFailures)
	}
	// The untampered flow passes the same endpoint check.
	if ok := idp.login(t, handler, "", oidcAccount{subject: "sub-alice", email: "alice@example.com"}); ok.Code != http.StatusFound {
		t.Fatalf("login = %d %q", ok.Code, ok.Body.String())
	}
	if idp.pkceFailures != 1 {
		t.Fatalf("PKCE failures = %d after a clean login", idp.pkceFailures)
	}
}

func TestOIDCOnlyUserCannotLogInThroughGitHub(t *testing.T) {
	db := authDBForTest(t)
	idp := newFakeIdP(t)
	if _, err := AllowlistOIDCUser(context.Background(), db, idp.issuer(), "bob", "bob@example.com", false); err != nil {
		t.Fatal(err)
	}
	handler := oidcHandlerForTest(t, db, idp, HandlerOptions{
		GitHubClientID:     "client",
		GitHubClientSecret: "secret",
		GitHub:             NewMockGitHubOAuth(GitHubProfile{Login: "bob", ID: "99"}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/oauth/github/callback?code=bob&state="+createStateForTest(t, db), nil))
	assertOIDCLoginDenied(t, db, response, http.StatusForbidden)
}

func TestOIDCLinkKeepsGitHubLoginForExistingUser(t *testing.T) {
	db := authDBForTest(t)
	idp := newFakeIdP(t)
	if _, err := AllowlistGitHubUser(context.Background(), db, GitHubProfile{Login: "OctoCat", ID: "583231"}); err != nil {
		t.Fatal(err)
	}
	handler := oidcHandlerForTest(t, db, idp, HandlerOptions{
		GitHubClientID:     "client",
		GitHubClientSecret: "secret",
		GitHub:             NewMockGitHubOAuth(GitHubProfile{Login: "OctoCat", ID: "583231"}),
	})
	link := httptest.NewRequest(http.MethodPost, "/admin/allowlist/oidc", strings.NewReader("user_key=octocat&email=octo@example.com"))
	link.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	link.AddCookie(adminSessionCookieForTest(t, db))
	linked := httptest.NewRecorder()
	handler.ServeHTTP(linked, link)
	if linked.Code != http.StatusSeeOther {
		t.Fatalf("link = %d %q", linked.Code, linked.Body.String())
	}

	github := httptest.NewRecorder()
	handler.ServeHTTP(github, httptest.NewRequest(http.MethodGet, "/oauth/github/callback?code=octocat&state="+createStateForTest(t, db), nil))
	if github.Code != http.StatusFound || sessionUserForTest(t, db, github) != "octocat" {
		t.Fatalf("GitHub login = %d %q", github.Code, github.Body.String())
	}
	oidc := idp.login(t, handler, "", oidcAccount{subject: "sub-octo", email: "octo@example.com"})
	if oidc.Code != http.StatusFound || sessionUserForTest(t, db, oidc) != "octocat" {
		t.Fatalf("OIDC login = %d %q", oidc.Code, oidc.Body.String())
	}
	var users int
	if err := db.QueryRow("SELECT count(*) FROM users WHERE user_key <> 'admin'").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 1 {
		t.Fatalf("users = %d, want 1", users)
	}

	page := httptest.NewRequest(http.MethodGet, "/admin/allowlist", nil)
	page.AddCookie(adminSessionCookieForTest(t, db))
	listing := httptest.NewRecorder()
	handler.ServeHTTP(listing, page)
	if !strings.Contains(listing.Body.String(), "octo@example.com (bound)") || !strings.Contains(listing.Body.String(), "Add Ring user") {
		t.Fatalf("allowlist page:\n%s", listing.Body.String())
	}
}

func TestAdminOIDCFormCreatesOIDCOnlyUser(t *testing.T) {
	db := authDBForTest(t)
	idp := newFakeIdP(t)
	handler := oidcHandlerForTest(t, db, idp, HandlerOptions{})
	for _, body := range []string{"user_key=bad_key!&email=x@example.com", "user_key=carol&email=not-an-email"} {
		request := httptest.NewRequest(http.MethodPost, "/admin/allowlist/oidc", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(adminSessionCookieForTest(t, db))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %q", body, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/allowlist/oidc", strings.NewReader("user_key=Carol&email=Carol@Example.com"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(adminSessionCookieForTest(t, db))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("add = %d %q", response.Code, response.Body.String())
	}
	var githubLogin, allowlisted, admin int
	var email string
	if err := db.QueryRow(`
SELECT users.github_login, users.allowlisted, users.sandcastle_admin, oidc_identities.email
FROM users JOIN oidc_identities ON oidc_identities.user_key = users.user_key
WHERE users.user_key = 'carol'`).Scan(&githubLogin, &allowlisted, &admin, &email); err != nil {
		t.Fatal(err)
	}
	if githubLogin != 0 || allowlisted != 1 || admin != 0 || email != "carol@example.com" {
		t.Fatalf("carol = github_login %d allowlisted %d admin %d email %q", githubLogin, allowlisted, admin, email)
	}
}

func TestBootstrapOIDCAdminsIsIdempotent(t *testing.T) {
	db := authDBForTest(t)
	plan, err := PlanServe(ServeRequest{
		Address:             ":0",
		DatabasePath:        "auth.db",
		AuthHostname:        "auth.example.com",
		OIDCIssuer:          "https://ring.example.com",
		OIDCClientID:        "sandcastle",
		BootstrapOIDCAdmins: []string{"Dana=Dana@Example.com", ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := BootstrapOIDCAdmins(context.Background(), db, plan.OIDCIssuer, plan.BootstrapOIDCAdmins); err != nil {
			t.Fatal(err)
		}
	}
	var count, githubLogin, admin int
	if err := db.QueryRow("SELECT count(*), MAX(github_login), MAX(sandcastle_admin) FROM users WHERE user_key = 'dana'").Scan(&count, &githubLogin, &admin); err != nil {
		t.Fatal(err)
	}
	if count != 1 || githubLogin != 0 || admin != 1 {
		t.Fatalf("dana = count %d github_login %d admin %d", count, githubLogin, admin)
	}
	if _, err := PlanServe(ServeRequest{Address: ":0", DatabasePath: "auth.db", BootstrapOIDCAdmins: []string{"dana=dana@example.com"}}); err == nil {
		t.Fatal("bootstrap OIDC admin without an issuer was accepted")
	}
	if _, err := PlanServe(ServeRequest{Address: ":0", DatabasePath: "auth.db", AuthHostname: "auth.example.com", OIDCIssuer: "http://ring.example.com", OIDCClientID: "x"}); err == nil {
		t.Fatal("plain-http OIDC issuer was accepted")
	}
}

func TestLoginRoutesWithoutOIDCAreUnchanged(t *testing.T) {
	db := authDBForTest(t)
	handler := NewHandler(db, HandlerOptions{AuthHostname: "auth.example.com", GitHubClientID: "client"})

	device := httptest.NewRecorder()
	handler.ServeHTTP(device, httptest.NewRequest(http.MethodGet, "/device?user_code=ABCD-EFGH", nil))
	if device.Code != http.StatusFound || device.Header().Get("Location") != "/login/github?next=%2Fdevice%3Fuser_code%3DABCD-EFGH" {
		t.Fatalf("device = %d %q", device.Code, device.Header().Get("Location"))
	}
	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(status.Body.String(), `<p><a href="/login/github">Sign in with GitHub</a></p>`) {
		t.Fatalf("status page:\n%s", status.Body.String())
	}
	for _, path := range []string{"/login", "/login/oidc", "/oauth/oidc/callback?code=x&state=y", "/admin/allowlist/oidc"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", path, response.Code)
		}
	}
}

func TestLoginChooserWithOIDC(t *testing.T) {
	db := authDBForTest(t)
	idp := newFakeIdP(t)

	both := oidcHandlerForTest(t, db, idp, HandlerOptions{GitHubClientID: "client"})
	device := httptest.NewRecorder()
	both.ServeHTTP(device, httptest.NewRequest(http.MethodGet, "/device?user_code=ABCD-EFGH", nil))
	if device.Header().Get("Location") != "/login?next=%2Fdevice%3Fuser_code%3DABCD-EFGH" {
		t.Fatalf("device location = %q", device.Header().Get("Location"))
	}
	chooser := httptest.NewRecorder()
	both.ServeHTTP(chooser, httptest.NewRequest(http.MethodGet, "/login?next=%2Fdevice%3Fuser_code%3DABCD-EFGH", nil))
	for _, want := range []string{
		`href="/login/oidc?next=%2Fdevice%3Fuser_code%3DABCD-EFGH">Sign in with Ring`,
		`href="/login/github?next=%2Fdevice%3Fuser_code%3DABCD-EFGH">Sign in with GitHub`,
	} {
		if !strings.Contains(chooser.Body.String(), want) {
			t.Fatalf("chooser missing %q:\n%s", want, chooser.Body.String())
		}
	}
	status := httptest.NewRecorder()
	both.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(status.Body.String(), `<a href="/login">Sign in</a>`) {
		t.Fatalf("status page:\n%s", status.Body.String())
	}

	oidcOnly := oidcHandlerForTest(t, db, idp, HandlerOptions{})
	onlyDevice := httptest.NewRecorder()
	oidcOnly.ServeHTTP(onlyDevice, httptest.NewRequest(http.MethodGet, "/device?user_code=ABCD-EFGH", nil))
	if onlyDevice.Header().Get("Location") != "/login/oidc?next=%2Fdevice%3Fuser_code%3DABCD-EFGH" {
		t.Fatalf("OIDC-only device location = %q", onlyDevice.Header().Get("Location"))
	}
	onlyChooser := httptest.NewRecorder()
	oidcOnly.ServeHTTP(onlyChooser, httptest.NewRequest(http.MethodGet, "/login", nil))
	if onlyChooser.Code != http.StatusFound || onlyChooser.Header().Get("Location") != "/login/oidc" {
		t.Fatalf("OIDC-only /login = %d %q", onlyChooser.Code, onlyChooser.Header().Get("Location"))
	}
}

func TestOIDCLoginUnavailableProviderAnswers503(t *testing.T) {
	db := authDBForTest(t)
	idp := newFakeIdP(t)
	handler := oidcHandlerForTest(t, db, idp, HandlerOptions{})
	idp.server.Close()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/login/oidc", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "Ring sign-in is unavailable") {
		t.Fatalf("/login/oidc = %d %q", response.Code, response.Body.String())
	}
}

func TestMigrateKeepsGitHubLoginForExistingUsers(t *testing.T) {
	db, err := OpenDatabase(t.TempDir() + "/auth.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
CREATE TABLE users (
    user_key TEXT PRIMARY KEY,
    github_username TEXT NOT NULL,
    github_username_normalized TEXT NOT NULL UNIQUE,
    github_account_id TEXT NOT NULL DEFAULT '',
    github_email TEXT NOT NULL DEFAULT '',
    ssh_public_key TEXT NOT NULL DEFAULT '',
    ssh_key_fingerprint TEXT NOT NULL DEFAULT '',
    allowlisted INTEGER NOT NULL DEFAULT 0,
    sandcastle_admin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE oauth_states (state TEXT PRIMARY KEY, created_at TEXT NOT NULL, expires_at TEXT NOT NULL);
INSERT INTO users (user_key, github_username, github_username_normalized, allowlisted, created_at, updated_at)
VALUES ('octocat', 'OctoCat', 'octocat', 1, datetime('now'), datetime('now'));
`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var githubLogin int
	if err := db.QueryRow("SELECT github_login FROM users WHERE user_key = 'octocat'").Scan(&githubLogin); err != nil {
		t.Fatal(err)
	}
	if githubLogin != 1 {
		t.Fatalf("github_login = %d, want 1", githubLogin)
	}
	if _, err := FindLoginUser(context.Background(), db, "octocat"); err != nil {
		t.Fatalf("existing user lost GitHub login: %v", err)
	}
}

func TestParseOIDCJWKRejectsShortRSAKeys(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{
		"kty": "RSA", "kid": "weak",
		"n": base64.RawURLEncoding.EncodeToString(new(big.Int).Lsh(big.NewInt(1), 1023).Bytes()),
		"e": "AQAB",
	})
	if _, err := parseOIDCJWK(raw); err == nil {
		t.Fatal("1024-bit RSA key accepted")
	}
}

func TestVerifyJWSSignatureAcceptsRS256(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{
		"kty": "RSA", "kid": "rsa-1", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	})
	jwk, err := parseOIDCJWK(raw)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("header.payload"))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if !verifyJWSSignature([]oidcJWK{jwk}, "RS256", digest[:], signature) {
		t.Fatal("RS256 signature rejected")
	}
	if verifyJWSSignature([]oidcJWK{jwk}, "ES256", digest[:], signature) {
		t.Fatal("RSA key accepted for ES256")
	}
}
