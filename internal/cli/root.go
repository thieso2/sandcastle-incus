package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

const version = "0.0.0-dev"

type outputFormat string

const (
	outputText outputFormat = "text"
	outputJSON outputFormat = "json"
)

type commandConfig struct {
	name           string
	stdout         io.Writer
	stderr         io.Writer
	projectStore   project.IncusProjectStore
	adminConfig    scconfig.Admin
	projectCreator project.Creator
	projectDeleter project.Deleter
	topologyStore  project.TopologyStore
	trustManager   usertrust.Manager
	sandboxCreator sandbox.Creator
	sandboxControl sandbox.Controller
	sandboxPort    sandbox.PortSetter
	dnsApplier     dns.Applier
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
		projectCreator: incusx.NewProjectCreator(adminConfig.Remote),
		projectDeleter: incusx.NewProjectDeleter(adminConfig.Remote),
		topologyStore:  incusx.NewTopologyStore(adminConfig.Remote),
		trustManager:   incusx.NewTrustManager(adminConfig.Remote),
		sandboxCreator: incusx.NewSandboxCreator(adminConfig.Remote),
		sandboxControl: incusx.NewSandboxController(adminConfig.Remote),
		sandboxPort:    incusx.NewSandboxPortSetter(adminConfig.Remote),
		dnsApplier:     incusx.NewDNSManager(adminConfig.Remote),
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
	root.AddCommand(newStatusCommand(config, opts))
	root.AddCommand(newAddCommand(config, opts))
	root.AddCommand(newSandboxLifecycleCommand(config, opts, "start", sandbox.ActionStart, false))
	root.AddCommand(newSandboxLifecycleCommand(config, opts, "stop", sandbox.ActionStop, false))
	root.AddCommand(newSandboxLifecycleCommand(config, opts, "restart", sandbox.ActionRestart, false))
	root.AddCommand(newSandboxLifecycleCommand(config, opts, "rm", sandbox.ActionRemove, true))
	root.AddCommand(newPortCommand(config, opts))
	root.AddCommand(newDNSCommand(config, opts))
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
