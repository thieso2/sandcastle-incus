package cli

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

// shortProjectName must not assume the default install prefix. `sc-adm install
// --prefix id` yields `id-<tenant>-<project>`, and hardcoding `sc2-` filtered
// every project out — enroll then added no project remotes and still exited 0.
func TestShortProjectNameIsInstallPrefixAgnostic(t *testing.T) {
	cases := []struct {
		project string
		tenant  string
		want    string
	}{
		{"sc2-e2edns-default", "e2edns", "default"},
		{"id-e2edns-default", "e2edns", "default"},
		{"id-e2edns-backend", "e2edns", "backend"},
		{"sc-acme-web", "acme", "web"},
		{"anything-acme-a-b", "acme", "a-b"},
		// The infra project has no project segment and gets no remote.
		{"sc2-e2edns", "e2edns", ""},
		{"id-e2edns", "e2edns", ""},
		// A different tenant's project must never match.
		{"sc2-other-default", "e2edns", ""},
	}
	for _, tc := range cases {
		if got := shortProjectName(tc.project, tc.tenant); got != tc.want {
			t.Errorf("shortProjectName(%q, %q) = %q, want %q", tc.project, tc.tenant, got, tc.want)
		}
	}
}

// The per-project endpoint is read off the base remote created by the token.
// It used to default to a hardcoded developer host.
func TestRemoteAddressReadsBaseRemote(t *testing.T) {
	dir := t.TempDir()
	config := "remotes:\n" +
		"  sc-e2edns:\n" +
		"    addr: https://10.200.0.10:8443\n" +
		"    protocol: incus\n" +
		"  sc-fallback:\n" +
		"    addr: https://10.0.0.1:8443,https://10.0.0.2:8443\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	addr, err := remoteAddress(filepath.Join(dir, "config.yml"), "sc-e2edns")
	if err != nil {
		t.Fatal(err)
	}
	if addr != "https://10.200.0.10:8443" {
		t.Fatalf("remoteAddress = %q", addr)
	}
	if _, err := remoteAddress(filepath.Join(dir, "config.yml"), "missing"); err == nil {
		t.Fatal("expected an error for an unknown remote")
	}
}

// The enroll flag must carry no host default; a hardcoded one silently pointed
// every project remote at the wrong Incus daemon.
func TestEnrollIncusEndpointHasNoHardcodedDefault(t *testing.T) {
	root := NewRootCommand(commandConfig{name: "sc"})
	cmd, _, err := root.Find([]string{"enroll"})
	if err != nil {
		t.Fatal(err)
	}
	flag := cmd.Flags().Lookup("incus-endpoint")
	if flag == nil {
		t.Fatal("enroll has no --incus-endpoint flag")
	}
	if flag.DefValue != "" {
		t.Fatalf("--incus-endpoint default = %q, want empty (derive it from the base remote)", flag.DefValue)
	}
}

// The Incus certificate add token is base64 JSON carrying the daemon's
// addresses. `sc enroll` needs them when the daemon already trusts this client's
// keypair and refuses to redeem the token (the multi-install shared-identity
// case), and as the last fallback for the per-project endpoint.
func TestIncusTokenAddresses(t *testing.T) {
	token := base64.StdEncoding.EncodeToString([]byte(
		`{"client_name":"sandcastle-e2edns","fingerprint":"ab","addresses":["10.200.0.10:8443","[fd42::1]:8443"],"secret":"s"}`))
	got := incusTokenAddresses(token)
	if len(got) != 2 || got[0] != "10.200.0.10:8443" || got[1] != "[fd42::1]:8443" {
		t.Fatalf("incusTokenAddresses = %#v", got)
	}
	for _, bad := range []string{"", "not-base64!!", base64.StdEncoding.EncodeToString([]byte("not json"))} {
		if got := incusTokenAddresses(bad); got != nil {
			t.Fatalf("incusTokenAddresses(%q) = %#v, want nil", bad, got)
		}
	}
}

