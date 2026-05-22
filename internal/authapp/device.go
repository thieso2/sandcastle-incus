package authapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

const (
	DeviceStatusPending  = "pending"
	DeviceStatusApproved = "approved"
	DeviceStatusDenied   = "denied"
	DeviceStatusExpired  = "expired"

	deviceLoginTTL      = 10 * time.Minute
	devicePollInterval  = 2
	deviceUserCodeChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

type DeviceLogin struct {
	DeviceCode        string
	UserCode          string
	Status            string
	UserKey           string
	Message           string
	ProvisionedAt     string
	Token             string
	RemoteName        string
	AccessibleTenants []string
	Projects          []string
	VerificationURI   string
	ExpiresAt         time.Time
	Interval          int
}

type deviceStartResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Status          string `json:"status"`
	Message         string `json:"message"`
}

type devicePollResponse struct {
	Status            string   `json:"status"`
	Message           string   `json:"message"`
	UserKey           string   `json:"user_key,omitempty"`
	Token             string   `json:"incus_certificate_add_token,omitempty"`
	RemoteName        string   `json:"remote_name,omitempty"`
	AccessibleTenants []string `json:"accessible_tenants,omitempty"`
	Projects          []string `json:"projects,omitempty"`
	ExpiresIn         int      `json:"expires_in,omitempty"`
}

func (h handler) deviceStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	login, err := CreateDeviceLogin(r.Context(), h.db, h.authHostname, timeNow())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, deviceStartResponse{
		DeviceCode:      login.DeviceCode,
		UserCode:        login.UserCode,
		VerificationURI: login.VerificationURI,
		ExpiresIn:       int(time.Until(login.ExpiresAt).Seconds()),
		Interval:        login.Interval,
		Status:          login.Status,
		Message:         login.Message,
	})
}

