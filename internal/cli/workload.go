package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/authapp"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
)

func newWorkloadCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "workload",
		Short: "Manage workload identity for machines",
	}
	command.AddCommand(newWorkloadEnableCommand(config, opts))
	return command
}

func newWorkloadEnableCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var authHostname string
	var cloudIdentity string
	var maxPolls int
	var debugApprove bool
	command := &cobra.Command{
		Use:   "enable [project:]machine",
		Short: "Enable workload identity for a tenant machine",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := commandAuthHostname(config, authHostname)
			if host == "" {
				return fmt.Errorf("--auth-hostname is required (or run sc login again to remember the Auth Hostname)")
			}
			plan, err := machine.PlanCreate(cmd.Context(), config.adminConfig, tenantStoreWithSSHKeyMetadata(config.tenantStore), config.machineStore, machine.CreateRequest{
				Reference: args[0],
			})
			if err != nil {
				return err
			}
			result, err := enableWorkloadIdentityForPlan(cmd.Context(), config, plan, workloadEnableOptions{
				AuthHostname:  host,
				CloudIdentity: cloudIdentity,
				MaxPolls:      maxPolls,
				DebugApprove:  debugApprove,
			})
			if err != nil {
				return err
			}
			if err := applyWorkloadIdentityToMachine(cmd.Context(), config, plan, result); err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatWorkloadEnable(result), result)
		},
	}
	command.Flags().StringVar(&authHostname, "auth-hostname", "", "public Auth Hostname (overrides config auth.hostname)")
	command.Flags().StringVar(&cloudIdentity, "cloud-identity", "", "Cloud Identity Config name to inject, for example gcp")
	command.Flags().IntVar(&maxPolls, "max-polls", 300, "maximum device login poll attempts")
	command.Flags().BoolVar(&debugApprove, "debug-approve", false, "auto-approve via /debug/device/approve (requires server --debug-device-user)")
	return command
}

type workloadEnableOptions struct {
	AuthHostname  string
	CloudIdentity string
	MaxPolls      int
	DebugApprove  bool
}

func enableWorkloadIdentityForPlan(ctx context.Context, config commandConfig, plan machine.CreatePlan, options workloadEnableOptions) (authapp.WorkloadEnableResult, error) {
	host := commandAuthHostname(config, options.AuthHostname)
	if host == "" {
		return authapp.WorkloadEnableResult{}, fmt.Errorf("--auth-hostname is required (or run sc login again to remember the Auth Hostname)")
	}
	client := config.authWorkload
	if client == nil {
		client = authapp.DeviceClient{BaseURL: host, AuthToken: strings.TrimSpace(config.adminConfig.AuthToken)}
	}
	deviceCode := ""
	if strings.TrimSpace(config.adminConfig.AuthToken) == "" {
		device, err := approveWorkloadDevice(ctx, config, client, options.MaxPolls, options.DebugApprove)
		if err != nil {
			return authapp.WorkloadEnableResult{}, err
		}
		deviceCode = device.DeviceCode
	}
	return client.EnableWorkload(ctx, authapp.WorkloadEnableRequest{
		DeviceCode:          deviceCode,
		Tenant:              plan.Tenant.Tenant,
		Project:             plan.Project,
		Machine:             plan.Name,
		CloudIdentityConfig: strings.TrimSpace(options.CloudIdentity),
	})
}

func applyWorkloadIdentityToMachine(ctx context.Context, config commandConfig, plan machine.CreatePlan, result authapp.WorkloadEnableResult) error {
	workloadRequest := &machine.WorkloadIdentityRequest{
		TokenEndpoint: result.TokenEndpoint,
		RuntimeSecret: result.RuntimeSecret,
		Tenant:        result.Tenant,
		Project:       result.Project,
		Machine:       result.Machine,
	}
	if strings.TrimSpace(result.GCPAudience) != "" {
		workloadRequest.GCP = &machine.GCPWorkloadIdentityConfig{
			Audience:                       result.GCPAudience,
			SubjectTokenType:               result.GCPSubjectTokenType,
			ServiceAccountImpersonationURL: result.GCPServiceAccountImpersonationURL,
		}
	}
	files, err := machine.WorkloadIdentityFiles(workloadRequest)
	if err != nil {
		return fmt.Errorf("build workload identity files: %w", err)
	}
	plan.WorkloadFiles = files
	plan.CertificateFiles = []machine.File{}
	if config.machineCreator == nil {
		return fmt.Errorf("machine creation executor is not configured")
	}
	return config.machineCreator.CreateMachine(ctx, plan)
}

type workloadApprovedDevice struct {
	DeviceCode string
	UserKey    string
}

func approveWorkloadDevice(ctx context.Context, config commandConfig, client authWorkloadClient, maxPolls int, debugApprove bool) (workloadApprovedDevice, error) {
	start, err := client.Start(ctx)
	if err != nil {
		return workloadApprovedDevice{}, err
	}
	fmt.Fprintf(config.stdout, "Open %s and enter code %s.\n", start.VerificationURI, start.UserCode)
	if debugApprove {
		if err := client.DebugApprove(ctx, start.UserCode); err != nil {
			return workloadApprovedDevice{}, err
		}
	}
	interval := start.Interval
	if interval <= 0 {
		interval = 2
	}
	if maxPolls <= 0 {
		maxPolls = 300
	}
	for i := 0; i < maxPolls; i++ {
		result, err := client.Poll(ctx, start.DeviceCode, authapp.DevicePollRequest{})
		if err != nil {
			return workloadApprovedDevice{}, err
		}
		switch result.Status {
		case authapp.DeviceStatusApproved:
			if result.UserKey != "" {
				fmt.Fprintf(config.stdout, "Approved as %s.\n", result.UserKey)
			} else {
				fmt.Fprintln(config.stdout, "Approved.")
			}
			return workloadApprovedDevice{DeviceCode: start.DeviceCode, UserKey: result.UserKey}, nil
		case authapp.DeviceStatusPending:
			select {
			case <-ctx.Done():
				return workloadApprovedDevice{}, ctx.Err()
			case <-time.After(time.Duration(interval) * time.Second):
			}
		case authapp.DeviceStatusExpired:
			return workloadApprovedDevice{}, fmt.Errorf("device login expired")
		case authapp.DeviceStatusDenied:
			return workloadApprovedDevice{}, fmt.Errorf("device login denied")
		default:
			return workloadApprovedDevice{}, fmt.Errorf("unknown device login status %q", result.Status)
		}
	}
	return workloadApprovedDevice{}, fmt.Errorf("device login polling timed out")
}

func formatWorkloadEnable(result authapp.WorkloadEnableResult) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Workload identity enabled for %s/%s/%s\n", result.Tenant, result.Project, result.Machine)
	fmt.Fprintf(&builder, "Token endpoint: %s\n", result.TokenEndpoint)
	fmt.Fprintf(&builder, "OIDC issuer:    %s\n", result.Issuer)
	if result.CloudIdentityConfig != "" {
		fmt.Fprintf(&builder, "Cloud identity: %s\n", result.CloudIdentityConfig)
	}
	if result.GCPAudience != "" {
		fmt.Fprintf(&builder, "GCP audience:   %s\n", result.GCPAudience)
	}
	fmt.Fprintf(&builder, "Helper:         %s", machine.WorkloadTokenHelperPath)
	return builder.String()
}
