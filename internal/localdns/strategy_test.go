package localdns

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolverStrategyForPlatforms(t *testing.T) {
	if ResolverStrategy("darwin") != StrategyMacOSResolver {
		t.Fatalf("darwin strategy = %q", ResolverStrategy("darwin"))
	}
	if ResolverStrategy("linux") != StrategySystemdResolve {
		t.Fatalf("linux strategy = %q", ResolverStrategy("linux"))
	}
	if ResolverStrategy("plan9") != StrategyFileOnly {
		t.Fatalf("fallback strategy = %q", ResolverStrategy("plan9"))
	}
}

func TestLinuxResolverPathIsResolvedDropIn(t *testing.T) {
	t.Setenv("SANDCASTLE_RESOLVER_DIR", "")
	got := ResolverPath("linux", "acme.sandcastle.internal")
	want := "/etc/systemd/resolved.conf.d/10-sandcastle-acme.sandcastle.internal.conf"
	if got != want {
		t.Fatalf("resolver path = %q, want %q", got, want)
	}
	// The prefix must sort before the public upstream drop-in so the tenant
	// CoreDNS is tried first (systemd doesn't fall through on NXDOMAIN).
	if filepath.Base(got) >= "50-public-upstream.conf" {
		t.Fatalf("drop-in %q must sort before 50-public-upstream.conf", filepath.Base(got))
	}
}

func TestLinuxResolverCommandsReloadSystemdResolved(t *testing.T) {
	t.Setenv("SANDCASTLE_RESOLVER_DIR", "")
	commands := ResolverCommands("linux", "acme.sandcastle.internal", "10.248.0.3:53")
	if len(commands) != 1 || joinArgs(commands[0].Args) != "systemctl restart systemd-resolved" {
		t.Fatalf("commands = %#v", commands)
	}
	// The loopback link is exactly what modern systemd-resolved rejects, so it
	// must never appear in a resolver command.
	for _, c := range commands {
		if joinArgs(c.Args) == "resolvectl dns lo 10.248.0.3:53" || contains(c.Args, "lo") {
			t.Fatalf("resolver commands must not target the loopback link: %#v", c.Args)
		}
	}
}

func TestLinuxResolverCommandsSkippedWhenResolverDirIsOverridden(t *testing.T) {
	t.Setenv("SANDCASTLE_RESOLVER_DIR", t.TempDir())
	commands := ResolverCommands("linux", "acme.sandcastle.internal", "10.248.0.3:53")
	if len(commands) != 0 {
		t.Fatalf("commands = %#v, want none with resolver dir override", commands)
	}
}

func TestResolverSyncCommandsReloadSystemdResolved(t *testing.T) {
	t.Setenv("SANDCASTLE_RESOLVER_DIR", "")
	state := State{Tenants: []TenantState{
		{DNSSuffix: "beta.sandcastle.internal", DNSEndpoint: EndpointState{IP: "10.248.0.3", Port: 53}},
	}}
	commands := ResolverSyncCommands(StrategySystemdResolve, state)
	if len(commands) != 1 || joinArgs(commands[0].Args) != "systemctl restart systemd-resolved" {
		t.Fatalf("commands = %#v", commands)
	}
	// Even with no tenants left, we still reload so the removed drop-in's route
	// disappears.
	empty := ResolverSyncCommands(StrategySystemdResolve, State{})
	if len(empty) != 1 || joinArgs(empty[0].Args) != "systemctl restart systemd-resolved" {
		t.Fatalf("empty-state commands = %#v", empty)
	}
}

func TestSystemdResolvedDropInRoutesSuffixToEndpoint(t *testing.T) {
	got, err := SystemdResolvedDropIn("E2Edns.", "10.251.1.3:53")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "\nDNS=10.251.1.3\n") {
		t.Fatalf("drop-in must set DNS to the endpoint IP (port 53 elided): %q", got)
	}
	if !strings.Contains(got, "\nDomains=~e2edns\n") {
		t.Fatalf("drop-in must route the lowercased suffix as a routing domain: %q", got)
	}
	// A non-default port is preserved in systemd's IP:port form.
	withPort, err := SystemdResolvedDropIn("e2edns", "10.251.1.3:5353")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withPort, "\nDNS=10.251.1.3:5353\n") {
		t.Fatalf("non-default port must be preserved: %q", withPort)
	}
	if _, err := SystemdResolvedDropIn("", "10.251.1.3:53"); err == nil {
		t.Fatal("empty suffix must error")
	}
	if _, err := SystemdResolvedDropIn("e2edns", "not-an-ip:53"); err == nil {
		t.Fatal("invalid endpoint IP must error")
	}
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func joinArgs(args []string) string {
	output := ""
	for index, arg := range args {
		if index > 0 {
			output += " "
		}
		output += arg
	}
	return output
}
