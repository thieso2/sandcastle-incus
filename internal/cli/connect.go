package cli

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
)

func newConnectCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var cloudIdentity string
	var authHostname string
	var maxPolls int
	var debugApprove bool
	command := &cobra.Command{
		Use:     "connect [project:]machine [-- command...]",
		Aliases: []string{"c"},
		Short:   "Connect to a Sandcastle machine",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reference := args[0]
			command := args[1:]
			cache := incusx.NewConnectCache(config.adminConfig.Remote)
			plan, fromCache, err := planConnectCached(cmd.Context(), config, cache, reference, command)
			if err != nil {
				if shouldCreateOnConnectFailure(err) {
					return createAndConnect(cmd, config, reference, command, workloadEnableOptions{
						AuthHostname:  authHostname,
						CloudIdentity: cloudIdentity,
						MaxPolls:      maxPolls,
						DebugApprove:  debugApprove,
					})
				}
				return err
			}
			if config.machineConnector == nil {
				return fmt.Errorf("machine connect executor is not configured")
			}
			// If the plan came from cache and the SSH host is unreachable, don't let SSH
			// hang — invalidate the cache immediately and retry with a fresh Incus lookup.
			if fromCache && !probeSSHPort(plan.SSHHost, 2*time.Second) {
				return retryConnectFresh(cmd, config, cache, reference, command, plan, workloadEnableOptions{
					AuthHostname:  authHostname,
					CloudIdentity: cloudIdentity,
					MaxPolls:      maxPolls,
					DebugApprove:  debugApprove,
				})
			}
			if strings.TrimSpace(cloudIdentity) != "" {
				if err := enableWorkloadIdentityForConnect(cmd, config, reference, workloadEnableOptions{
					AuthHostname:  authHostname,
					CloudIdentity: cloudIdentity,
					MaxPolls:      maxPolls,
					DebugApprove:  debugApprove,
				}); err != nil {
					return err
				}
			} else {
				verboseCLI(config, "workload identity: not requested before connect %s; gcloud works only if this machine already has workload files", reference)
			}
			if err := refreshKnownHostsForPrivateIPConnect(cmd.Context(), config, plan); err != nil {
				return err
			}
			if err := config.machineConnector.ConnectMachine(cmd.Context(), plan, machine.ConnectSession{
				Stdin:  config.stdin,
				Stdout: config.stdout,
				Stderr: config.stderr,
			}); err != nil {
				if shouldCreateOnConnectFailure(err) {
					return createAndConnect(cmd, config, reference, command, workloadEnableOptions{
						AuthHostname:  authHostname,
						CloudIdentity: cloudIdentity,
						MaxPolls:      maxPolls,
						DebugApprove:  debugApprove,
					})
				}
				// If the plan came from the cache, stale metadata (changed IP or host key)
				// may have caused the failure. Invalidate both caches and retry once with
				// a fresh Incus lookup and keyscan.
				if fromCache {
					return retryConnectFresh(cmd, config, cache, reference, command, plan, workloadEnableOptions{
						AuthHostname:  authHostname,
						CloudIdentity: cloudIdentity,
						MaxPolls:      maxPolls,
						DebugApprove:  debugApprove,
					})
				}
				return err
			}
			return nil
		},
	}
	command.Flags().StringVar(&cloudIdentity, "cloud-identity", "", "Cloud Identity Config name to inject before connecting, for example gcp")
	command.Flags().StringVar(&authHostname, "auth-hostname", "", "public Auth Hostname (overrides config auth.hostname)")
	command.Flags().IntVar(&maxPolls, "max-polls", 300, "maximum device login poll attempts when enabling workload identity")
	command.Flags().BoolVar(&debugApprove, "debug-approve", false, "auto-approve workload identity device login (requires server --debug-device-user)")
	return command
}

// planConnectCached resolves a ConnectPlan using the local cache when available.
// fromCache is true when the plan was served from the cache without an Incus API call.
func planConnectCached(ctx context.Context, cfg commandConfig, cache incusx.ConnectCache, reference string, command []string) (plan machine.ConnectPlan, fromCache bool, err error) {
	if cached, ok := lookupCachedPlan(cache, cfg.adminConfig.Tenant, cfg.adminConfig.Project, reference); ok {
		return applyConnectCommand(cached, command), true, nil
	}
	plan, err = machine.PlanConnect(ctx, cfg.adminConfig, cfg.tenantStore, cfg.machineStore, machine.ConnectRequest{
		Reference: reference,
		Command:   command,
	})
	if err != nil {
		return machine.ConnectPlan{}, false, err
	}
	if plan.Managed {
		if canonKey := connectPlanCacheKey(plan.Tenant.Tenant, plan.Project, plan.Name); canonKey != "" {
			cache.StorePlan(canonKey, plan)
		}
	}
	return plan, false, nil
}

