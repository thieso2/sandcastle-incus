package localdns

import "testing"

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

func TestLinuxResolverCommandsUseSystemdResolved(t *testing.T) {
	t.Setenv("SANDCASTLE_RESOLVER_DIR", "")
	commands := ResolverCommands("linux", "acme.sandcastle.internal", "10.248.0.3:53")
	if len(commands) != 2 {
		t.Fatalf("commands = %#v", commands)
	}
	if got := joinArgs(commands[0].Args); got != "resolvectl dns lo 10.248.0.3:53" {
		t.Fatalf("dns command = %q", got)
	}
	if got := joinArgs(commands[1].Args); got != "resolvectl domain lo ~acme.sandcastle.internal" {
		t.Fatalf("domain command = %q", got)
	}
}

func TestLinuxResolverCommandsSkippedWhenResolverDirIsOverridden(t *testing.T) {
	t.Setenv("SANDCASTLE_RESOLVER_DIR", t.TempDir())
	commands := ResolverCommands("linux", "acme.sandcastle.internal", "10.248.0.3:53")
	if len(commands) != 0 {
		t.Fatalf("commands = %#v, want none with resolver dir override", commands)
	}
}

func TestResolverSyncCommandsUseInstalledTenantSuffixes(t *testing.T) {
	t.Setenv("SANDCASTLE_RESOLVER_DIR", "")
	state := State{Tenants: []TenantState{
		{DNSSuffix: "beta.sandcastle.internal", DNSEndpoint: EndpointState{IP: "10.248.0.3", Port: 53}},
		{DNSSuffix: "alpha.sandcastle.internal.", DNSEndpoint: EndpointState{IP: "10.248.1.3", Port: 53}},
		{DNSSuffix: "broken.sandcastle.internal", DNSEndpoint: EndpointState{IP: "not-an-ip", Port: 53}},
	}}
	commands := ResolverSyncCommands(StrategySystemdResolve, state)
	if len(commands) != 2 {
		t.Fatalf("commands = %#v", commands)
	}
	if got := joinArgs(commands[0].Args); got != "resolvectl dns lo 10.248.0.3:53 10.248.1.3:53" {
		t.Fatalf("dns command = %q", got)
	}
	if got := joinArgs(commands[1].Args); got != "resolvectl domain lo ~alpha.sandcastle.internal ~beta.sandcastle.internal" {
		t.Fatalf("domain command = %q", got)
	}
}

func TestResolverSyncCommandsRevertWhenNoDomainsRemain(t *testing.T) {
	t.Setenv("SANDCASTLE_RESOLVER_DIR", "")
	commands := ResolverSyncCommands(StrategySystemdResolve, State{})
	if len(commands) != 1 {
		t.Fatalf("commands = %#v", commands)
	}
	if got := joinArgs(commands[0].Args); got != "resolvectl revert lo" {
		t.Fatalf("revert command = %q", got)
	}
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
