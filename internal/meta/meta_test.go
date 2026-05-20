package meta

import "testing"

func TestProjectConfigRoundTrip(t *testing.T) {
	input := Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.88.17.0/24",
		DefaultTemplate: "ai",
		Tailscale: Tailscale{
			State:            "connected",
			AdvertisedRoutes: []string{"10.88.17.0/24"},
		},
		PublicRoutes: []PublicRoute{{
			Hostname:  "app.example.com",
			Sandbox:   "codex",
			RoutePort: 5173,
		}},
	}
	config, err := ProjectConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	if config[KeyKind] != KindProject {
		t.Fatalf("kind = %q", config[KeyKind])
	}
	if config[KeyOwner] != "alice" {
		t.Fatalf("owner scalar = %q", config[KeyOwner])
	}
	output, err := ParseProjectConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if output.Owner != input.Owner || output.Project != input.Project || output.PrivateCIDR != input.PrivateCIDR {
		t.Fatalf("round trip = %#v, want %#v", output, input)
	}
	if len(output.Tailscale.AdvertisedRoutes) != 1 {
		t.Fatalf("advertised routes = %#v", output.Tailscale.AdvertisedRoutes)
	}
	if len(output.PublicRoutes) != 1 || output.PublicRoutes[0].Hostname != "app.example.com" {
		t.Fatalf("public routes = %#v", output.PublicRoutes)
	}
}

func TestSandboxConfigRoundTrip(t *testing.T) {
	input := Sandbox{
		Owner:        "alice",
		Project:      "myproject",
		Name:         "codex",
		AppPort:      3000,
		PrivateIP:    "10.88.17.21",
		LinuxUser:    "alice",
		HomeDir:      "codex",
		WorkspaceDir: ".",
		ExtraSANs:    []string{"app.example.test"},
	}
	config, err := SandboxConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	if config[KeyAppPort] != "3000" {
		t.Fatalf("app port scalar = %q", config[KeyAppPort])
	}
	if config[KeyLinuxUser] != "alice" {
		t.Fatalf("linux user scalar = %q", config[KeyLinuxUser])
	}
	output, err := ParseSandboxConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if output.Name != input.Name || output.PrivateIP != input.PrivateIP || output.LinuxUser != input.LinuxUser {
		t.Fatalf("round trip = %#v, want %#v", output, input)
	}
	if len(output.ExtraSANs) != 1 {
		t.Fatalf("extra SANs = %#v", output.ExtraSANs)
	}
}

func TestRouteConfigRoundTrip(t *testing.T) {
	input := Route{
		Hostname:        "app.example.com",
		TargetOwner:     "alice",
		TargetProject:   "myproject",
		TargetSandbox:   "codex",
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
	_, err := ParseProjectConfig(map[string]string{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIsManaged(t *testing.T) {
	if IsManaged(map[string]string{}) {
		t.Fatal("empty config should be unmanaged")
	}
	if !IsManaged(map[string]string{KeyKind: KindProject, KeyVersion: "1"}) {
		t.Fatal("Sandcastle config should be managed")
	}
}
