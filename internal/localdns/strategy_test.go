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
	commands := ResolverCommands("linux", "myproject.project-tld", "127.0.0.1:53541")
	if len(commands) != 2 {
		t.Fatalf("commands = %#v", commands)
	}
	if got := joinArgs(commands[0].Args); got != "resolvectl dns lo 127.0.0.1:53541" {
		t.Fatalf("dns command = %q", got)
	}
	if got := joinArgs(commands[1].Args); got != "resolvectl domain lo ~myproject.project-tld" {
		t.Fatalf("domain command = %q", got)
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
