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
	got, err := systemdResolvedUnitForExecutable("E2Edns.", "10.251.1.3:53", "/usr/local/bin/sandcastle")
	if err != nil {
		t.Fatal(err)
	}
	link := resolvedLinkName("e2edns")
	if len(link) > 15 {
		t.Fatalf("link name %q exceeds IFNAMSIZ", link)
	}
	address := strings.TrimSuffix(resolvedLinkAddress("e2edns"), "/32")
	for _, want := range []string{
		// The scope's DNS server is the dns-proxy on the dummy link's OWN
		// address, never the tenant CoreDNS directly: resolved binds a link
		// scope's UDP sockets to the link, and a dummy link blackholes any
		// off-link destination — resolved then silently degrades the server
		// to TCP and fails one lookup after every idle period (seen live on
		// majestix). Only an on-link address works over UDP.
		"ExecStart=/usr/local/bin/sandcastle dns-proxy --link " + link +
			" --address " + address + " --domain e2edns --upstream 10.251.1.3:53",
		"ExecStopPost=/bin/sh -c 'ip link delete " + link + " 2>/dev/null || true'",
		"Restart=on-failure",
		// A resolved restart wipes per-link config; PartOf propagates the
		// restart here so the scope is re-applied.
		"PartOf=systemd-resolved.service",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("unit missing %q:\n%s", want, got)
		}
	}
	// A non-default port is preserved in the upstream endpoint.
	withPort, err := systemdResolvedUnitForExecutable("e2edns", "10.251.1.3:5353", "/usr/local/bin/sandcastle")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withPort, "--upstream 10.251.1.3:5353") {
		t.Fatalf("non-default port must be preserved: %q", withPort)
	}
	if _, err := systemdResolvedUnitForExecutable("", "10.251.1.3:53", "/x"); err == nil {
		t.Fatal("empty suffix must error")
	}
	if _, err := systemdResolvedUnitForExecutable("e2edns", "not-an-ip:53", "/x"); err == nil {
		t.Fatal("invalid endpoint IP must error")
	}
	if _, err := systemdResolvedUnitForExecutable("e2edns", "10.251.1.3:53", ""); err == nil {
		t.Fatal("empty executable must error")
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
