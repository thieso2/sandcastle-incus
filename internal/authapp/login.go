package authapp

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (h handler) githubLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.TrimSpace(h.githubOAuth.ClientID) == "" {
		http.Error(w, "GitHub OAuth client id is not configured", http.StatusServiceUnavailable)
		return
	}
	state, err := createOAuthState(r.Context(), h.db, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Remember where to land after the OAuth roundtrip (e.g. the device
	// approval page with its user_code). A short-lived cookie survives the
	// redirect to GitHub and back; only local paths are accepted.
	if next := safeLocalRedirect(r.URL.Query().Get("next")); next != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     loginNextCookie,
			Value:    next,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Expires:  time.Now().Add(10 * time.Minute),
		})
	}
	http.Redirect(w, r, GitHubAuthorizeURL(h.githubOAuth.ClientID, state), http.StatusFound)
}

// loginNextCookie carries the post-login destination across the OAuth
// roundtrip.
const loginNextCookie = "sc_login_next"

// safeLocalRedirect returns the value only when it is a same-site path
// ("/device?user_code=…"), guarding against open redirects.
func safeLocalRedirect(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.ContainsAny(value, "\r\n") {
		return value
	}
	return ""
}

func (h handler) githubCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || state == "" {
		http.Error(w, "GitHub OAuth code and state are required", http.StatusBadRequest)
		return
	}
	if err := consumeOAuthState(r.Context(), h.db, state, time.Now()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	accessToken, err := h.githubClient.ExchangeCode(r.Context(), h.githubOAuth, code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	profile, err := h.githubClient.Profile(r.Context(), accessToken)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	normalized := NormalizeGitHubUsername(profile.Login)
	user, err := FindLoginUser(r.Context(), h.db, normalized)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if err := RecordGitHubLogin(r.Context(), h.db, user, profile); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sessionID, err := CreateSession(r.Context(), h.db, user.UserKey, time.Now())
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
		Expires:  time.Now().Add(24 * time.Hour),
	})
	destination := "/"
	if cookie, err := r.Cookie(loginNextCookie); err == nil {
		if next := safeLocalRedirect(cookie.Value); next != "" {
			destination = next
		}
		http.SetCookie(w, &http.Cookie{Name: loginNextCookie, Value: "", Path: "/", MaxAge: -1})
	}
	http.Redirect(w, r, destination, http.StatusFound)
}

func createOAuthState(ctx context.Context, db *sql.DB, now time.Time) (string, error) {
	state, err := randomToken(24)
	if err != nil {
		return "", err
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO oauth_states (state, created_at, expires_at)
VALUES (?, ?, ?)
`, state, now.UTC().Format(time.RFC3339), now.Add(10*time.Minute).UTC().Format(time.RFC3339))
	if err != nil {
		return "", fmt.Errorf("create OAuth state: %w", err)
	}
	return state, nil
}

func consumeOAuthState(ctx context.Context, db *sql.DB, state string, now time.Time) error {
	var expiresAtText string
	if err := db.QueryRowContext(ctx, "SELECT expires_at FROM oauth_states WHERE state = ?", state).Scan(&expiresAtText); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("OAuth state is invalid or already used")
		}
		return err
	}
	expiresAt, err := time.Parse(time.RFC3339, expiresAtText)
	if err != nil {
		return fmt.Errorf("OAuth state expiry is invalid")
	}
	if !now.Before(expiresAt) {
		_, _ = db.ExecContext(ctx, "DELETE FROM oauth_states WHERE state = ?", state)
		return fmt.Errorf("OAuth state expired")
	}
	result, err := db.ExecContext(ctx, "DELETE FROM oauth_states WHERE state = ?", state)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return fmt.Errorf("OAuth state is invalid or already used")
	}
	return nil
}
