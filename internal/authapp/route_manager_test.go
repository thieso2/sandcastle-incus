package authapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeRouteBackend records proxy-device calls and serves scripted MachineState,
// mirroring fakeReconcileStore in suffix_reconcile_test.go.
type fakeRouteBackend struct {
	states   map[string]MachineState // key "project/machine"
	stateErr map[string]error
	devices  map[string]fakeDevice
	ensured  int
	removed  int
	// fronted records every hostname list published to the shared front, and
	// frontErr scripts a front that is unreachable.
	fronted  [][]string
	frontErr error
}

type fakeDevice struct {
	localPort   int
	machineIP   string
	backendPort int
}

func newFakeBackend() *fakeRouteBackend {
	return &fakeRouteBackend{states: map[string]MachineState{}, stateErr: map[string]error{}, devices: map[string]fakeDevice{}}
}

func (f *fakeRouteBackend) EnsureProxyDevice(ctx context.Context, name string, localPort int, machineIP string, backendPort int) error {
	f.ensured++
	f.devices[name] = fakeDevice{localPort, machineIP, backendPort}
	return nil
}

func (f *fakeRouteBackend) RemoveProxyDevice(ctx context.Context, name string) error {
	f.removed++
	delete(f.devices, name)
	return nil
}

func (f *fakeRouteBackend) UpdateFrontRoutes(ctx context.Context, hostnames []string) error {
	f.fronted = append(f.fronted, append([]string(nil), hostnames...))
	return f.frontErr
}

func (f *fakeRouteBackend) MachineState(ctx context.Context, tenant, project, machine string) (MachineState, error) {
	key := tenant + "/" + project + "/" + machine
	if err := f.stateErr[key]; err != nil {
		return MachineState{}, err
	}
	return f.states[key], nil
}

// fakeCaddy captures the last applied Caddyfile.
type fakeCaddy struct {
	applied string
	calls   int
}

func (c *fakeCaddy) Apply(ctx context.Context, caddyfile string) error {
	c.calls++
	c.applied = caddyfile
	return nil
}

func newManager(t *testing.T) (RouteManager, *fakeRouteBackend, *fakeCaddy) {
	t.Helper()
	backend := newFakeBackend()
	caddy := &fakeCaddy{}
	m := RouteManager{
		DB:      newClaimsTestDB(t),
		Backend: backend,
		Caddy:   caddy,
		Render:  testCaddyConfig(),
	}
	return m, backend, caddy
}

func running(ip string) MachineState { return MachineState{Present: true, Running: true, IPv4: ip} }

func TestPublish_WiresDeviceAndCaddy(t *testing.T) {
	ctx := context.Background()
	m, backend, caddy := newManager(t)
	backend.states["acme/default/web"] = running("10.248.3.42")

	route, err := m.Publish(ctx, PublishRequest{Hostname: "web.acme.sc2.thieso2.dev", Tenant: "acme", Project: "default", Machine: "web", BackendPort: 3000})
	if err != nil {
		t.Fatal(err)
	}
	dev := backend.devices[routeDeviceName(route.Hostname)]
	if dev.machineIP != "10.248.3.42" || dev.backendPort != 3000 || dev.localPort != route.LocalPort {
		t.Fatalf("proxy device wrong: %+v (route local %d)", dev, route.LocalPort)
	}
	if caddy.calls == 0 || caddy.applied == "" {
		t.Fatal("caddy was not applied on publish")
	}
}

func TestPublish_RefusesStoppedMachine(t *testing.T) {
	ctx := context.Background()
	m, backend, _ := newManager(t)
	backend.states["acme/default/web"] = MachineState{Present: true, Running: false}
	if _, err := m.Publish(ctx, PublishRequest{Hostname: "web.acme.sc2.thieso2.dev", Tenant: "acme", Project: "default", Machine: "web", BackendPort: 3000}); err == nil {
		t.Fatal("expected publish to refuse a stopped machine")
	}
}

func TestReconcile_PrunesDeletedMachine(t *testing.T) {
	ctx := context.Background()
	m, backend, _ := newManager(t)
	backend.states["acme/default/web"] = running("10.248.3.42")
	route, _ := m.Publish(ctx, PublishRequest{Hostname: "web.acme.sc2.thieso2.dev", Tenant: "acme", Project: "default", Machine: "web", BackendPort: 3000})

	// Machine deleted out from under the route.
	backend.states["acme/default/web"] = MachineState{Present: false}
	pruned, err := m.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 pruned, got %d", pruned)
	}
	if _, found, _ := GetRoute(ctx, m.DB, route.Hostname); found {
		t.Fatal("route not pruned from registry")
	}
	if _, present := backend.devices[routeDeviceName(route.Hostname)]; present {
		t.Fatal("proxy device not removed on prune")
	}
}

