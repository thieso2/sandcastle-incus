package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/authapp"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

// fakeResourceClient is a minimal authResourceClient stand-in: config.authResources
// lets tests inject it directly, the same seam tenantClient()/authTenants uses for
// the sibling /api/tenants client.
type fakeResourceClient struct {
	result      authapp.ResourceListResult
	err         error
	calls       int
	lastRequest authapp.ResourceListRequest
}

func (f *fakeResourceClient) ListResources(ctx context.Context, request authapp.ResourceListRequest) (authapp.ResourceListResult, error) {
	f.calls++
	f.lastRequest = request
	if f.err != nil {
		return authapp.ResourceListResult{}, f.err
	}
	return f.result, nil
}

var _ authResourceClient = (*fakeResourceClient)(nil)

// The resource-cache-not-ready message a real GET /api/resources answers with
// 503 whether the config toggle is off or the cache simply hasn't finished its
// initial read yet — see resourceCacheUnavailableMessage in
// internal/authapp/resource_cache_api.go. sc ls never distinguishes the two; it
// falls back on any non-200 the same way.
const fakeResourceCacheUnavailableErr = "auth app resource list: resource cache is not available: toggle is off, or the cache has not finished its initial read / lost its event-bus connection"

func TestListMachinesViaCache_ReadyReturnsCacheDataIncludingNewResourceTypes(t *testing.T) {
	fake := &fakeResourceClient{result: authapp.ResourceListResult{
		Tenant:      tenant.Summary{Tenant: "acme"},
		AllProjects: true,
		Machines:    []meta.Machine{{Project: "gbrain", Name: "web", Running: true}},
		Networks:    []api.Network{{Name: "sc2br0", Type: "bridge", Managed: true, Project: "gbrain"}},
		StoragePools: []api.StoragePool{
			{Name: "default", Driver: "zfs"},
		},
		StorageVolumes: []api.StorageVolumeFull{
			{StorageVolume: api.StorageVolume{Name: "web-root", Type: "custom", Project: "gbrain"}},
		},
		Profiles: []api.Profile{{Name: "default", Project: "gbrain"}},
		Images:   []api.Image{{Fingerprint: "0123456789abcdef", Project: "gbrain", Architecture: "x86_64", Type: "container"}},
	}}
	config := commandConfig{
		adminConfig:   scconfig.Admin{Tenant: "acme"},
		authResources: fake,
	}
	result, ok := listMachinesViaCache(context.Background(), config, listMachinesRequest{AllProjects: true})
	if !ok {
		t.Fatalf("cache-ready path fell back unexpectedly")
	}
	if fake.calls != 1 {
		t.Fatalf("ListResources called %d times, want 1", fake.calls)
	}
	if len(result.Machines) != 1 || len(result.Networks) != 1 || len(result.StoragePools) != 1 ||
		len(result.StorageVolumes) != 1 || len(result.Profiles) != 1 || len(result.Images) != 1 {
		t.Fatalf("result = %+v, want every cache-backed resource type carried through", result)
	}

	rendered := formatMachineList(result, listRenderOptions{
		ShowNetworks:       true,
		ShowStoragePools:   true,
		ShowStorageVolumes: true,
		ShowProfiles:       true,
		ShowImages:         true,
	})
	for _, want := range []string{
		"NETWORKS", "sc2br0",
		"STORAGE POOLS", "default", "zfs",
		"STORAGE VOLUMES", "web-root",
		"PROFILES",
		"IMAGES", "0123456789ab", // 12-char truncated fingerprint
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered output missing %q:\n%s", want, rendered)
		}
	}
}

