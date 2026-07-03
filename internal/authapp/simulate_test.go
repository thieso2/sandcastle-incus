package authapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSimulateLoginDisabledWithoutToken(t *testing.T) {
	db := authDBForTest(t)
	handler := NewHandler(db, HandlerOptions{AuthHostname: "auth.example.com"})

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/oauth/github/simulate?token=x&username=octocat", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("simulate without token configured = %d, want 404", res.Code)
	}
}

func TestSimulateLoginRejectsWrongToken(t *testing.T) {
	db := authDBForTest(t)
	handler := NewHandler(db, HandlerOptions{AuthHostname: "auth.example.com", SimulateGitHubToken: "s3cret"})

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/oauth/github/simulate?token=nope&username=octocat", nil))
	if res.Code != http.StatusForbidden {
		t.Fatalf("simulate with wrong token = %d, want 403", res.Code)
	}
}

func TestSimulateLoginMintsSessionAndAllowlists(t *testing.T) {
	db := authDBForTest(t)
	handler := NewHandler(db, HandlerOptions{AuthHostname: "auth.example.com", SimulateGitHubToken: "s3cret"})

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/oauth/github/simulate?token=s3cret&username=OctoCat", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("simulate = %d %q", res.Code, res.Body.String())
	}
	cookies := res.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "sandcastle_session" {
		t.Fatalf("cookies = %#v", cookies)
	}

	// The simulated user is auto-allowlisted, so the session reaches the
	// onboarding page (allowlisted-only) at "/".
	onboarding := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookies[0])
	handler.ServeHTTP(onboarding, req)
	if onboarding.Code != http.StatusOK {
		t.Fatalf("onboarding = %d %q", onboarding.Code, onboarding.Body.String())
	}

	user, err := FindLoginUser(context.Background(), db, "octocat")
	if err != nil {
		t.Fatalf("FindLoginUser: %v", err)
	}
	if !user.Allowlisted {
		t.Fatalf("simulated user should be allowlisted: %#v", user)
	}
}

func TestSimulateLoginApprovesDeviceLogin(t *testing.T) {
	db := authDBForTest(t)
	handler := NewHandler(db, HandlerOptions{AuthHostname: "auth.example.com", SimulateGitHubToken: "s3cret"})

	login, err := CreateDeviceLogin(context.Background(), db, "auth.example.com", timeNow())
	if err != nil {
		t.Fatalf("CreateDeviceLogin: %v", err)
	}

	form := url.Values{}
	form.Set("token", "s3cret")
	form.Set("username", "octocat")
	form.Set("user_code", login.UserCode)
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/oauth/github/simulate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("simulate device approve = %d %q", res.Code, res.Body.String())
	}

	polled, err := PollDeviceLogin(context.Background(), db, login.DeviceCode, timeNow())
	if err != nil {
		t.Fatalf("PollDeviceLogin: %v", err)
	}
	if polled.Status != "approved" {
		t.Fatalf("device login status = %q, want approved", polled.Status)
	}
}
