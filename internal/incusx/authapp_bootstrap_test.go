package incusx

import "testing"

func TestAuthAppDevices_RouteIngressBindsHostPorts(t *testing.T) {
	// Cloudflare login (no host ports for the auth hostname) BUT route ingress on
	// → the appliance must still bind host :80/:443 for route certs.
	devices := authAppDevices(BootstrapAuthAppRequest{
		Bridge:       "sc2-net",
		StoragePool:  "local",
		IngressMode:  IngressCloudflare,
		RouteIngress: IngressACME,
	})
	for _, name := range []string{"http", "https"} {
		if _, ok := devices[name]; !ok {
			t.Fatalf("route ingress should bind host device %q, devices=%v", name, devices)
		}
	}
}

func TestAuthAppDevices_ProxiedRouteIngressLeavesHostPorts(t *testing.T) {
	// acme-proxied: an upstream SNI proxy owns the host :80/:443 and forwards to
	// the appliance, so the appliance must not try to claim them (it would fail to
	// bind against the proxy that is already there).
	devices := authAppDevices(BootstrapAuthAppRequest{
		Bridge:       "sc2-net",
		StoragePool:  "local",
		IngressMode:  IngressCloudflare,
		RouteIngress: IngressACMEProxied,
	})
	for _, name := range []string{"http", "https"} {
		if _, ok := devices[name]; ok {
			t.Fatalf("acme-proxied route ingress must not bind host device %q, devices=%v", name, devices)
		}
	}
}

func TestAuthAppDevices_NoIngressNoHostPorts(t *testing.T) {
	devices := authAppDevices(BootstrapAuthAppRequest{
		Bridge:      "sc2-net",
		StoragePool: "local",
		IngressMode: IngressCloudflare,
	})
	if _, ok := devices["https"]; ok {
		t.Fatalf("cloudflare-only (no route ingress) must not bind host :443, devices=%v", devices)
	}
}