func TestListMachinesViaCache_ScopesRequestTenantAndProject(t *testing.T) {
	fake := &fakeResourceClient{result: authapp.ResourceListResult{Tenant: tenant.Summary{Tenant: "acme"}}}
	config := commandConfig{adminConfig: scconfig.Admin{Tenant: "acme", Project: "pinned"}, authResources: fake}

	if _, ok := listMachinesViaCache(context.Background(), config, listMachinesRequest{}); !ok {
		t.Fatal("expected cache path to answer")
	}
	if fake.lastRequest.Tenant != "acme" || fake.lastRequest.Project != "pinned" {
		t.Fatalf("request = %+v, want tenant=acme project=pinned (locally pinned project)", fake.lastRequest)
	}

	if _, ok := listMachinesViaCache(context.Background(), config, listMachinesRequest{AllProjects: true}); !ok {
		t.Fatal("expected cache path to answer")
	}
	if fake.lastRequest.Project != "" {
		t.Fatalf("--all-projects request carried project = %q, want empty", fake.lastRequest.Project)
	}

	if _, ok := listMachinesViaCache(context.Background(), config, listMachinesRequest{Project: "other/gbrain"}); !ok {
		t.Fatal("expected cache path to answer")
	}
	if fake.lastRequest.Tenant != "other" || fake.lastRequest.Project != "gbrain" {
		t.Fatalf("scoped tenant/project request = %+v, want tenant=other project=gbrain", fake.lastRequest)
	}
}

func TestListMachinesViaCache_FallsBackWithNoStoredAuthToken(t *testing.T) {
	config := commandConfig{adminConfig: scconfig.Admin{Tenant: "acme"}}
	if _, ok := listMachinesViaCache(context.Background(), config, listMachinesRequest{}); ok {
		t.Fatalf("expected fallback with no stored AuthToken")
	}
}

func TestListMachinesViaCache_FallsBackWhenUnreachable(t *testing.T) {
	fake := &fakeResourceClient{err: errors.New("dial tcp: connection refused")}
	config := commandConfig{adminConfig: scconfig.Admin{Tenant: "acme"}, authResources: fake}
	if _, ok := listMachinesViaCache(context.Background(), config, listMachinesRequest{}); ok {
		t.Fatalf("expected fallback when endpoint unreachable")
	}
}

func TestListMachinesViaCache_FallsBackWhenNotReady(t *testing.T) {
	fake := &fakeResourceClient{err: errors.New(fakeResourceCacheUnavailableErr)}
	config := commandConfig{adminConfig: scconfig.Admin{Tenant: "acme"}, authResources: fake}
	if _, ok := listMachinesViaCache(context.Background(), config, listMachinesRequest{}); ok {
		t.Fatalf("expected fallback when cache not ready")
	}
}

func TestListMachinesViaCache_FallsBackWhenToggleOff(t *testing.T) {
	// The endpoint answers the exact same 503 for "toggle off" and "not
	// ready" (resourceCacheUnavailableMessage) — sc ls keys its fallback off
	// the failed call alone, never off distinguishing the two.
	fake := &fakeResourceClient{err: errors.New(fakeResourceCacheUnavailableErr)}
	config := commandConfig{adminConfig: scconfig.Admin{Tenant: "acme"}, authResources: fake}
	if _, ok := listMachinesViaCache(context.Background(), config, listMachinesRequest{}); ok {
		t.Fatalf("expected fallback when toggle is off")
	}
}

// With the toggle off (or any other fallback trigger), listMachines() — the
// existing live per-project path — runs completely unchanged, and
// formatMachineList's default (no new flags) output must not vary depending
// on whether a cache-backed listPayload happens to carry the new resource
// types. This is the byte-for-byte compatibility acceptance criterion.
func TestFormatMachineList_DefaultOutputUnaffectedByCacheOnlyFields(t *testing.T) {
	base := listPayload{
		Tenant:      tenant.Summary{Tenant: "acme", DNSSuffix: "acme.sandcastle.dev"},
		AllProjects: true,
		Machines:    []meta.Machine{{Project: "gbrain", Name: "web", Running: true}},
	}
	withExtras := base
	withExtras.Networks = []api.Network{{Name: "sc2br0", Project: "gbrain"}}
	withExtras.StoragePools = []api.StoragePool{{Name: "default"}}
	withExtras.StorageVolumes = []api.StorageVolumeFull{{StorageVolume: api.StorageVolume{Name: "vol", Project: "gbrain"}}}
	withExtras.Profiles = []api.Profile{{Name: "default", Project: "gbrain"}}
	withExtras.Images = []api.Image{{Fingerprint: "abc123", Project: "gbrain"}}

	plain := formatMachineList(base, listRenderOptions{})
	extra := formatMachineList(withExtras, listRenderOptions{})
	if plain != extra {
		t.Fatalf("output differs when cache-only fields are present but no flag requested them:\nplain=%q\nextra=%q", plain, extra)
	}
	for _, marker := range []string{"NETWORKS", "STORAGE POOLS", "STORAGE VOLUMES", "PROFILES", "IMAGES"} {
		if strings.Contains(extra, marker) {
			t.Fatalf("output contains %q section though its flag was not requested:\n%s", marker, extra)
		}
	}
}