// lookupCachedPlan tries the exact project key first, then falls back to a name-only
// search across all projects when no default project is configured.
func lookupCachedPlan(cache incusx.ConnectCache, tenant, project, reference string) (machine.ConnectPlan, bool) {
	if strings.ContainsAny(reference, "./: ") {
		return machine.ConnectPlan{}, false // FQDN or explicit project ref — skip cache
	}
	if key := connectPlanCacheKey(tenant, project, reference); key != "" {
		return cache.LookupPlan(key)
	}
	// No default project: search by name, accept only an unambiguous match.
	if strings.TrimSpace(tenant) != "" && strings.TrimSpace(reference) != "" {
		return cache.LookupPlanByName(tenant, reference)
	}
	return machine.ConnectPlan{}, false
}

// retryConnectFresh invalidates stale cache entries from oldPlan, re-resolves the plan
// from Incus, re-scans the SSH host key, and retries the connection once.
// This handles machine recreation (new IP or new host key) transparently.
func retryConnectFresh(cmd *cobra.Command, cfg commandConfig, cache incusx.ConnectCache, reference string, command []string, oldPlan machine.ConnectPlan, workloadOptions workloadEnableOptions) error {
	if key := connectPlanCacheKey(cfg.adminConfig.Tenant, cfg.adminConfig.Project, reference); key != "" {
		cache.InvalidatePlan(key)
	}
	if canonKey := connectPlanCacheKey(oldPlan.Tenant.Tenant, oldPlan.Project, oldPlan.Name); canonKey != "" {
		cache.InvalidatePlan(canonKey)
	}
	cache.InvalidateKeyscan(oldPlan.Hostname)

	plan, err := machine.PlanConnect(cmd.Context(), cfg.adminConfig, cfg.tenantStore, cfg.machineStore, machine.ConnectRequest{
		Reference: reference,
		Command:   command,
	})
	if err != nil {
		if shouldCreateOnConnectFailure(err) {
			return createAndConnect(cmd, cfg, reference, command, workloadOptions)
		}
		return err
	}
	if plan.Managed {
		if key := connectPlanCacheKey(plan.Tenant.Tenant, plan.Project, plan.Name); key != "" {
			cache.StorePlan(key, plan)
		}
	}
	if strings.TrimSpace(workloadOptions.CloudIdentity) != "" {
		if err := enableWorkloadIdentityForConnect(cmd, cfg, reference, workloadOptions); err != nil {
			return err
		}
	}
	if err := refreshKnownHostsForPrivateIPConnect(cmd.Context(), cfg, plan); err != nil {
		return err
	}
	return cfg.machineConnector.ConnectMachine(cmd.Context(), plan, machine.ConnectSession{
		Stdin:  cfg.stdin,
		Stdout: cfg.stdout,
		Stderr: cfg.stderr,
	})
}

// connectPlanCacheKey returns a cache key for simple project/machine references.
// Returns "" for FQDNs, multi-project notation, or when the default project is unknown.
func connectPlanCacheKey(tenant, project, name string) string {
	tenant = strings.TrimSpace(tenant)
	project = strings.TrimSpace(project)
	name = strings.TrimSpace(name)
	if tenant == "" || project == "" || name == "" {
		return ""
	}
	if strings.ContainsAny(name, "./: ") {
		return ""
	}
	return tenant + ":" + project + "/" + name
}

// applyConnectCommand sets the command and interactive flag on a cached plan.
func applyConnectCommand(plan machine.ConnectPlan, command []string) machine.ConnectPlan {
	if len(command) == 0 {
		plan.Command = []string{"/bin/bash", "-l"}
		plan.Interactive = true
	} else {
		plan.Command = command
		plan.Interactive = false
	}
	return plan
}

func shouldCreateOnConnectFailure(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "not found") && !strings.Contains(message, "project")
}

func refreshKnownHostsForPrivateIPConnect(ctx context.Context, config commandConfig, plan machine.ConnectPlan) error {
	if !plan.Managed || strings.TrimSpace(plan.PrivateIP) == "" || plan.SSHHost != plan.PrivateIP {
		return nil
	}
	return refreshMachineKnownHosts(ctx, config, machine.CreatePlan{
		Tenant:       plan.Tenant,
		Project:      plan.Project,
		Name:         plan.Name,
		InstanceName: plan.InstanceName,
		Hostname:     plan.Hostname,
		PrivateIP:    plan.PrivateIP,
	})
}

