package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

const version = "0.0.0-dev"

type outputFormat string

const (
	outputText outputFormat = "text"
	outputJSON outputFormat = "json"
)

type commandConfig struct {
	name         string
	stdout       io.Writer
	stderr       io.Writer
	projectStore project.IncusProjectStore
	adminConfig  scconfig.Admin
}

type rootOptions struct {
	output outputFormat
}

// Execute runs the Sandcastle CLI and returns a process exit code.
func Execute(name string, args []string) int {
	adminConfig := scconfig.LoadAdminFromEnv()
	cmd := NewRootCommand(commandConfig{
		name:        name,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		adminConfig: adminConfig,
		projectStore: incusx.NewProjectStore(
			adminConfig.Remote,
		),
	})
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// NewRootCommand builds the Sandcastle command tree.
func NewRootCommand(config commandConfig) *cobra.Command {
	if config.name == "" {
		config.name = "sandcastle"
	}
	if config.stdout == nil {
		config.stdout = io.Discard
	}
	if config.stderr == nil {
		config.stderr = io.Discard
	}
	if config.projectStore == nil {
		config.projectStore = project.MemoryStore{}
	}
	if config.adminConfig.Remote == "" {
		config.adminConfig = scconfig.LoadAdminFromEnv()
	}

	opts := &rootOptions{output: outputText}
	root := &cobra.Command{
		Use:           config.name,
		Short:         "Manage Incus-backed Sandcastle development sandboxes",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().Var(&opts.output, "output", "output format: text or json")

	root.AddCommand(newVersionCommand(config, opts))
	root.AddCommand(newListCommand(config, opts))
	root.AddCommand(newAdminCommand(config, opts))

	return root
}

func (f *outputFormat) Set(value string) error {
	switch outputFormat(value) {
	case outputText, outputJSON:
		*f = outputFormat(value)
		return nil
	default:
		return fmt.Errorf("unsupported output format %q", value)
	}
}

func (f outputFormat) String() string {
	if f == "" {
		return string(outputText)
	}
	return string(f)
}

func (f outputFormat) Type() string {
	return "format"
}
