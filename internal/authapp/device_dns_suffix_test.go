package authapp

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestEffectiveDNSSuffixPrecedence(t *testing.T) {
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
			if got := effectiveDNSSuffix(tc.cli, tc.browser); got != tc.want {
				t.Fatalf("effectiveDNSSuffix(%q,%q) = %q, want %q", tc.cli, tc.browser, got, tc.want)
			}
		})
	}
}

// The browser approval form's DNS suffix is persisted at approval and surfaces
// on the DeviceLogin the poll loads, so first-login provisioning can use it.
func TestApproveDeviceLoginPersistsBrowserSuffix(t *testing.T) {
	db := authDBForTest(t)
	ctx := context.Background()
	login, err := CreateDeviceLogin(ctx, db, "auth.example.com", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := ApproveDeviceLogin(ctx, db, login.UserCode, "octocat", "  julius  ", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := findDeviceLoginByDeviceCode(ctx, db, login.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != DeviceStatusApproved {
		t.Fatalf("status = %q, want approved", got.Status)
	}
	if got.RequestedDNSSuffix != "julius" {
		t.Fatalf("RequestedDNSSuffix = %q, want %q (trimmed)", got.RequestedDNSSuffix, "julius")
	}
}

func TestApproveDeviceLoginWithoutSuffixLeavesItEmpty(t *testing.T) {
	db := authDBForTest(t)
	ctx := context.Background()
	login, err := CreateDeviceLogin(ctx, db, "auth.example.com", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := ApproveDeviceLogin(ctx, db, login.UserCode, "octocat", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := findDeviceLoginByDeviceCode(ctx, db, login.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if got.RequestedDNSSuffix != "" {
		t.Fatalf("RequestedDNSSuffix = %q, want empty", got.RequestedDNSSuffix)
	}
}

func TestDeviceTemplateRendersDNSSuffixField(t *testing.T) {
	var buf bytes.Buffer
	if err := deviceTemplate.Execute(&buf, struct {
		UserCode  string
		DNSSuffix string
	}{UserCode: "ABCD-1234", DNSSuffix: "julius"}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{`name="dns_suffix"`, `value="julius"`, `name="user_code"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("device form missing %q:\n%s", want, html)
		}
	}
}