func createAndConnect(cmd *cobra.Command, config commandConfig, reference string, command []string, workloadOptions workloadEnableOptions) error {
	if config.stderr != nil {
		fmt.Fprintf(config.stderr, "Machine %s not found; creating it before connecting.\n", reference)
	}
	if err := ensureTenantUnixUserForMachineCreate(cmd.Context(), config); err != nil {
		return err
	}
	createPlan, err := machine.PlanCreate(cmd.Context(), config.adminConfig, tenantStoreWithSSHKeyMetadata(config.tenantStore), config.machineStore, machine.CreateRequest{
		Reference: reference,
	})
	if err != nil {
		return err
	}
	if config.machineCreator == nil {
		return fmt.Errorf("machine creation executor is not configured")
	}
	if strings.TrimSpace(workloadOptions.CloudIdentity) == "" {
		workloadOptions.CloudIdentity = effectiveProjectCloudIdentity(config, createPlan.Tenant, createPlan.Project, "")
	}
	if strings.TrimSpace(workloadOptions.CloudIdentity) == "" {
		verboseCLI(config, "workload identity: not requested for auto-create %s; gcloud credentials will not be configured (use --cloud-identity gcp)", createPlan.Reference)
	}
	if err := config.machineCreator.CreateMachine(cmd.Context(), createPlan); err != nil {
		return err
	}
	if strings.TrimSpace(workloadOptions.CloudIdentity) != "" {
		result, err := enableWorkloadIdentityForPlan(cmd.Context(), config, createPlan, workloadOptions)
		if err != nil {
			return err
		}
		if err := applyWorkloadIdentityToMachine(cmd.Context(), config, createPlan, result); err != nil {
			return err
		}
	}
	if err := refreshTenantDNS(cmd.Context(), config, createPlan.Tenant); err != nil {
		return err
	}
	if err := refreshMachineKnownHosts(cmd.Context(), config, createPlan); err != nil {
		return err
	}
	if config.machineConnector == nil {
		return fmt.Errorf("machine connect executor is not configured")
	}
	connectPlan, err := connectPlanFromCreatePlan(createPlan, command)
	if err != nil {
		return err
	}
	cache := incusx.NewConnectCache(config.adminConfig.Remote)
	if connectPlan.Managed {
		if key := connectPlanCacheKey(connectPlan.Tenant.Tenant, connectPlan.Project, connectPlan.Name); key != "" {
			cache.StorePlan(key, connectPlan)
		}
	}
	return config.machineConnector.ConnectMachine(cmd.Context(), connectPlan, machine.ConnectSession{
		Stdin:  config.stdin,
		Stdout: config.stdout,
		Stderr: config.stderr,
	})
}

func enableWorkloadIdentityForConnect(cmd *cobra.Command, config commandConfig, reference string, workloadOptions workloadEnableOptions) error {
	plan, err := machine.PlanCreate(cmd.Context(), config.adminConfig, tenantStoreWithSSHKeyMetadata(config.tenantStore), config.machineStore, machine.CreateRequest{
		Reference: reference,
	})
	if err != nil {
		return err
	}
	result, err := enableWorkloadIdentityForPlan(cmd.Context(), config, plan, workloadOptions)
	if err != nil {
		return err
	}
	return applyWorkloadIdentityToMachine(cmd.Context(), config, plan, result)
}

// probeSSHPort returns true if TCP port 22 on host is reachable within the given timeout.
// Used to detect stale cached IPs before handing off to SSH (which would hang on unreachable hosts).
func probeSSHPort(host string, timeout time.Duration) bool {
	if strings.TrimSpace(host) == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "22"), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func connectPlanFromCreatePlan(plan machine.CreatePlan, command []string) (machine.ConnectPlan, error) {
	interactive := false
	if len(command) == 0 {
		command = []string{"/bin/bash", "-l"}
		interactive = true
	}
	if len(command) == 0 || command[0] == "" {
		return machine.ConnectPlan{}, fmt.Errorf("connect command is required")
	}
	return machine.ConnectPlan{
		Reference:    plan.Reference,
		Tenant:       plan.Tenant,
		Project:      plan.Project,
		Name:         plan.Name,
		InstanceName: plan.InstanceName,
		Hostname:     plan.Hostname,
		PrivateIP:    plan.PrivateIP,
		SSHHost:      plan.PrivateIP,
		HostKeyAlias: plan.Hostname,
		Command:      command,
		LinuxUser:    plan.LinuxUser,
		UserID:       machine.DefaultLinuxUID,
		GroupID:      machine.DefaultLinuxGID,
		WorkingDir:   "/workspace",
		Interactive:  interactive,
		Managed:      true,
	}, nil
}
