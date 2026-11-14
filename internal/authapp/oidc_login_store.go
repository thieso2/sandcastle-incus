package authapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// OIDCIdentity links one user to one account at an external OIDC Identity
// Provider. The admin allowlists an email; the first login with that email
// binds the provider's stable subject, and from then on only the subject
// matches.
type OIDCIdentity struct {
	UserKey string
	Issuer  string
	Email   string
	Subject string
	BoundAt string
}

// OIDCAdmin is one --bootstrap-oidc-admin entry.
type OIDCAdmin struct {
	UserKey string
	Email   string
}

var errOIDCLoginDenied = errors.New("this account is not allowlisted for Sandcastle")

func migrateOIDCLogin(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS oidc_identities (
    user_key TEXT PRIMARY KEY REFERENCES users(user_key) ON DELETE CASCADE,
    issuer TEXT NOT NULL,
    email TEXT NOT NULL,
    subject TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    bound_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS oidc_identities_subject ON oidc_identities(issuer, subject) WHERE subject <> '';
CREATE UNIQUE INDEX IF NOT EXISTS oidc_identities_email ON oidc_identities(issuer, email);
`); err != nil {
		return fmt.Errorf("migrate oidc identities: %w", err)
	}
	if err := ensureColumn(ctx, db, "oauth_states", "pkce_verifier", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return ensureColumn(ctx, db, "oauth_states", "nonce", "TEXT NOT NULL DEFAULT ''")
}

func normalizeOIDCEmail(email string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	local, domain, ok := strings.Cut(normalized, "@")
	if !ok || local == "" || domain == "" || strings.ContainsAny(normalized, " \t\r\n,") {
		return "", fmt.Errorf("invalid email %q", email)
	}
	return normalized, nil
}

// ParseOIDCAdmin parses "<user_key>=<email>".
func ParseOIDCAdmin(value string) (OIDCAdmin, error) {
	key, email, ok := strings.Cut(strings.TrimSpace(value), "=")
	if !ok {
		return OIDCAdmin{}, fmt.Errorf("OIDC admin %q: want <user_key>=<email>", value)
	}
	key = NormalizeGitHubUsername(key)
	if err := ValidateGitHubUsername(key); err != nil {
		return OIDCAdmin{}, fmt.Errorf("OIDC admin %q: invalid user key", value)
	}
	normalized, err := normalizeOIDCEmail(email)
	if err != nil {
		return OIDCAdmin{}, fmt.Errorf("OIDC admin %q: %w", value, err)
	}
	return OIDCAdmin{UserKey: key, Email: normalized}, nil
}

// AllowlistOIDCUser allowlists userKey for OIDC login as email at issuer. An
// existing user is linked and keeps its GitHub login setting; a new user is
// created without GitHub login, so a GitHub account that happens to share the
// user key's name can never sign in as them. Pointing an identity at a
// different issuer or email drops its bound subject.
func AllowlistOIDCUser(ctx context.Context, db *sql.DB, issuer string, userKey string, email string, admin bool) (User, error) {
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		return User{}, fmt.Errorf("OIDC login is not configured")
	}
	key := NormalizeGitHubUsername(userKey)
	if err := ValidateGitHubUsername(key); err != nil {
		return User{}, err
	}
	normalizedEmail, err := normalizeOIDCEmail(email)
	if err != nil {
		return User{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO users (
    user_key, github_username, github_username_normalized, github_login,
    allowlisted, sandcastle_admin, created_at, updated_at
) VALUES (?, ?, ?, 0, 1, ?, datetime('now'), datetime('now'))
ON CONFLICT(user_key) DO UPDATE SET
    allowlisted = 1,
    sandcastle_admin = MAX(users.sandcastle_admin, excluded.sandcastle_admin),
    updated_at = datetime('now')
`, key, key, key, boolInt(admin)); err != nil {
		return User{}, fmt.Errorf("allowlist OIDC user %s: %w", key, err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO oidc_identities (user_key, issuer, email, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(user_key) DO UPDATE SET
    subject = CASE WHEN oidc_identities.issuer = excluded.issuer AND oidc_identities.email = excluded.email THEN oidc_identities.subject ELSE '' END,
    bound_at = CASE WHEN oidc_identities.issuer = excluded.issuer AND oidc_identities.email = excluded.email THEN oidc_identities.bound_at ELSE '' END,
    issuer = excluded.issuer,
    email = excluded.email
`, key, issuer, normalizedEmail, now); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, fmt.Errorf("email %s is already linked to another user", normalizedEmail)
		}
		return User{}, fmt.Errorf("link OIDC identity for %s: %w", key, err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return findUserByKey(ctx, db, key)
}

// BootstrapOIDCAdmins applies --bootstrap-oidc-admin; repeated runs converge.
func BootstrapOIDCAdmins(ctx context.Context, db *sql.DB, issuer string, admins []OIDCAdmin) error {
	for _, admin := range admins {
		if _, err := AllowlistOIDCUser(ctx, db, issuer, admin.UserKey, admin.Email, true); err != nil {
			return err
		}
	}
	return nil
}

// ResolveOIDCLogin maps a verified ID token's subject and email to an
// allowlisted user. A bound identity matches by subject only. An unbound one
// matches by email once, and that login binds the subject. emailVerified is
// false only when the token says email_verified=false; such an email never
// binds.
func ResolveOIDCLogin(ctx context.Context, db *sql.DB, issuer string, subject string, email string, emailVerified bool, now time.Time) (User, error) {
	if strings.TrimSpace(subject) == "" {
		return User{}, errOIDCLoginDenied
	}
	var userKey string
	err := db.QueryRowContext(ctx, `
SELECT user_key FROM oidc_identities WHERE issuer = ? AND subject = ?
`, issuer, subject).Scan(&userKey)
	if errors.Is(err, sql.ErrNoRows) {
		normalizedEmail, emailErr := normalizeOIDCEmail(email)
		if emailErr != nil || !emailVerified {
			return User{}, errOIDCLoginDenied
		}
		err = db.QueryRowContext(ctx, `
UPDATE oidc_identities
SET subject = ?, bound_at = ?
WHERE issuer = ? AND email = ? AND subject = ''
  AND user_key IN (SELECT user_key FROM users WHERE allowlisted = 1)
RETURNING user_key
`, subject, now.UTC().Format(time.RFC3339), issuer, normalizedEmail).Scan(&userKey)
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, errOIDCLoginDenied
		}
	}
	if err != nil {
		return User{}, err
	}
	user, err := findUserByKey(ctx, db, userKey)
	if err != nil {
		return User{}, err
	}
	if !user.Allowlisted {
		return User{}, errOIDCLoginDenied
	}
	return user, nil
}

func findUserByKey(ctx context.Context, db *sql.DB, userKey string) (User, error) {
	row := db.QueryRowContext(ctx, `
SELECT user_key, github_username, github_username_normalized, github_account_id, github_email, allowlisted, sandcastle_admin
FROM users
WHERE user_key = ?
`, userKey)
	return scanUser(row)
}

// allowlistEntry is one row of the admin allowlist page.
type allowlistEntry struct {
	User
	GitHubLogin bool
	OIDCEmail   string
	OIDCBound   bool
}

func listAllowlistEntries(ctx context.Context, db *sql.DB) ([]allowlistEntry, error) {
	rows, err := db.QueryContext(ctx, `
SELECT users.user_key, users.github_username, users.github_username_normalized, users.github_account_id,
       users.github_email, users.allowlisted, users.sandcastle_admin, users.github_login,
       COALESCE(oidc_identities.email, ''), COALESCE(oidc_identities.subject, '')
FROM users
LEFT JOIN oidc_identities ON oidc_identities.user_key = users.user_key
ORDER BY users.github_username_normalized
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []allowlistEntry
	for rows.Next() {
		var entry allowlistEntry
		var allowlisted, admin, githubLogin int
		var subject string
		if err := rows.Scan(&entry.UserKey, &entry.GitHubUsername, &entry.GitHubUsernameNormalized, &entry.GitHubAccountID,
			&entry.GitHubEmail, &allowlisted, &admin, &githubLogin, &entry.OIDCEmail, &subject); err != nil {
			return nil, err
		}
		entry.Allowlisted = allowlisted == 1
		entry.SandcastleAdmin = admin == 1
		entry.GitHubLogin = githubLogin == 1
		entry.OIDCBound = subject != ""
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
