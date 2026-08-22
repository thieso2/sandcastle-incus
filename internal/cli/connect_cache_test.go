package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/authapp"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/hostkeys"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestCacheableConnectReference(t *testing.T) {
	config := commandConfig{adminConfig: scconfig.Admin{Project: "pinned"}}
	cases := []struct {
		ref     string
		project string
		machine string
		ok      bool
	}{
		{"dev", "pinned", "dev", true},
		{"wordpress:dev", "wordpress", "dev", true},
		{"web*", "", "", false},                 // glob → live selector
		{"dev.wordpress.obelix", "", "", false}, // dotted cross-install form
		{"suffix:wordpress:dev", "", "", false}, // multi-colon → live
		{"", "", "", false},
		{":dev", "", "", false},
	}
	for _, c := range cases {
		project, machine, ok := cacheableConnectReference(config, c.ref)
		if ok != c.ok || project != c.project || machine != c.machine {
			t.Fatalf("ref %q → (%q, %q, %v), want (%q, %q, %v)", c.ref, project, machine, ok, c.project, c.machine, c.ok)
		}
	}
	// A bare machine name with NO pinned project cannot resolve from cache.
	if _, _, ok := cacheableConnectReference(commandConfig{}, "dev"); ok {
		t.Fatal("bare reference with no configured project must fall back")
	}
}

func TestRecordedKeyMatchesScan(t *testing.T) {
	ed := hostkeys.Key{Type: "ssh-ed25519", Key: "AAAA_ed"}
	rsa := hostkeys.Key{Type: "ssh-rsa", Key: "AAAA_rsa"}
	changed := hostkeys.Key{Type: "ssh-ed25519", Key: "AAAA_other"}
	if !recordedKeyMatchesScan([]hostkeys.Key{ed, rsa}, []hostkeys.Key{ed}) {
		t.Fatal("agreeing key must match")
	}
	if recordedKeyMatchesScan([]hostkeys.Key{ed}, []hostkeys.Key{changed}) {
		t.Fatal("a changed key of a recorded type must NOT match (rebuilt machine)")
	}
	if recordedKeyMatchesScan([]hostkeys.Key{ed}, []hostkeys.Key{rsa}) {
		t.Fatal("no overlapping type gives no certainty — must not match")
	}
	if recordedKeyMatchesScan(nil, []hostkeys.Key{ed}) {
		t.Fatal("nothing recorded must not match")
	}
}

func TestCachedConnectLoginUser(t *testing.T) {
	if user := cachedConnectLoginUser(tenant.Summary{UnixUser: "thies"}, nil); user != "thies" {
		t.Fatalf("summary user: %q", user)
	}
	profiles := cachedTestProfiles("default", "#cloud-config\nusers:\n  - name: alice\n    uid: 2000\n")
	if user := cachedConnectLoginUser(tenant.Summary{}, profiles); user != "alice" {
		t.Fatalf("profile user: %q", user)
	}
	if user := cachedConnectLoginUser(tenant.Summary{}, nil); user != tenant.DefaultV2UnixUser {
		t.Fatalf("fallback user: %q", user)
	}
}

// The full cache-first dial: cached running machine + agreeing pinned host key
// yields a strict-checking ssh argv with no live Incus involvement at all.
func TestDialV2MachineViaCacheHit(t *testing.T) {
	home := useLoginHomeForTest(t)
	key := hostkeys.Key{Type: "ssh-ed25519", Key: "AAAAC3TestKey"}
	writeKnownHostsForTest(t, home, "dev.wordpress.obelix", key)
	stubConnectCacheKeyscan(t, []hostkeys.Key{key}, nil)

	fake := &fakeResourceClient{result: authapp.ResourceListResult{
		Tenant: tenant.Summary{Tenant: "thieso2", DNSSuffix: "obelix", UnixUser: "thies", PrivateCIDR: "10.123.0.0/24"},
		Machines: []meta.Machine{
			{Name: "dev", Project: "wordpress", Running: true, PrivateIP: "10.123.0.176"},
		},
	}}
	config := commandConfig{adminConfig: scconfig.Admin{Tenant: "thieso2"}, authResources: fake}

	dialed, ok := dialV2MachineViaCache(context.Background(), config, "wordpress:dev")
	if !ok {
		t.Fatal("expected a cache hit")
	}
	args := strings.Join(dialed.sshArgs, " ")
	for _, want := range []string{
		"HostKeyAlias=dev.wordpress.obelix",
		"StrictHostKeyChecking=yes",
		"thies@10.123.0.176",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("sshArgs %q missing %q", args, want)
		}
	}
	if fake.lastRequest.Project != "wordpress" || fake.lastRequest.Machine != "dev" {
		t.Fatalf("cache request = %+v", fake.lastRequest)
	}
}