// Tenant machine host keys are ephemeral and their private IPs recycle, so
// `sc c` must not write them into (or be blocked by) the user's own
// ~/.ssh/known_hosts. Before this, the second `sc c` after a delete+recreate
// died with "Host key verification failed".
func TestSandcastleKnownHostsIsSeparateFromUserSSH(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := sandcastleKnownHostsPath()
	if path == "" {
		t.Fatal("empty known_hosts path")
	}
	if filepath.Base(path) != "known_hosts" {
		t.Fatalf("path = %q", path)
	}
	if strings.Contains(path, filepath.Join(home, ".ssh")) {
		t.Fatalf("path %q is inside the user's ~/.ssh", path)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("parent dir not created: %v", err)
	}
}

// forgetKnownHost must drop only the recycled IP's entry, and tolerate a
// missing file entirely.
func TestForgetKnownHostRemovesOnlyThatHost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	content := "10.61.0.66 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n" +
		"10.61.0.84 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	forgetKnownHost(context.Background(), path, "10.61.0.66")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "10.61.0.66") {
		t.Fatalf("stale host key survived:\n%s", data)
	}
	if !strings.Contains(string(data), "10.61.0.84") {
		t.Fatalf("unrelated host key was removed:\n%s", data)
	}
	// A missing file must not panic or create one.
	missing := filepath.Join(dir, "nope")
	forgetKnownHost(context.Background(), missing, "10.61.0.66")
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("forgetKnownHost created a file it should have skipped")
	}
}

// When the daemon already trusts the client keypair, enrollment must fall back to
// the token's advertised addresses — not just the sidecar tailnet address, which
// is unknown until the sidecar joins. `sc login` used to die there.
func TestTrustedClientRemoteURLsPrefersTailnetThenToken(t *testing.T) {
	token := base64.StdEncoding.EncodeToString([]byte(
		`{"addresses":["10.200.0.10:8443","10.61.0.1:8443"]}`))

	got := trustedClientRemoteURLs("100.87.50.11", token)
	want := []string{"https://100.87.50.11:8443", "https://10.200.0.10:8443", "https://10.61.0.1:8443"}
	if len(got) != len(want) {
		t.Fatalf("urls = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("urls = %#v, want %#v", got, want)
		}
	}
	// No tailnet address yet: the token still gets us there.
	if got := trustedClientRemoteURLs("", token); len(got) != 2 || got[0] != "https://10.200.0.10:8443" {
		t.Fatalf("urls without tailnet address = %#v", got)
	}
	// Nothing at all is an honest empty, so the caller can report it.
	if got := trustedClientRemoteURLs("", "garbage"); len(got) != 0 {
		t.Fatalf("urls = %#v, want none", got)
	}
}

// Several installs share one Incus daemon, so a same-named tenant exists once per
// install. `sc-adm tenant status <t>` was unscoped and reported whichever install
// sorted first — on majestix, `SANDCASTLE_INCUS_PROJECT_PREFIX=sc2 sc-adm tenant
// status e2edns` printed install B's `id-e2edns-default`.
func TestAdminTenantStatusIsScopedToTheInstallPrefix(t *testing.T) {
	// The OTHER install first: an unscoped lookup takes the first match, so this
	// ordering is what makes the test fail when the scoping is removed.
	projects := append(
		v2TenantProjectsWithPrefix("id", "e2edns", "10.62.0.0/24", "default"),
		v2TenantProjects("e2edns", "10.61.0.0/24", "default")...,
	)
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.Admin{IncusProjectPrefix: "sc2"},
		tenantStore: tenant.MemoryStore{Projects: projects},
	}, "tenant", "status", "e2edns")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "id-e2edns") {
		t.Fatalf("admin status leaked the other install's tenant:\n%s", stdout)
	}
	if !strings.Contains(stdout, "sc2-e2edns-default") {
		t.Fatalf("admin status did not resolve this install's tenant:\n%s", stdout)
	}
}
