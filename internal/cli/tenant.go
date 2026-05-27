package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/authapp"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
)

type tenantListRow struct {
	Tenant   string `json:"tenant"`
	Personal bool   `json:"personal"`
	Current  bool   `json:"current"`
}

type tenantListOutput struct {
	Tenants []tenantListRow `json:"tenants"`
}

type tenantSwitchOutput struct {
	Tenant     string `json:"tenant"`
	LocalOnly  bool   `json:"local_only,omitempty"`
	ConfigPath string `json:"config_path"`
	Message    string `json:"message"`
}

func newTenantCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "tenant",
		Short: "List and select accessible Sandcastle tenants",
	}
	command.AddCommand(newTenantListCommand(config, opts))
	command.AddCommand(newTenantSwitchCommand(config, opts))
	return command
}

func newTenantListCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List tenants accessible to the current user",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := tenantClient(config)
			if err != nil {
				return err
			}
			tenants, err := client.ListTenants(cmd.Context())
			if err != nil {
				return err
			}
			output := tenantListOutput{Tenants: tenantListRows(tenants, strings.TrimSpace(config.adminConfig.Tenant))}
			return writeOutput(config.stdout, opts.output, formatTenantAccessList(output), output)
		},
	}
}

func newTenantSwitchCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var localOnly bool
	command := &cobra.Command{
		Use:   "switch tenant",
		Short: "Select the local Current Tenant",
		Long:  "Select the local Current Tenant. By default this validates Tenant Access through the Auth App; use --local-only to update local config without online validation.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tenantName := strings.TrimSpace(args[0])
			if tenantName == "" {
				return fmt.Errorf("tenant is required")
			}
			if !localOnly {
				if err := validateTenantAccessForSwitch(cmd.Context(), config, tenantName); err != nil {
					return err
				}
			}
			cfgPath := scconfig.DefaultConfigPath()
			cfg, err := scconfig.LoadSandcastleConfig(cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			cfg.Tenant = tenantName
			if err := scconfig.SaveSandcastleConfig(cfgPath, cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			result := tenantSwitchOutput{
				Tenant:     tenantName,
				LocalOnly:  localOnly,
				ConfigPath: cfgPath,
				Message:    tenantSwitchHint(tenantName, localOnly),
			}
			return writeOutput(config.stdout, opts.output, formatTenantSwitch(result), result)
		},
	}
	command.Flags().BoolVar(&localOnly, "local-only", false, "update local Current Tenant config without Auth App Tenant Access validation")
	return command
}

func tenantClient(config commandConfig) (authTenantClient, error) {
	if config.authTenants != nil {
		return config.authTenants, nil
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

func validateTenantAccessForSwitch(ctx context.Context, config commandConfig, tenantName string) error {
	client, err := tenantClient(config)
	if err != nil {
		return err
	}
	tenants, err := client.ListTenants(ctx)
	if err != nil {
		return err
	}
	for _, candidate := range tenants {
		if candidate.Tenant == tenantName {
			return nil
		}
	}
	return fmt.Errorf("tenant %s is not accessible to the current user; use --local-only to update local config without validation", tenantName)
}

func tenantListRows(tenants []authapp.TenantAccessSummary, currentTenant string) []tenantListRow {
	rows := make([]tenantListRow, 0, len(tenants))
	for _, tenant := range tenants {
		rows = append(rows, tenantListRow{
			Tenant:   tenant.Tenant,
			Personal: tenant.Personal,
			Current:  tenant.Tenant == currentTenant,
		})
	}
	return rows
}

func formatTenantAccessList(output tenantListOutput) string {
	if len(output.Tenants) == 0 {
		return "No accessible tenants"
	}
	var builder strings.Builder
	builder.WriteString("Tenant\tPersonal\tCurrent\n")
	for _, tenant := range output.Tenants {
		builder.WriteString(tenant.Tenant)
		builder.WriteByte('\t')
		builder.WriteString(yesNo(tenant.Personal))
		builder.WriteByte('\t')
		builder.WriteString(yesNo(tenant.Current))
		builder.WriteByte('\n')
	}
	return strings.TrimRight(builder.String(), "\n")
}

func formatTenantSwitch(result tenantSwitchOutput) string {
	return fmt.Sprintf("Current Tenant set to %q in %s.\n%s", result.Tenant, result.ConfigPath, result.Message)
}

func tenantSwitchHint(tenantName string, localOnly bool) string {
	hint := fmt.Sprintf("Local setup is unchanged; run sc dns setup %s, sc trust install %s, and sc tailscale up %s if this tenant has not been set up locally.", tenantName, tenantName, tenantName)
	if localOnly {
		return "Skipped Auth App Tenant Access validation. " + hint
	}
	return hint
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

var _ authTenantClient = authapp.DeviceClient{}
