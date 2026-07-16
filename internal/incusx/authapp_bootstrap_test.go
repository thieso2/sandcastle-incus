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