// Every uncertainty falls back: stopped/bare/missing machine, key mismatch,
// keyscan failure, cache error.
func TestDialV2MachineViaCacheFallsBack(t *testing.T) {
	home := useLoginHomeForTest(t)
	key := hostkeys.Key{Type: "ssh-ed25519", Key: "AAAAC3TestKey"}
	writeKnownHostsForTest(t, home, "dev.wordpress.obelix", key)
	summary := tenant.Summary{Tenant: "thieso2", DNSSuffix: "obelix", UnixUser: "thies"}
	running := meta.Machine{Name: "dev", Project: "wordpress", Running: true, PrivateIP: "10.123.0.176"}

	cases := []struct {
		name    string
		result  authapp.ResourceListResult
		err     error
		scan    []hostkeys.Key
		scanErr error
	}{
		{name: "cache error", err: errors.New("boom"), scan: []hostkeys.Key{key}},
		{name: "machine missing", result: authapp.ResourceListResult{Tenant: summary}, scan: []hostkeys.Key{key}},
		{name: "machine stopped", result: authapp.ResourceListResult{Tenant: summary,
			Machines: []meta.Machine{{Name: "dev", Project: "wordpress", Running: false}}}, scan: []hostkeys.Key{key}},
		{name: "machine bare", result: authapp.ResourceListResult{Tenant: summary,
			Machines: []meta.Machine{{Name: "dev", Project: "wordpress", Running: true, PrivateIP: "10.123.0.176", Bare: true}}}, scan: []hostkeys.Key{key}},
		{name: "no suffix", result: authapp.ResourceListResult{Tenant: tenant.Summary{Tenant: "thieso2"},
			Machines: []meta.Machine{running}}, scan: []hostkeys.Key{key}},
		{name: "keyscan fails", result: authapp.ResourceListResult{Tenant: summary,
			Machines: []meta.Machine{running}}, scanErr: errors.New("connection refused")},
		{name: "key mismatch (rebuilt)", result: authapp.ResourceListResult{Tenant: summary,
			Machines: []meta.Machine{running}}, scan: []hostkeys.Key{{Type: "ssh-ed25519", Key: "AAAAC3Rebuilt"}}},
	}
	for _, c := range cases {
		stubConnectCacheKeyscan(t, c.scan, c.scanErr)
		fake := &fakeResourceClient{result: c.result, err: c.err}
		config := commandConfig{adminConfig: scconfig.Admin{Tenant: "thieso2"}, authResources: fake}
		if _, ok := dialV2MachineViaCache(context.Background(), config, "wordpress:dev"); ok {
			t.Fatalf("%s: expected fallback to the live path", c.name)
		}
	}
}

func TestConnectCacheEnvDisables(t *testing.T) {
	t.Setenv(connectCacheEnv, "0")
	fake := &fakeResourceClient{}
	config := commandConfig{adminConfig: scconfig.Admin{Tenant: "thieso2"}, authResources: fake}
	if _, ok := dialV2MachineViaCache(context.Background(), config, "wordpress:dev"); ok {
		t.Fatal("disabled cache must fall back")
	}
	if fake.calls != 0 {
		t.Fatalf("disabled cache must not call the auth-app (calls=%d)", fake.calls)
	}
}

func cachedTestProfiles(name string, userData string) []api.Profile {
	return []api.Profile{{
		Name:       name,
		ProfilePut: api.ProfilePut{Config: map[string]string{"cloud-init.user-data": userData}},
	}}
}

func writeKnownHostsForTest(t *testing.T, home string, host string, key hostkeys.Key) {
	t.Helper()
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := host + " " + key.Type + " " + key.Key + "\n"
	if err := os.WriteFile(filepath.Join(dir, "known_hosts"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
}

func stubConnectCacheKeyscan(t *testing.T, keys []hostkeys.Key, err error) {
	t.Helper()
	original := connectCacheKeyscan
	connectCacheKeyscan = func(ctx context.Context, host string) ([]hostkeys.Key, error) {
		return keys, err
	}
	t.Cleanup(func() { connectCacheKeyscan = original })
}
