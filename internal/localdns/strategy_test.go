package localdns

import (
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

func TestLinuxResolverPathIsPerSuffixUnit(t *testing.T) {
	t.Setenv("SANDCASTLE_RESOLVER_DIR", "")
	got := ResolverPath("linux", "acme.sandcastle.internal")
	want := "/etc/systemd/system/sandcastle-dns-acme.sandcastle.internal.service"
	if got != want {
		t.Fatalf("resolver path = %q, want %q", got, want)
	}
}

func TestLinuxResolverCommandsEnableAndRestartTheUnit(t *testing.T) {
	t.Setenv("SANDCASTLE_RESOLVER_DIR", "")
	commands := ResolverCommands("linux", "acme.sandcastle.internal", "10.248.0.3:53")
	want := []string{
		"systemctl daemon-reload",
		"systemctl enable sandcastle-dns-acme.sandcastle.internal.service",
		"systemctl restart sandcastle-dns-acme.sandcastle.internal.service",
	}
	if len(commands) != len(want) {
		t.Fatalf("commands = %#v", commands)
	}
	for index, command := range commands {
		if joinArgs(command.Args) != want[index] {
			t.Fatalf("command[%d] = %q, want %q", index, joinArgs(command.Args), want[index])
		}
	}
	// The loopback link is exactly what modern systemd-resolved rejects, so it
	// must never appear in a resolver command.
	for _, c := range commands {
		if contains(c.Args, "lo") {
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

func TestResolverSyncCommandsReloadSystemd(t *testing.T) {
	t.Setenv("SANDCASTLE_RESOLVER_DIR", "")
	state := State{Tenants: []TenantState{
		{DNSSuffix: "beta.sandcastle.internal", DNSEndpoint: EndpointState{IP: "10.248.0.3", Port: 53}},
	}}
	commands := ResolverSyncCommands(StrategySystemdResolve, state)
	if len(commands) != 1 || joinArgs(commands[0].Args) != "systemctl daemon-reload" {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestResolverPreUninstallStopsTheUnit(t *testing.T) {
	t.Setenv("SANDCASTLE_RESOLVER_DIR", "")
	commands := ResolverPreUninstallCommands(StrategySystemdResolve, "castle")
	if len(commands) != 1 || joinArgs(commands[0].Args) != "systemctl disable --now sandcastle-dns-castle.service" {
		t.Fatalf("commands = %#v", commands)
	}
}

// Per-domain DNS routing in systemd-resolved only works ACROSS link scopes: a
// global drop-in merges every tenant's server into one flat list where only
// the rotating "current server" is asked — with two tenants one zone always
// dies (authoritative NXDOMAIN from the wrong server). Each suffix therefore
// gets its own dummy link + scope via a persistent systemd unit.
func TestSystemdResolvedUnitCreatesPerSuffixLinkScope(t *testing.T) {
	got, err := SystemdResolvedUnit("E2Edns.", "10.251.1.3:53")
	if err != nil {
		t.Fatal(err)
	}
	link := resolvedLinkName("e2edns")
	if len(link) > 15 {
		t.Fatalf("link name %q exceeds IFNAMSIZ", link)
	}
	for _, want := range []string{
		"ip link add " + link + " type dummy",
		// resolved only activates a link's DNS scope once the link has an
		// address — a bare up dummy stays "Current Scopes: none".
		"ip addr replace " + resolvedLinkAddress("e2edns") + " dev " + link,
		"resolvectl dns " + link + " 10.251.1.3",
		`resolvectl domain ` + link + ` "~e2edns"`,
		"ip link delete " + link,
		// A resolved restart wipes per-link config; PartOf propagates the
		// restart here so the scope is re-applied.
		"PartOf=systemd-resolved.service",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("unit missing %q:\n%s", want, got)
		}
	}
	// A non-default port is preserved in systemd's IP:port form.
	withPort, err := SystemdResolvedUnit("e2edns", "10.251.1.3:5353")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withPort, "resolvectl dns "+link+" 10.251.1.3:5353") {
		t.Fatalf("non-default port must be preserved: %q", withPort)
	}
	if _, err := SystemdResolvedUnit("", "10.251.1.3:53"); err == nil {
		t.Fatal("empty suffix must error")
	}
	if _, err := SystemdResolvedUnit("e2edns", "not-an-ip:53"); err == nil {
		t.Fatal("invalid endpoint IP must error")
	}
	// Distinct suffixes must get distinct links (scopes must not collide).
	if resolvedLinkName("castle") == resolvedLinkName("idefix") {
		t.Fatal("distinct suffixes must map to distinct link names")
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
