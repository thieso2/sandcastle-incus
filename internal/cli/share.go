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
	command.AddCommand(newShareOffersCommand(config, opts))
	command.AddCommand(newShareAcceptCommand(config, opts))
	command.AddCommand(newShareDeclineCommand(config, opts))
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
	var inbound bool
	var offers bool
	command := &cobra.Command{
		Use:   "list",
		Short: "List Tenant Storage Shares for the current tenant",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := shareClient(config)
			if err != nil {
				return err
			}
			var shares []meta.TenantStorageShare
			switch {
			case offers:
				shares, err = client.ListShareOffers(cmd.Context(), strings.TrimSpace(config.adminConfig.Tenant))
			case inbound:
				shares, err = client.ListInboundShares(cmd.Context(), strings.TrimSpace(config.adminConfig.Tenant))
			case outbound:
				shares, err = client.ListShares(cmd.Context(), strings.TrimSpace(config.adminConfig.Tenant))
			default:
				shares, err = client.ListShares(cmd.Context(), strings.TrimSpace(config.adminConfig.Tenant))
				if err == nil {
					var inboundShares []meta.TenantStorageShare
					inboundShares, err = client.ListInboundShares(cmd.Context(), strings.TrimSpace(config.adminConfig.Tenant))
					shares = append(shares, inboundShares...)
				}
			}
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatShares(shares), map[string]any{"shares": shares})
		},
	}
	command.Flags().BoolVar(&outbound, "outbound", false, "show shares offered by the current tenant")
	command.Flags().BoolVar(&inbound, "inbound", false, "show accepted or declined shares offered to the current tenant")
	command.Flags().BoolVar(&offers, "offers", false, "show pending shares offered to the current tenant")
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

func newShareOffersCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "offers",
		Short: "List pending Tenant Storage Share offers",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := shareClient(config)
			if err != nil {
				return err
			}
			shares, err := client.ListShareOffers(cmd.Context(), strings.TrimSpace(config.adminConfig.Tenant))
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatShares(shares), map[string]any{"shares": shares})
		},
	}
	return command
}

func newShareAcceptCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return newShareRecipientCommand(config, opts, "accept", "Accept a Tenant Storage Share offer")
}

func newShareDeclineCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return newShareRecipientCommand(config, opts, "decline", "Decline a Tenant Storage Share offer")
}

func newShareRecipientCommand(config commandConfig, opts *rootOptions, action string, short string) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   action + " source-tenant/source-project/share-name",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sourceTenant, sourceProject, shareName, err := parseInboundShareRef(args[0])
			if err != nil {
				return err
			}
			client, err := shareClient(config)
			if err != nil {
				return err
			}
			request := authapp.ShareRecipientRequest{
				Tenant:        strings.TrimSpace(config.adminConfig.Tenant),
				SourceTenant:  sourceTenant,
				SourceProject: sourceProject,
				Name:          shareName,
				DryRun:        dryRun,
			}
			var result meta.TenantStorageShare
			if action == "accept" {
				result, err = client.AcceptShare(cmd.Context(), request)
			} else {
				result, err = client.DeclineShare(cmd.Context(), request)
			}
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatShare(result), result)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the share plan without mutating metadata")
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

func parseInboundShareRef(value string) (string, string, string, error) {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("share reference must be source-tenant/source-project/share-name")
	}
	if _, _, err := share.ParseStatusRef(parts[1] + "/" + parts[2]); err != nil {
		return "", "", "", err
	}
	return parts[0], parts[1], parts[2], nil
}

var _ authShareClient = authapp.DeviceClient{}
