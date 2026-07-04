package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/authapp"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
)

func idempotentLoginAdminConfig() scconfig.Admin {
	return scconfig.Admin{
		Remote:       "sandcastle-octocat",
		Tenant:       "octocat",
		AuthHostname: "https://auth.example.com",
		AuthToken:    "cli-token",
	}
}

func TestLoginReusesExistingSession(t *testing.T) {
	useLoginHomeForTest(t)
	tenants := &fakeAuthTenantClient{tenants: []authapp.TenantAccessSummary{{Tenant: "octocat", Personal: true}}}
	device := &fakeAuthDeviceClient{}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:             "sandcastle",
		adminConfig:      idempotentLoginAdminConfig(),
		authTenants:      tenants,
		authDevice:       device,
		loginRemoteProbe: func(context.Context, string) error { return nil },
	}, "login", "https://auth.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`Already logged in at https://auth.example.com (tenant "octocat"); remote "sandcastle-octocat" responds.`,
		"Re-run with --force to re-authenticate.",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if tenants.listRequests != 1 {
		t.Fatalf("ListTenants calls = %d", tenants.listRequests)
	}
	if device.startCalls != 0 {
		t.Fatalf("device flow started %d times, want 0", device.startCalls)
	}
	if strings.Contains(stdout, "Open: ") {
		t.Fatalf("device flow output present:\n%s", stdout)
	}
}

func TestLoginForceBypassesExistingSession(t *testing.T) {
	useLoginHomeForTest(t)
	installer := &fakeLoginRemoteInstaller{}
	device := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{DeviceCode: "device", UserCode: "ABCD-1234", VerificationURI: "https://auth.example.com/device", Interval: 1},
		polls: []authapp.DevicePollResult{{
			Status:            authapp.DeviceStatusApproved,
			UserKey:           "octocat",
			Token:             "token",
			RemoteName:        "sandcastle-octocat",
			AccessibleTenants: []string{"octocat"},
		}},
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: idempotentLoginAdminConfig(),
		authTenants: &fakeAuthTenantClient{tenants: []authapp.TenantAccessSummary{{Tenant: "octocat"}}},
		authDevice:  device,
		loginRemote: installer,
		loginRemoteProbe: func(context.Context, string) error {
			t.Fatal("probe must not run with --force")
			return nil
		},
	}, "login", "https://auth.example.com", "--force")
	if err != nil {
		t.Fatal(err)
	}
	if device.startCalls != 1 {
		t.Fatalf("device flow started %d times, want 1", device.startCalls)
	}
	if !strings.Contains(stdout, "Approved as octocat.") {
		t.Fatalf("stdout missing approval:\n%s", stdout)
	}
}

func TestLoginFallsBackWhenSavedTokenRejected(t *testing.T) {
	useLoginHomeForTest(t)
	installer := &fakeLoginRemoteInstaller{}
	device := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{DeviceCode: "device", UserCode: "ABCD-1234", VerificationURI: "https://auth.example.com/device", Interval: 1},
		polls: []authapp.DevicePollResult{{
			Status:            authapp.DeviceStatusApproved,
			UserKey:           "octocat",
			Token:             "token",
			RemoteName:        "sandcastle-octocat",
			AccessibleTenants: []string{"octocat"},
		}},
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: idempotentLoginAdminConfig(),
		authTenants: &fakeAuthTenantClient{err: errors.New("403 token expired")},
		authDevice:  device,
		loginRemote: installer,
	}, "login", "https://auth.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if device.startCalls != 1 {
		t.Fatalf("device flow started %d times, want 1", device.startCalls)
	}
	if strings.Contains(stdout, "Already logged in") {
		t.Fatalf("unexpected shortcut output:\n%s", stdout)
	}
}

func TestLoginRefusesWhenNotTailnetNode(t *testing.T) {
	useLoginHomeForTest(t)
	device := &fakeAuthDeviceClient{}
	_, err := executeForTestWithConfig(t, commandConfig{
		name:       "sandcastle",
		authDevice: device,
		loginTailnetPrecheck: func(context.Context) error {
			return errors.New("this machine is not a tailnet node")
		},
	}, "login", "https://auth.example.com")
	if err == nil || !strings.Contains(err.Error(), "not a tailnet node") {
		t.Fatalf("err = %v", err)
	}
	if device.startCalls != 0 {
		t.Fatalf("device flow started %d times, want 0 (refused before browser)", device.startCalls)
	}
}

func TestLoginSkipSetupBypassesTailnetPrecheck(t *testing.T) {
	useLoginHomeForTest(t)
	installer := &fakeLoginRemoteInstaller{}
	device := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{DeviceCode: "device", UserCode: "ABCD-1234", VerificationURI: "https://auth.example.com/device", Interval: 1},
		polls: []authapp.DevicePollResult{{
			Status:            authapp.DeviceStatusApproved,
			UserKey:           "octocat",
			Token:             "token",
			RemoteName:        "sandcastle-octocat",
			AccessibleTenants: []string{"octocat"},
		}},
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		authDevice:  device,
		loginRemote: installer,
		loginTailnetPrecheck: func(context.Context) error {
			t.Fatal("precheck must not run with --skip-setup")
			return nil
		},
	}, "login", "https://auth.example.com", "--skip-setup")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Approved as octocat.") {
		t.Fatalf("stdout missing approval:\n%s", stdout)
	}
}

func TestLoginDifferentHostSkipsShortcut(t *testing.T) {
	useLoginHomeForTest(t)
	tenants := &fakeAuthTenantClient{tenants: []authapp.TenantAccessSummary{{Tenant: "octocat"}}}
	installer := &fakeLoginRemoteInstaller{}
	device := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{DeviceCode: "device", UserCode: "ABCD-1234", VerificationURI: "https://other.example.com/device", Interval: 1},
		polls: []authapp.DevicePollResult{{
			Status:            authapp.DeviceStatusApproved,
			UserKey:           "octocat",
			Token:             "token",
			RemoteName:        "sandcastle-octocat",
			AccessibleTenants: []string{"octocat"},
		}},
	}
	_, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: idempotentLoginAdminConfig(),
		authTenants: tenants,
		authDevice:  device,
		loginRemote: installer,
	}, "login", "https://other.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if tenants.listRequests != 0 {
		t.Fatalf("ListTenants calls = %d, want 0 (different host)", tenants.listRequests)
	}
	if device.startCalls != 1 {
		t.Fatalf("device flow started %d times, want 1", device.startCalls)
	}
}
