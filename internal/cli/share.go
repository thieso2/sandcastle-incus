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
	command.AddCommand(newShareRevokeCommand(config, opts))
	command.AddCommand(newShareDeleteCommand(config, opts))
	command.AddCommand(newShareReconcileCommand(config, opts))
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
			result, err := client.CreateShare(cmd.Context(), authapp.ShareCreateRequest{
				SourceTenant: strings.TrimSpace(config.adminConfig.Tenant),
				Source:       args[0],
				Recipients:   recipients,
				Name:         name,
				DryRun:       dryRun,
			})
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatShareResult(result), result)
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
					shares = append(shares, acceptedSharesForTenant(inboundShares, strings.TrimSpace(config.adminConfig.Tenant))...)
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

func acceptedSharesForTenant(shares []meta.TenantStorageShare, tenantName string) []meta.TenantStorageShare {
	var output []meta.TenantStorageShare
	for _, storageShare := range shares {
		for _, recipient := range storageShare.Recipients {
			if recipient.Tenant == tenantName && recipient.State == share.RecipientStateAccepted {
				output = append(output, storageShare)
				break
			}
		}
	}
	return output
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

func newShareRevokeCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var recipientTenant string
	var dryRun bool
	command := &cobra.Command{
		Use:   "revoke project/share-name --tenant tenant",
		Short: "Revoke one recipient from an outbound Tenant Storage Share",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, name, err := share.ParseStatusRef(args[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(recipientTenant) == "" {
				return fmt.Errorf("--tenant is required")
			}
			client, err := shareClient(config)
			if err != nil {
				return err
			}
			result, err := client.RevokeShare(cmd.Context(), authapp.ShareRevokeRequest{
				Tenant:          strings.TrimSpace(config.adminConfig.Tenant),
				Project:         project,
				Name:            name,
				RecipientTenant: recipientTenant,
				DryRun:          dryRun,
			})
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatShareResult(result), result)
		},
	}
	command.Flags().StringVar(&recipientTenant, "tenant", "", "recipient tenant to revoke")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the share revocation without mutating metadata or machines")
	return command
}

func newShareDeleteCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var yes bool
	var dryRun bool
	command := &cobra.Command{
		Use:   "delete project/share-name",
		Short: "Delete an outbound Tenant Storage Share",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, name, err := share.ParseStatusRef(args[0])
			if err != nil {
				return err
			}
			if !yes && !dryRun {
				confirmed, err := confirmMissingYes(config, "Delete Tenant Storage Share "+args[0]+"?", "refusing to delete share without --yes")
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("delete canceled")
				}
			}
			client, err := shareClient(config)
			if err != nil {
				return err
			}
			result, err := client.DeleteShare(cmd.Context(), authapp.ShareDeleteRequest{
				Tenant:  strings.TrimSpace(config.adminConfig.Tenant),
				Project: project,
				Name:    name,
				DryRun:  dryRun,
			})
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatShareResult(result), result)
		},
	}
	command.Flags().BoolVar(&yes, "yes", false, "confirm share deletion")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the share deletion without mutating metadata or machines")
	return command
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
			var result share.Result
			if action == "accept" {
				result, err = client.AcceptShare(cmd.Context(), request)
			} else {
				result, err = client.DeclineShare(cmd.Context(), request)
			}
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatShareResult(result), result)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the share plan without mutating metadata")
	return command
}

func newShareReconcileCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var tenantName string
	var dryRun bool
	command := &cobra.Command{
		Use:   "reconcile",
		Short: "Reconcile accepted Tenant Storage Shares onto running machines",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := shareClient(config)
			if err != nil {
				return err
			}
			if strings.TrimSpace(tenantName) == "" {
				tenantName = strings.TrimSpace(config.adminConfig.Tenant)
			}
			result, err := client.ReconcileShares(cmd.Context(), authapp.ShareReconcileRequest{
				Tenant: tenantName,
				DryRun: dryRun,
			})
			if err != nil {
				return err
			}
			if err := writeOutput(config.stdout, opts.output, formatReconcileResult(result), result); err != nil {
				return err
			}
			if result.HasFailures() {
				return fmt.Errorf("share reconciliation left one or more machines unreconciled")
			}
			return nil
		},
	}
	command.Flags().StringVar(&tenantName, "tenant", "", "tenant to reconcile; defaults to the current tenant")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "show planned machine device changes without mutating Incus")
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

func formatShareResult(result share.Result) string {
	text := formatShare(result.Share)
	if result.Reconcile != nil {
		text += formatReconcileResult(*result.Reconcile)
	}
	for _, reconcile := range result.Reconciles {
		text += formatReconcileResult(reconcile)
	}
	return text
}

func formatReconcileResult(result share.ReconcileResult) string {
	if len(result.Machines) == 0 {
		return "No machines to reconcile\n"
	}
	var builder strings.Builder
	if result.Tenant != "" {
		builder.WriteString("Reconcile " + result.Tenant + ":\n")
	} else {
		builder.WriteString("Reconcile:\n")
	}
	for _, machine := range result.Machines {
		status := machine.Status
		if status == "" {
			status = "ok"
		}
		line := fmt.Sprintf("- %s/%s: %s", machine.Project, machine.Machine, status)
		if machine.Changed {
			line += " changed"
		}
		if machine.Error != "" {
			line += ": " + machine.Error
		}
		builder.WriteString(line + "\n")
	}
	return builder.String()
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