func (h handler) devicePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		DeviceCode string `json:"device_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	login, err := PollDeviceLogin(r.Context(), h.db, request.DeviceCode, timeNow())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if login.Status == DeviceStatusApproved && login.ProvisionedAt == "" && h.provisioner != nil {
		login, err = h.provisionPersonalTenant(r.Context(), login)
		if err != nil {
			login.Status = DeviceStatusPending
		}
	}
	expiresIn := int(time.Until(login.ExpiresAt).Seconds())
	if expiresIn < 0 {
		expiresIn = 0
	}
	writeJSON(w, http.StatusOK, devicePollResponse{
		Status:            login.Status,
		Message:           login.Message,
		UserKey:           login.UserKey,
		Token:             login.Token,
		RemoteName:        login.RemoteName,
		AccessibleTenants: login.AccessibleTenants,
		Projects:          login.Projects,
		ExpiresIn:         expiresIn,
	})
}

func (h handler) provisionPersonalTenant(ctx context.Context, login DeviceLogin) (DeviceLogin, error) {
	user, err := FindUser(ctx, h.db, login.UserKey)
	if err != nil {
		return DeviceLogin{}, err
	}
	if _, err := h.db.ExecContext(ctx, "UPDATE device_logins SET message = ? WHERE device_code = ? AND provisioned_at = ''", "Provisioning Personal Tenant for "+user.UserKey+".", login.DeviceCode); err != nil {
		return DeviceLogin{}, err
	}
	result, err := h.provisioner.EnsurePersonalTenant(ctx, user)
	if err != nil {
		message := "Personal Tenant provisioning failed: " + err.Error()
		_, _ = h.db.ExecContext(ctx, "UPDATE device_logins SET message = ? WHERE device_code = ? AND provisioned_at = ''", message, login.DeviceCode)
		login.Message = message
		return login, err
	}
	message := result.normalizedMessage()
	_, err = h.db.ExecContext(ctx, "UPDATE device_logins SET message = ?, provisioned_at = ? WHERE device_code = ?", message, timeNow().UTC().Format(time.RFC3339), login.DeviceCode)
	if err != nil {
		return DeviceLogin{}, err
	}
	login, err = findDeviceLoginByDeviceCode(ctx, h.db, login.DeviceCode)
	if err != nil {
		return DeviceLogin{}, err
	}
	login.Token = result.Token
	login.RemoteName = result.RemoteName
	login.AccessibleTenants = append([]string{}, result.AccessibleTenants...)
	login.Projects = append([]string{}, result.Projects...)
	return login, nil
}

func (h handler) deviceApprove(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.deviceApproveForm(w, r)
	case http.MethodPost:
		h.deviceApprovePost(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h handler) deviceApproveForm(w http.ResponseWriter, r *http.Request) {
	if _, err := h.requireAllowlistedSession(r); err != nil {
		http.Redirect(w, r, "/login/github", http.StatusFound)
		return
	}
	code := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("user_code")))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = deviceTemplate.Execute(w, struct{ UserCode string }{UserCode: code})
}

func (h handler) deviceApprovePost(w http.ResponseWriter, r *http.Request) {
	user, err := h.requireAllowlistedSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	code := strings.ToUpper(strings.TrimSpace(r.FormValue("user_code")))
	action := strings.TrimSpace(r.FormValue("action"))
	switch action {
	case "approve":
		err = ApproveDeviceLogin(r.Context(), h.db, code, user.UserKey, timeNow())
	case "deny":
		err = DenyDeviceLogin(r.Context(), h.db, code, timeNow())
	default:
		err = fmt.Errorf("device login action is required")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	switch action {
	case "approve":
		_, _ = w.Write([]byte("Device login approved. Return to the terminal to continue provisioning.\n"))
	case "deny":
		_, _ = w.Write([]byte("Device login denied. Return to the terminal.\n"))
	}
}

func (h handler) requireAllowlistedSession(r *http.Request) (User, error) {
	cookie, err := r.Cookie(h.sessionCookie)
	if err != nil {
		return User{}, fmt.Errorf("web session is required")
	}
	user, err := UserForSession(r.Context(), h.db, cookie.Value, timeNow())
	if err != nil {
		return User{}, err
	}
	if !user.Allowlisted {
		return User{}, fmt.Errorf("allowlisted GitHub login is required")
	}
	return user, nil
}

func CreateDeviceLogin(ctx context.Context, db *sql.DB, authHostname string, now time.Time) (DeviceLogin, error) {
	deviceCode, err := randomToken(32)
	if err != nil {
		return DeviceLogin{}, err
	}
	userCode := newUserCode()
	expiresAt := now.Add(deviceLoginTTL).UTC()
	message := "Waiting for browser approval."
	_, err = db.ExecContext(ctx, `
INSERT INTO device_logins (device_code, user_code, status, message, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)
`, deviceCode, userCode, DeviceStatusPending, message, now.UTC().Format(time.RFC3339), expiresAt.Format(time.RFC3339))
	if err != nil {
		return DeviceLogin{}, fmt.Errorf("create device login: %w", err)
	}
	return DeviceLogin{
		DeviceCode:      deviceCode,
		UserCode:        userCode,
		Status:          DeviceStatusPending,
		Message:         message,
		VerificationURI: verificationURI(authHostname, userCode),
		ExpiresAt:       expiresAt,
		Interval:        devicePollInterval,
	}, nil
}

func PollDeviceLogin(ctx context.Context, db *sql.DB, deviceCode string, now time.Time) (DeviceLogin, error) {
	login, err := findDeviceLoginByDeviceCode(ctx, db, deviceCode)
	if err != nil {
		return DeviceLogin{}, err
	}
	if login.Status == DeviceStatusPending && !now.Before(login.ExpiresAt) {
		if _, err := db.ExecContext(ctx, "UPDATE device_logins SET status = ?, message = ? WHERE device_code = ?", DeviceStatusExpired, "Device login expired.", login.DeviceCode); err != nil {
			return DeviceLogin{}, err
		}
		login.Status = DeviceStatusExpired
		login.Message = "Device login expired."
	}
	return login, nil
}

func ApproveDeviceLogin(ctx context.Context, db *sql.DB, userCode string, userKey string, now time.Time) error {
	login, err := findDeviceLoginByUserCode(ctx, db, userCode)
	if err != nil {
		return err
	}
	if login.Status != DeviceStatusPending {
		return fmt.Errorf("device login is %s", login.Status)
	}
	if !now.Before(login.ExpiresAt) {
		_, _ = db.ExecContext(ctx, "UPDATE device_logins SET status = ?, message = ? WHERE user_code = ?", DeviceStatusExpired, "Device login expired.", login.UserCode)
		return fmt.Errorf("device login expired")
	}
	_, err = db.ExecContext(ctx, `
