package authapp

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestEffectiveInitialProjectPrecedence(t *testing.T) {
	cases := []struct {
		name, cli, browser, want string
	}{
		{"cli wins", "flag", "form", "flag"},
		{"cli trims to empty falls back", "  ", "form", "form"},
		{"browser used when cli empty", "", "form", "form"},
		{"browser trimmed", "", "  form  ", "form"},
		{"both empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveInitialProject(tc.cli, tc.browser); got != tc.want {
				t.Fatalf("effectiveInitialProject(%q,%q) = %q, want %q", tc.cli, tc.browser, got, tc.want)
			}
		})
	}
}

// The browser approval form's initial-project name is persisted at approval and
// surfaces on the DeviceLogin the poll loads, so first-login provisioning can
// use it (issue #93).
func TestApproveDeviceLoginPersistsBrowserInitialProject(t *testing.T) {
	db := authDBForTest(t)
	ctx := context.Background()
	login, err := CreateDeviceLogin(ctx, db, "auth.example.com", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := ApproveDeviceLogin(ctx, db, login.UserCode, "octocat", "", "  web  ", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := findDeviceLoginByDeviceCode(ctx, db, login.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != DeviceStatusApproved {
		t.Fatalf("status = %q, want approved", got.Status)
	}
	if got.RequestedInitialProject != "web" {
		t.Fatalf("RequestedInitialProject = %q, want %q (trimmed)", got.RequestedInitialProject, "web")
	}
}

func TestApproveDeviceLoginWithoutInitialProjectLeavesItEmpty(t *testing.T) {
	db := authDBForTest(t)
	ctx := context.Background()
	login, err := CreateDeviceLogin(ctx, db, "auth.example.com", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := ApproveDeviceLogin(ctx, db, login.UserCode, "octocat", "", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := findDeviceLoginByDeviceCode(ctx, db, login.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if got.RequestedInitialProject != "" {
		t.Fatalf("RequestedInitialProject = %q, want empty", got.RequestedInitialProject)
	}
}

func TestDeviceTemplateRendersInitialProjectField(t *testing.T) {
	var buf bytes.Buffer
	if err := deviceTemplate.Execute(&buf, struct {
		UserCode       string
		DNSSuffix      string
		InitialProject string
	}{UserCode: "ABCD-1234", InitialProject: "web"}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{`name="initial_project"`, `value="web"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("device form missing %q:\n%s", want, html)
		}
	}
}
