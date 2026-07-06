package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type incusRunner func(context.Context, []string, []string, io.Reader, io.Writer, io.Writer) error

func newIncusCommand(config commandConfig, _ *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:                "incus [args...]",
		Short:              "Run incus with the active Sandcastle remote's Incus config",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			incusDir := resolveIncusDir(config.adminConfig.Remote)
			if incusDir == "" {
				return fmt.Errorf("no Sandcastle-managed Incus config found for remote %q; add one with: sc remote add", config.adminConfig.Remote)
			}
			runner := config.incusRunner
			if runner == nil {
				runner = runIncusCLI
			}
			projectName, err := incusTenantProject(config.adminConfig)
			if err != nil {
				return err
			}
			// v2 tenants use per-project Incus projects (sc2-<tenant>-<project>),
			// not the v1 sc-<tenant> project the name derivation above assumes.
			if summary, isV2 := v2TenantSummary(cmd.Context(), config); isV2 {
				projectName = summary.V2IncusProjectName(config.adminConfig.Project)
			}
			env := append(os.Environ(), "INCUS_CONF="+incusDir)
			envOverrides := []string{"INCUS_CONF=" + incusDir}
			if projectName != "" {
				env = append(env, "INCUS_PROJECT="+projectName)
				envOverrides = append(envOverrides, "INCUS_PROJECT="+projectName)
			}
			if os.Getenv("VERBOSE") == "1" {
				fmt.Fprintf(config.stderr, "[verbose] sc incus env: %s\n", strings.Join(envOverrides, " "))
				fmt.Fprintf(config.stderr, "[verbose] sc incus command: %s\n", shellCommandLine(append([]string{"incus"}, args...)))
			}
			return runner(cmd.Context(), args, env, config.stdin, config.stdout, config.stderr)
		},
	}
}

func newIncusNativeCommand(config commandConfig, _ *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:                "incus-native [args...]",
		Short:              "Run incus scoped to the tenant's native (freeform) Incus project",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// v2 has no separate native project — freeform IS the model, so
			// this scopes to the tenant's app project like plain `sc incus`.
			return runIncusWithProject(cmd, config, args, naming.TenantNativeIncusProjectName,
				func(summary tenant.Summary) string { return summary.V2IncusProjectName(config.adminConfig.Project) })
		},
	}
}

func newIncusInfraCommand(config commandConfig, _ *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:                "incus-infra [args...]",
		Short:              "Run incus scoped to the tenant's infra (sidecars) Incus project",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIncusWithProject(cmd, config, args, naming.TenantInfraIncusProjectName,
				func(summary tenant.Summary) string { return summary.InfraProject })
		},
	}
}

func runIncusWithProject(cmd *cobra.Command, config commandConfig, args []string, projectNameFn func(string) string, v2ProjectFn func(tenant.Summary) string) error {
	incusDir := resolveIncusDir(config.adminConfig.Remote)
	if incusDir == "" {
		return fmt.Errorf("no Sandcastle-managed Incus config found for remote %q; add one with: sc remote add", config.adminConfig.Remote)
	}
	runner := config.incusRunner
	if runner == nil {
		runner = runIncusCLI
	}
	mainProject, err := incusTenantProject(config.adminConfig)
	if err != nil {
		return err
	}
	envOverrides := []string{"INCUS_CONF=" + incusDir}
	env := append(os.Environ(), "INCUS_CONF="+incusDir)
	if summary, isV2 := v2TenantSummary(cmd.Context(), config); isV2 {
		projectName := v2ProjectFn(summary)
		env = append(env, "INCUS_PROJECT="+projectName)
		envOverrides = append(envOverrides, "INCUS_PROJECT="+projectName)
	} else if mainProject != "" {
		projectName := projectNameFn(mainProject)
		env = append(env, "INCUS_PROJECT="+projectName)
		envOverrides = append(envOverrides, "INCUS_PROJECT="+projectName)
	}
	if os.Getenv("VERBOSE") == "1" {
		fmt.Fprintf(config.stderr, "[verbose] sc incus env: %s\n", strings.Join(envOverrides, " "))
		fmt.Fprintf(config.stderr, "[verbose] sc incus command: %s\n", shellCommandLine(append([]string{"incus"}, args...)))
	}
	return runner(cmd.Context(), args, env, config.stdin, config.stdout, config.stderr)
}

func runIncusCLI(ctx context.Context, args []string, env []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	incusCmd := exec.CommandContext(ctx, "incus", args...)
	incusCmd.Env = env
	incusCmd.Stdin = stdin
	incusCmd.Stdout = stdout
	incusCmd.Stderr = stderr
	return incusCmd.Run()
}

// resolveIncusDir returns the per-remote Incus config dir if it exists, otherwise empty string.
func resolveIncusDir(remote string) string {
	if remote == "" {
		return ""
	}
	dir := scconfig.ResolveConfigPath(remote)
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	return ""
}

func incusTenantProject(admin scconfig.Admin) (string, error) {
	tenant := strings.TrimSpace(admin.Tenant)
	if tenant == "" {
		return "", nil
	}
	ref, err := naming.ParseTenantRef(tenant)
	if err != nil {
		return "", err
	}
	prefix := admin.IncusProjectPrefix
	if strings.TrimSpace(prefix) == "" {
		prefix = scconfig.DefaultIncusProjectPrefix
	}
	projectName, err := naming.TenantIncusProjectNameWithPrefix(prefix, ref)
	if err != nil {
		return "", err
	}
	return projectName, nil
}

func shellCommandLine(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\n\"'\\$`") {
			quoted = append(quoted, strconv.Quote(arg))
			continue
		}
		quoted = append(quoted, arg)
	}
	return strings.Join(quoted, " ")
}
