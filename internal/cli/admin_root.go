package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/spf13/cobra"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/images"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/routebroker"
)

// ExecuteAdmin runs the Sandcastle admin CLI and returns a process exit code.
// It always uses the global Incus config (~/.config/incus/) with admin TLS certificates —
// INCUS_CONF is never set so the OS default applies.
func ExecuteAdmin(name string, args []string) int {
	adminConfig := scconfig.LoadAdmin()
	verbose := os.Getenv("VERBOSE") == "1"

	// Prefer explicit admin_remote; fall back to cert/IP-based auto-detection;
	// finally fall back to the global Incus default remote.
	adminRemote := adminConfig.AdminRemote
	if adminRemote == "" {
		adminRemote = detectAdminRemote(adminConfig.Remote, verbose)
		if verbose && adminRemote != "" {
			fmt.Fprintf(os.Stderr, "[verbose] admin remote auto-detected: %s\n", adminRemote)
		}
	}
	if adminRemote == "" {
		if globalCfg, err := cliconfig.LoadConfig(""); err == nil {
			adminRemote = globalCfg.DefaultRemote
			if verbose && adminRemote != "" {
				fmt.Fprintf(os.Stderr, "[verbose] admin remote: using global incus default %q\n", adminRemote)
			}
		}
	}
	if adminRemote != "" {
		adminConfig.Remote = adminRemote
	}
	// INCUS_CONF intentionally not set → uses ~/.config/incus/ (admin certs)

	if verbose {
		incusConf := os.Getenv("INCUS_CONF")
		if incusConf == "" {
			incusConf = "~/.config/incus (default)"
		}
		fmt.Fprintf(os.Stderr, "[verbose] incus config: %s\n[verbose] incus remote: %s\n", incusConf, adminConfig.Remote)
	}

	directRouteManager := incusx.NewRouteManager(adminConfig.Remote)
	directRouteManager.InfrastructureProject = adminConfig.InfrastructureProject
	directRouteManager.LetsEncryptEmail = adminConfig.LetsEncryptEmail

	cmd := NewAdminRootCommand(commandConfig{
		name:                 name,
		stdin:                os.Stdin,
		stdout:               os.Stdout,
		stderr:               os.Stderr,
		adminConfig:          adminConfig,
		projectStore:         incusx.NewProjectStore(adminConfig.Remote),
		projectCreator:       incusx.NewProjectCreator(adminConfig.Remote).WithVerbose(verbose, os.Stderr),
		projectDeleter:       incusx.NewProjectDeleter(adminConfig.Remote).WithVerbose(verbose, os.Stderr),
		projectSSHKeyUpdater: incusx.NewProjectSSHKeyManager(adminConfig.Remote),
		infraCreator:         incusx.NewInfrastructureCreator(adminConfig.Remote),
		infraDeleter:         incusx.NewInfrastructureDeleter(adminConfig.Remote),
		imageManager:         incusx.NewImageManager(adminConfig.Remote),
		imageBuilder:         images.LocalBuilder{},
		imageImporter:        images.LocalImporter{},
		topologyStore:        incusx.NewTopologyStore(adminConfig.Remote),
		trustManager:         incusx.NewTrustManager(adminConfig.Remote),
		routeBroker: routebroker.HTTPRunner{Server: routebroker.Server{
			Admin:         adminConfig,
			Projects:      incusx.NewProjectStore(adminConfig.Remote),
			Sandboxes:     incusx.NewHostOverrideManager(adminConfig.Remote),
			Routes:        directRouteManager,
			RouteMetadata: directRouteManager,
			Trust:         incusx.NewRouteBrokerTrustMapper(adminConfig.Remote),
		}},
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

// NewAdminRootCommand builds the Sandcastle admin command tree with all admin
// subcommands promoted to the top level (no "admin" prefix).
func NewAdminRootCommand(config commandConfig) *cobra.Command {
	if config.name == "" {
		config.name = "sandcastle-admin"
	}
	if config.stdout == nil {
		config.stdout = io.Discard
	}
	if config.stdin == nil {
		config.stdin = strings.NewReader("")
	}
	if config.stderr == nil {
		config.stderr = io.Discard
	}
	if config.adminConfig.Remote == "" {
		config.adminConfig = scconfig.LoadAdmin()
	}
	if config.projectStore == nil {
		config.projectStore = project.MemoryStore{}
	}

	opts := &rootOptions{output: outputText}
	var jsonOutput bool
	root := &cobra.Command{
		Use:           config.name,
		Short:         "Manage Sandcastle shared infrastructure and user accounts",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if !jsonOutput {
				return nil
			}
			if cmd.Root().PersistentFlags().Changed("output") && opts.output != outputJSON {
				return fmt.Errorf("--json cannot be combined with --output %s", opts.output)
			}
			opts.output = outputJSON
			return nil
		},
	}
	root.PersistentFlags().Var(&opts.output, "output", "output format: text or json")
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "write JSON output")

	root.AddCommand(newVersionCommand(config, opts))
	root.AddCommand(newAdminProjectCommand(config, opts))
	root.AddCommand(newAdminUserCommand(config, opts))
	root.AddCommand(newAdminInfraCommand(config, opts))
	root.AddCommand(newAdminImageCommand(config, opts))
	root.AddCommand(newAdminTLDCommand(config, opts))
	root.AddCommand(newAdminRouteBrokerCommand(config))
	root.AddCommand(newConfigCommand(config, opts))

	return root
}