// formatMachineList's "no machines found" short-circuit must still fire when
// no flags are set, matching pre-wish behavior exactly, even on an otherwise
// empty result.
func TestFormatMachineList_NoMachinesFoundUnchanged(t *testing.T) {
	result := listPayload{Tenant: tenant.Summary{Tenant: "acme"}, AllProjects: true}
	got := formatMachineList(result, listRenderOptions{})
	want := "No Sandcastle machines found in the current install."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// poisonTenantStore and poisonMachineStore fail the test the instant either is
// touched — the live per-project Incus path (listMachines) is the only caller
// of either interface, so wiring these into `sc ls` end to end is how t4
// proves acceptance criterion 1 ("verify it's not hitting live Incus calls"),
// not just that listMachinesViaCache was invoked (list_cache_test.go's other
// tests already show that at the function level, one layer short of the real
// `sc ls`/RunE dispatch this test goes through instead).
type poisonTenantStore struct{ t *testing.T }

func (p poisonTenantStore) ListProjects(ctx context.Context) ([]tenant.IncusProject, error) {
	p.t.Helper()
	p.t.Fatal("live tenant store touched despite a ready cache-backed answer")
	return nil, nil
}

type poisonMachineStore struct{ t *testing.T }

func (p poisonMachineStore) ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error) {
	p.t.Helper()
	p.t.Fatal("live machine store touched despite a ready cache-backed answer")
	return nil, nil
}

// TestListCommand_ToggleOnReadyAnswersFromCacheWithoutLiveIncusCall runs `sc
// ls -a` through the real command dispatch (NewRootCommand/RunE), the same
// path an operator invokes, with tenantStore/machineStore wired to fail the
// test if the live per-project path is ever reached. Only a ready cache
// answer (config.authResources returning 200) can make this test pass —
// t4's end-to-end verification of acceptance criterion 1.
func TestListCommand_ToggleOnReadyAnswersFromCacheWithoutLiveIncusCall(t *testing.T) {
	fake := &fakeResourceClient{result: authapp.ResourceListResult{
		Tenant:      tenant.Summary{Tenant: "acme", DNSSuffix: "acme.sandcastle.dev"},
		AllProjects: true,
		Machines:    []meta.Machine{{Project: "gbrain", Name: "web", Running: true}},
	}}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:          "sandcastle",
		tenantStore:   poisonTenantStore{t: t},
		machineStore:  poisonMachineStore{t: t},
		authResources: fake,
	}, "ls", "-a")
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatalf("cache endpoint called %d times, want exactly 1", fake.calls)
	}
	if !strings.Contains(stdout, "gbrain") || !strings.Contains(stdout, "web") {
		t.Fatalf("stdout = %q, want the cache-backed machine listed", stdout)
	}
}

// TestListCommand_FallsBackToLiveWhenCacheUnavailable is the mirror of the
// above at the same full-command layer: with the cache-backed endpoint
// answering as not-ready/toggle-off (the two collapse to the same client-side
// signal — see implementation-notes.md), `sc ls` must fall through to
// tenantStore/machineStore and produce exactly what the pre-wish live path
// would have shown.
func TestListCommand_FallsBackToLiveWhenCacheUnavailable(t *testing.T) {
	fake := &fakeResourceClient{err: errors.New(fakeResourceCacheUnavailableErr)}
	projects := v2TenantProjects("acme", "10.248.0.0/24", "default")
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: projects},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{{
			Tenant: "acme", Project: "default", Name: "codex", Running: true,
		}}},
		authResources: fake,
	}, "list")
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatalf("cache endpoint called %d times, want exactly 1 (attempted, then fell back)", fake.calls)
	}
	if !strings.Contains(stdout, "default") || !strings.Contains(stdout, "codex") {
		t.Fatalf("stdout = %q, want the live-path machine listed after fallback", stdout)
	}
}
