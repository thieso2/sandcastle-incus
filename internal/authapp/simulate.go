package authapp

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// simulateLogin is a token-gated backdoor that simulates the OUTCOME of a GitHub
// OAuth login without ever contacting GitHub. It is registered only when the
// auth-app is started with a simulate-github token (see NewHandler). Given the
// shared token and a `username` it:
//
//  1. allowlists the user (idempotent),
//  2. records the login,
//  3. if a device `user_code` is supplied, approves that pending CLI device login,
//  4. mints the browser session cookie.
//
// This lets an end-to-end deployment run with NO real GitHub OAuth app. It is a
// deliberate backdoor — never enable it in production. It accepts GET (query
// params) or POST (form) so both browsers and the CLI can drive it.
func (h handler) simulateLogin(w http.ResponseWriter, r *http.Request) {
	if h.simulateToken == "" {
		http.NotFound(w, r)
		return
	}
	provided := strings.TrimSpace(r.FormValue("token"))
	if subtle.ConstantTimeCompare([]byte(provided), []byte(h.simulateToken)) != 1 {
		http.Error(w, "invalid simulate token", http.StatusForbidden)
		return
	}
	profile, err := SimulatedGitHubProfile(r.FormValue("username"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	user, err := AllowlistGitHubUser(r.Context(), h.db, profile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := RecordGitHubLogin(r.Context(), h.db, user, profile); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if code := strings.ToUpper(strings.TrimSpace(r.FormValue("user_code"))); code != "" {
		if err := ApproveDeviceLogin(r.Context(), h.db, code, user.UserKey, timeNow()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	sessionID, err := CreateSession(r.Context(), h.db, user.UserKey, timeNow())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     h.sessionCookie,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  timeNow().Add(24 * time.Hour),
	})
	if redirect := strings.TrimSpace(r.FormValue("redirect")); redirect != "" {
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "simulated GitHub login for %s\n", user.UserKey)
}
