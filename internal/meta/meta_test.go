package meta

import "testing"

func TestTenantConfigRoundTrip(t *testing.T) {
	input := Tenant{
		Tenant:      "acme",
		PrivateCIDR: "10.88.17.0/24",
		Projects: []Project{
			{Name: "default"},
			{Name: "website", CreatedBy: "alice", DockerAutostart: true},
		},
		Tailscale: Tailscale{
			State:            "connected",
			AdvertisedRoutes: []string{"10.88.17.0/24"},
		},
		PublicRoutes: []PublicRoute{{
			Hostname:  "app.example.com",
			Project:   "website",
			Machine:   "codex",
			RoutePort: 5173,
		}},
	}
	config, err := TenantConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	if config[KeyKind] != KindTenant {
		t.Fatalf("kind = %q", config[KeyKind])
	}
	if config[KeyTenant] != "acme" {
		t.Fatalf("tenant scalar = %q", config[KeyTenant])
	}
	output, err := ParseTenantConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if output.Tenant != input.Tenant || output.PrivateCIDR != input.PrivateCIDR {
		t.Fatalf("round trip = %#v, want %#v", output, input)
	}
	if len(output.Projects) != 2 || output.Projects[1].Name != "website" || !output.Projects[1].DockerAutostart {
		t.Fatalf("projects = %#v", output.Projects)
	}
	if len(output.Tailscale.AdvertisedRoutes) != 1 {
		t.Fatalf("advertised routes = %#v", output.Tailscale.AdvertisedRoutes)
	}
	if len(output.PublicRoutes) != 1 || output.PublicRoutes[0].Machine != "codex" {
		t.Fatalf("public routes = %#v", output.PublicRoutes)
	}
}

func TestMachineConfigRoundTrip(t *testing.T) {
	input := Machine{
		Tenant:          "acme",
		Project:         "website",
		Name:            "codex",
		Type:            MachineTypeContainer,
		Template:        "ai",
		AppPort:         3000,
		PrivateIP:       "10.88.17.21",
		LinuxUser:       "alice",
		CloudIdentity:   "gcp",
		DockerAutostart: true,
		HomeDir:         "website/codex",
		WorkspaceDir:    "website/codex",
		ContainerTools:  true,
		ExtraSANs:       []string{"app.example.test"},
		CreatedBy:       "alice",
	}
	config, err := MachineConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	if config[KeyAppPort] != "3000" {
		t.Fatalf("app port scalar = %q", config[KeyAppPort])
	}
	if config[KeyMachine] != "codex" {
		t.Fatalf("machine scalar = %q", config[KeyMachine])
	}
	output, err := ParseMachineConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if output.Name != input.Name || output.PrivateIP != input.PrivateIP || output.LinuxUser != input.LinuxUser || output.CloudIdentity != input.CloudIdentity || output.DockerAutostart != input.DockerAutostart {
		t.Fatalf("round trip = %#v, want %#v", output, input)
	}
	if len(output.ExtraSANs) != 1 {
		t.Fatalf("extra SANs = %#v", output.ExtraSANs)
	}
	if !output.ContainerTools {
		t.Fatalf("ContainerTools = false, want true")
	}
}

func TestRouteConfigRoundTrip(t *testing.T) {
	input := Route{
		Hostname:        "app.example.com",
		TargetTenant:    "acme",
		TargetProject:   "website",
		TargetMachine:   "codex",
		TargetIP:        "10.248.0.20",
		RoutePort:       5173,
		CreatedBy:       "alice",
		IngressAttached: true,
	}
	config, err := RouteConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	if config[KeyKind] != KindRoute {
		t.Fatalf("kind = %q", config[KeyKind])
	}
	if config[KeyHostname] != "app.example.com" {
		t.Fatalf("hostname scalar = %q", config[KeyHostname])
	}
	if config[KeyTenant] != "acme" {
		t.Fatalf("tenant scalar = %q", config[KeyTenant])
	}
	if config[KeyAppPort] != "5173" {
		t.Fatalf("app port scalar = %q", config[KeyAppPort])
	}
	if config[KeyCreatedBy] != "alice" {
		t.Fatalf("created by scalar = %q", config[KeyCreatedBy])
	}
	output, err := ParseRouteConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if output.Hostname != input.Hostname || output.TargetIP != input.TargetIP || output.RoutePort != input.RoutePort || output.CreatedBy != input.CreatedBy {
		t.Fatalf("round trip = %#v, want %#v", output, input)
	}
}

func TestParseRejectsUnmanagedConfig(t *testing.T) {
	_, err := ParseTenantConfig(map[string]string{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIsManaged(t *testing.T) {
	if IsManaged(map[string]string{}) {
		t.Fatal("empty config should be unmanaged")
	}
	if !IsManaged(map[string]string{KeyKind: KindTenant, KeyVersion: "1"}) {
		t.Fatal("Sandcastle config should be managed")
	}
}
