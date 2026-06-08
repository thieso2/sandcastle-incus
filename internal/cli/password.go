package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

type passwordSetPayload struct {
	Tenant       string `json:"tenant"`
	IncusProject string `json:"incusProject"`
}

func newPasswordCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "password",
		Short: "Manage the current tenant login and Samba password",
	}
	command.AddCommand(newPasswordSetCommand(config, opts))
	return command
}

func newPasswordSetCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "set [password]",
		Short: "Set the Unix login and Samba password for the tenant's machines",
		Long: "Set the password for the tenant owner's Linux user on every machine in the\n" +
			"current tenant. The same secret becomes both the Unix login password and the\n" +
			"Samba password, so it unlocks SSH/console login and the SMB [home] and\n" +
			"[workspace] shares.\n\n" +
			"Pass the password as an argument, or omit it to read one line from stdin.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			password, err := passwordFromArgs(args, config.stdin)
			if err != nil {
				return err
			}
			summary, err := currentTenantSummary(cmd.Context(), config)
			if err != nil {
				return err
			}
			payload := passwordSetPayload{
				Tenant:       summary.Tenant,
				IncusProject: summary.IncusName,
			}
			if !dryRun {
				if config.passwordReconciler == nil {
					return fmt.Errorf("machine password reconciler is not configured")
				}
				if err := config.passwordReconciler.ReconcileTenantPassword(cmd.Context(), summary, password); err != nil {
					return err
				}
			}
			return writeOutput(config.stdout, opts.output, formatPasswordSet(payload), payload)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the password update without mutating machines")
	return command
}

// passwordFromArgs takes the password from the positional argument, or reads a
// single line from stdin when no argument is given (so it can be piped without
// landing in shell history). Trailing newlines are stripped; the password is
// otherwise used verbatim.
func passwordFromArgs(args []string, stdin io.Reader) (string, error) {
	if len(args) > 0 {
		if args[0] == "" {
			return "", fmt.Errorf("password is empty")
		}
		return args[0], nil
	}
	if stdin == nil {
		return "", fmt.Errorf("password is required; pass it as an argument or on stdin")
	}
	reader := bufio.NewReader(stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read password from stdin: %w", err)
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		return "", fmt.Errorf("password is required; pass it as an argument or on stdin")
	}
	return password, nil
}

func formatPasswordSet(payload passwordSetPayload) string {
	return fmt.Sprintf("Password set for tenant %s (Unix login + Samba)", payload.Tenant)
}