func TestReconcile_KeepsStoppedMachine(t *testing.T) {
	ctx := context.Background()
	m, backend, _ := newManager(t)
	backend.states["acme/default/web"] = running("10.248.3.42")
	route, _ := m.Publish(ctx, PublishRequest{Hostname: "web.acme.sc2.thieso2.dev", Tenant: "acme", Project: "default", Machine: "web", BackendPort: 3000})

	backend.states["acme/default/web"] = MachineState{Present: true, Running: false}
	pruned, err := m.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 0 {
		t.Fatalf("stopped machine must not be pruned, got %d", pruned)
	}
	if _, found, _ := GetRoute(ctx, m.DB, route.Hostname); !found {
		t.Fatal("route wrongly removed for a stopped machine")
	}
}

func TestReconcile_RefreshesChangedIP(t *testing.T) {
	ctx := context.Background()
	m, backend, _ := newManager(t)
	backend.states["acme/default/web"] = running("10.248.3.42")
	route, _ := m.Publish(ctx, PublishRequest{Hostname: "web.acme.sc2.thieso2.dev", Tenant: "acme", Project: "default", Machine: "web", BackendPort: 3000})

	// Machine recreated with a new IP.
	backend.states["acme/default/web"] = running("10.248.3.99")
	if _, err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	dev := backend.devices[routeDeviceName(route.Hostname)]
	if dev.machineIP != "10.248.3.99" {
		t.Fatalf("device connect not refreshed, got %q", dev.machineIP)
	}
}

func TestReconcile_NeverPrunesOnTransientFailure(t *testing.T) {
	ctx := context.Background()
	m, backend, _ := newManager(t)
	backend.states["acme/default/web"] = running("10.248.3.42")
	route, _ := m.Publish(ctx, PublishRequest{Hostname: "web.acme.sc2.thieso2.dev", Tenant: "acme", Project: "default", Machine: "web", BackendPort: 3000})

	backend.stateErr["acme/default/web"] = errors.New("incus unreachable")
	pruned, err := m.Reconcile(ctx)
	if err != nil {
		t.Fatalf("reconcile should tolerate transient failures: %v", err)
	}
	if pruned != 0 {
		t.Fatalf("must not prune on transient failure, pruned %d", pruned)
	}
	if _, found, _ := GetRoute(ctx, m.DB, route.Hostname); !found {
		t.Fatal("route wrongly pruned on transient failure")
	}
}

func TestStatus_LiveVsUnhealthy(t *testing.T) {
	ctx := context.Background()
	m, backend, _ := newManager(t)
	backend.states["acme/default/web"] = running("10.248.3.42")
	route, _ := m.Publish(ctx, PublishRequest{Hostname: "web.acme.sc2.thieso2.dev", Tenant: "acme", Project: "default", Machine: "web", BackendPort: 3000})

	if got := m.Status(ctx, route); got.Status != "live" {
		t.Fatalf("running machine should be live, got %q", got.Status)
	}
	backend.states["acme/default/web"] = MachineState{Present: true, Running: false}
	if got := m.Status(ctx, route); got.Status != RouteStatusUnhealthy {
		t.Fatalf("stopped machine should be unhealthy, got %q", got.Status)
	}
}

// `sc route list` names the backend by its Machine Private Hostname, so Status
// must carry the FQDN the backend reports — including for a stopped Machine,
// where the row is unhealthy but still has to say which Machine it points at.
func TestStatus_CarriesMachineFQDN(t *testing.T) {
	ctx := context.Background()
	m, backend, _ := newManager(t)
	live := running("10.248.3.42")
	live.FQDN = "web.default.acme.sc2.thieso2.dev"
	backend.states["acme/default/web"] = live
	route, _ := m.Publish(ctx, PublishRequest{Hostname: "web.acme.sc2.thieso2.dev", Tenant: "acme", Project: "default", Machine: "web", BackendPort: 3000})

	if got := m.Status(ctx, route); got.MachineFQDN != "web.default.acme.sc2.thieso2.dev" {
		t.Fatalf("live route MachineFQDN = %q", got.MachineFQDN)
	}
	backend.states["acme/default/web"] = MachineState{Present: true, Running: false, FQDN: "web.default.acme.sc2.thieso2.dev"}
	got := m.Status(ctx, route)
	if got.Status != RouteStatusUnhealthy || got.MachineFQDN != "web.default.acme.sc2.thieso2.dev" {
		t.Fatalf("stopped route = %q / %q", got.Status, got.MachineFQDN)
	}
}

func TestStatus_CustomHostnameAwaitingDNS(t *testing.T) {
	ctx := context.Background()
	m, backend, _ := newManager(t)
	m.ResolveHost = func(context.Context, string) bool { return false } // DNS not pointed yet
	backend.states["acme/default/web"] = running("10.248.3.42")
	route, _ := m.Publish(ctx, PublishRequest{Hostname: "app.customer.com", Tenant: "acme", Project: "default", Machine: "web", BackendPort: 3000})

	// Custom hostname (not under the auth wildcard) whose DNS doesn't resolve yet.
	if got := m.Status(ctx, route); got.Status != RouteStatusAwaitingDNS {
		t.Fatalf("unresolved custom hostname should be awaiting-dns, got %q", got.Status)
	}
	// Once DNS points at the front door, it goes live.
	m.ResolveHost = func(context.Context, string) bool { return true }
	if got := m.Status(ctx, route); got.Status != RouteStatusLive {
		t.Fatalf("resolved custom hostname should be live, got %q", got.Status)
	}
}

