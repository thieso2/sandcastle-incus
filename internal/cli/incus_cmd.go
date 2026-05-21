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
	dir := scconfig.RemoteIncusDir(remote)
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
	prefix := admin.ProjectPrefix
	if strings.TrimSpace(prefix) == "" {
		prefix = scconfig.DefaultProjectPrefix
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
