package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/authapp"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/share"
)

func newShareCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "share",
		Short: "Manage Tenant Storage Shares",
	}
	command.AddCommand(newShareCreateCommand(config, opts))
	command.AddCommand(newShareListCommand(config, opts))
	command.AddCommand(newShareStatusCommand(config, opts))
	return command
}

func newShareCreateCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var recipients []string
	var name string
	var dryRun bool
	command := &cobra.Command{
		Use:   "create project:/workspace/dir --to tenant",
		Short: "Create an outbound Tenant Storage Share offer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := shareClient(config)
			if err != nil {
				return err
			}
			created, err := client.CreateShare(cmd.Context(), authapp.ShareCreateRequest{
				SourceTenant: strings.TrimSpace(config.adminConfig.Tenant),
				Source:       args[0],
				Recipients:   recipients,
				Name:         name,
				DryRun:       dryRun,
			})
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatShare(created), created)
		},
	}
	command.Flags().StringArrayVar(&recipients, "to", nil, "recipient tenant to offer the share to (repeatable)")
	command.Flags().StringVar(&name, "name", "", "Share Name; defaults to the source directory basename")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the share plan without mutating metadata")
	return command
}

func newShareListCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var outbound bool
	command := &cobra.Command{
		Use:   "list",
		Short: "List Tenant Storage Shares for the current tenant",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !outbound {
				outbound = true
			}
			client, err := shareClient(config)
			if err != nil {
				return err
			}
			shares, err := client.ListShares(cmd.Context(), strings.TrimSpace(config.adminConfig.Tenant))
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatShares(shares), map[string]any{"shares": shares})
		},
	}
	command.Flags().BoolVar(&outbound, "outbound", false, "show shares offered by the current tenant")
	return command
}

func newShareStatusCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "status project/share-name",
		Short: "Show an outbound Tenant Storage Share",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, name, err := share.ParseStatusRef(args[0])
			if err != nil {
				return err
			}
			client, err := shareClient(config)
			if err != nil {
				return err
			}
			found, err := client.GetShare(cmd.Context(), strings.TrimSpace(config.adminConfig.Tenant), project, name)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatShare(found), found)
		},
	}
	return command
}

func shareClient(config commandConfig) (authShareClient, error) {
	if config.authShares != nil {
		return config.authShares, nil
	}
	if strings.TrimSpace(config.adminConfig.AuthToken) == "" {
		return nil, fmt.Errorf("CLI Auth Token is required; run sc login")
	}
	baseURL := commandAuthHostname(config, "")
	if baseURL == "" {
		return nil, fmt.Errorf("Auth Hostname is required; run sc login")
	}
	return authapp.DeviceClient{BaseURL: baseURL, AuthToken: strings.TrimSpace(config.adminConfig.AuthToken)}, nil
}

func formatShares(shares []meta.TenantStorageShare) string {
	if len(shares) == 0 {
		return "No Tenant Storage Shares\n"
	}
	var builder strings.Builder
	for _, share := range shares {
		builder.WriteString(formatShare(share))
	}
	return builder.String()
}

func formatShare(value meta.TenantStorageShare) string {
	var recipients []string
	for _, recipient := range value.Recipients {
		label := recipient.Tenant
		if recipient.State != "" {
			label += " (" + recipient.State + ")"
		}
		recipients = append(recipients, label)
	}
	return fmt.Sprintf(
		"Share: %s/%s\nSource: %s:/workspace/%s\nRecipients: %s\nAvailability: %s\n",
		value.SourceProject,
		value.Name,
		value.SourceProject,
		value.SourceDir,
		strings.Join(recipients, ", "),
		value.Availability,
	)
}

var _ authShareClient = authapp.DeviceClient{}