func TestStatus_AutoSubdomainNeverAwaitingDNS(t *testing.T) {
	ctx := context.Background()
	m, backend, _ := newManager(t)
	// Auto-subdomains sit under the wildcard; DNS is never consulted for them.
	m.ResolveHost = func(context.Context, string) bool { t.Fatal("must not DNS-check an auto-subdomain"); return false }
	backend.states["acme/default/web"] = running("10.248.3.42")
	route, _ := m.Publish(ctx, PublishRequest{Hostname: "web.acme.sc2.thieso2.dev", Tenant: "acme", Project: "default", Machine: "web", BackendPort: 3000})
	if got := m.Status(ctx, route); got.Status != RouteStatusLive {
		t.Fatalf("auto-subdomain should be live, got %q", got.Status)
	}
}

// The shared front learns the hostname list from the Auth App, so every
// Caddyfile regeneration must publish it — that is what makes a custom hostname
// work with no edit on the front.
func TestPublishSendsHostnamesToFront(t *testing.T) {
	manager, backend, _ := newManager(t)
	backend.states["acme/web/api"] = MachineState{Present: true, Running: true, IPv4: "10.1.0.5"}
	ctx := context.Background()
	if _, err := manager.Publish(ctx, PublishRequest{
		Hostname: "app.customer.com", Tenant: "acme", Project: "web", Machine: "api", BackendPort: 3000,
	}); err != nil {
		t.Fatal(err)
	}
	if len(backend.fronted) == 0 {
		t.Fatal("publish must push the hostname list to the front")
	}
	last := backend.fronted[len(backend.fronted)-1]
	if len(last) != 1 || last[0] != "app.customer.com" {
		t.Errorf("front received %v, want [app.customer.com]", last)
	}
}

// A front that cannot be reached must not fail the publish: the Route is
// already live on this appliance and the reconcile pass retries the front.
func TestPublishSucceedsWhenFrontIsUnreachable(t *testing.T) {
	manager, backend, _ := newManager(t)
	backend.states["acme/web/api"] = MachineState{Present: true, Running: true, IPv4: "10.1.0.5"}
	backend.frontErr = errors.New("front is down")
	var logged []string
	manager.Logf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	route, err := manager.Publish(context.Background(), PublishRequest{
		Hostname: "app.customer.com", Tenant: "acme", Project: "web", Machine: "api", BackendPort: 3000,
	})
	if err != nil {
		t.Fatalf("a down front must not fail the publish: %v", err)
	}
	if route.Hostname != "app.customer.com" {
		t.Errorf("route = %+v, want it published anyway", route)
	}
	if len(logged) == 0 || !strings.Contains(logged[0], "front") {
		t.Errorf("the failure must be logged, got %v", logged)
	}
}

func TestRenderFrontSNIFragment(t *testing.T) {
	out := RenderFrontSNIFragment("sandcastle_obelix", "10.239.150.87:443",
		[]string{"b.example.dev", "a.example.dev", "b.example.dev", ""})
	if !strings.Contains(out, "@sandcastle_obelix tls sni a.example.dev b.example.dev\n") {
		t.Errorf("hosts must be sorted and de-duplicated:\n%s", out)
	}
	if !strings.Contains(out, "proxy tcp/10.239.150.87:443") {
		t.Errorf("fragment must proxy to the appliance:\n%s", out)
	}
	// Same input renders byte-identical output, so an unchanged registry does
	// not churn the front.
	again := RenderFrontSNIFragment("sandcastle_obelix", "10.239.150.87:443",
		[]string{"b.example.dev", "a.example.dev"})
	if out != again {
		t.Error("render must be deterministic")
	}
	empty := RenderFrontSNIFragment("sandcastle_obelix", "10.239.150.87:443", nil)
	if strings.Contains(empty, "tls sni") {
		t.Errorf("an install with no routes must forward nothing:\n%s", empty)
	}
}

func TestFrontMatcherNameIsUniquePerInstall(t *testing.T) {
	if got := FrontMatcherName("obelix"); got != "sandcastle_obelix" {
		t.Errorf("FrontMatcherName(obelix) = %q", got)
	}
	if got := FrontMatcherName("sc-2 test"); got != "sandcastle_sc_2_test" {
		t.Errorf("unsafe characters must be folded, got %q", got)
	}
	if got := FrontMatcherName(""); got != "sandcastle_routes" {
		t.Errorf("empty prefix = %q", got)
	}
}
