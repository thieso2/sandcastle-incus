package meta

import "testing"

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
	_, err := ParseMachineConfig(map[string]string{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIsManaged(t *testing.T) {
	if IsManaged(map[string]string{}) {
		t.Fatal("empty config should be unmanaged")
	}
	if !IsManaged(map[string]string{KeyKind: KindInfra, KeyVersion: "1"}) {
		t.Fatal("Sandcastle config should be managed")
	}
}