UPDATE device_logins
SET status = ?, user_key = ?, message = ?, approved_at = ?
WHERE user_code = ?
`, DeviceStatusApproved, userKey, "Approved. Provisioning will continue in the CLI.", now.UTC().Format(time.RFC3339), login.UserCode)
	return err
}

func DenyDeviceLogin(ctx context.Context, db *sql.DB, userCode string, now time.Time) error {
	login, err := findDeviceLoginByUserCode(ctx, db, userCode)
	if err != nil {
		return err
	}
	if login.Status != DeviceStatusPending {
		return fmt.Errorf("device login is %s", login.Status)
	}
	_, err = db.ExecContext(ctx, "UPDATE device_logins SET status = ?, message = ?, approved_at = ? WHERE user_code = ?", DeviceStatusDenied, "Device login denied.", now.UTC().Format(time.RFC3339), login.UserCode)
	return err
}

func findDeviceLoginByDeviceCode(ctx context.Context, db *sql.DB, deviceCode string) (DeviceLogin, error) {
	row := db.QueryRowContext(ctx, `
SELECT device_code, user_code, status, user_key, message, provisioned_at, expires_at
FROM device_logins
WHERE device_code = ?
`, strings.TrimSpace(deviceCode))
	return scanDeviceLogin(row)
}

func findDeviceLoginByUserCode(ctx context.Context, db *sql.DB, userCode string) (DeviceLogin, error) {
	row := db.QueryRowContext(ctx, `
SELECT device_code, user_code, status, user_key, message, provisioned_at, expires_at
FROM device_logins
WHERE user_code = ?
`, strings.ToUpper(strings.TrimSpace(userCode)))
	return scanDeviceLogin(row)
}

func scanDeviceLogin(row *sql.Row) (DeviceLogin, error) {
	var login DeviceLogin
	var expiresAt string
	if err := row.Scan(&login.DeviceCode, &login.UserCode, &login.Status, &login.UserKey, &login.Message, &login.ProvisionedAt, &expiresAt); err != nil {
		if err == sql.ErrNoRows {
			return DeviceLogin{}, fmt.Errorf("device login not found")
		}
		return DeviceLogin{}, err
	}
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return DeviceLogin{}, err
	}
	login.ExpiresAt = parsed
	login.Interval = devicePollInterval
	return login, nil
}

func verificationURI(authHostname string, userCode string) string {
	host := strings.Trim(strings.TrimSpace(authHostname), ".")
	if host == "" {
		return "/device?user_code=" + userCode
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return strings.TrimRight(host, "/") + "/device?user_code=" + userCode
	}
	return "https://" + host + "/device?user_code=" + userCode
}

func newUserCode() string {
	var builder strings.Builder
	for i := 0; i < 8; i++ {
		if i == 4 {
			builder.WriteByte('-')
		}
		builder.WriteByte(deviceUserCodeChars[rand.Intn(len(deviceUserCodeChars))])
	}
	return builder.String()
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

var deviceTemplate = template.Must(template.New("device").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Sandcastle Device Login</title>
</head>
<body>
  <main>
    <h1>Device Login</h1>
    <form method="post" action="/device">
      <label>User Code <input name="user_code" value="{{.UserCode}}"></label>
      <button name="action" value="approve" type="submit">Approve</button>
      <button name="action" value="deny" type="submit">Deny</button>
    </form>
  </main>
</body>
</html>
`))
