package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Every destructive command must expose --yes.
//
// `sc-adm tenant delete` lost its --yes registration in the v1 removal (#52)
// while the RunE still read the `yes` variable and the error still told the user
// to pass --yes. The flag was simply unknown, so the command could not be run
// non-interactively at all — and nothing failed until the e2e teardown hit it.
// A per-command assertion would have missed it too, so walk the tree.
func TestDestructiveCommandsRegisterYes(t *testing.T) {
	roots := map[string]*cobra.Command{
		"sc":     NewRootCommand(commandConfig{name: "sc"}),
		"sc-adm": NewAdminRootCommand(commandConfig{name: "sc-adm"}),
	}
	for name, root := range roots {
		walkCommands(root, func(cmd *cobra.Command) {
			if !isDestructive(cmd) {
				return
			}
			if cmd.Flags().Lookup("yes") == nil {
				t.Errorf("%s %s: destructive command does not register --yes", name, cmd.CommandPath())
			}
		})
	}
}

// A command that names --yes in its own confirmation error must accept it.
func TestYesFlagIsParsable(t *testing.T) {
	root := NewAdminRootCommand(commandConfig{name: "sc-adm"})
	cmd, _, err := root.Find([]string{"tenant", "delete"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.ParseFlags([]string{"--yes", "--purge"}); err != nil {
		t.Fatalf("sc-adm tenant delete --yes --purge: %v", err)
	}
}

func walkCommands(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)
	for _, child := range cmd.Commands() {
		walkCommands(child, fn)
	}
}

func isDestructive(cmd *cobra.Command) bool {
	if !cmd.Runnable() {
		return false
	}
	verb := strings.Fields(cmd.Use)
	if len(verb) == 0 {
		return false
	}
	switch verb[0] {
	case "delete", "destroy", "purge":
		// Destroys server-side state. Must be confirmable non-interactively.
		return true
	}
	// `dns uninstall` and `trust uninstall` only revert local host configuration
	// (resolver entries, trust store) and have never taken --yes.
	return false
}
