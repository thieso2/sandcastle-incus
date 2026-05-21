package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

type sshKeySetPayload struct {
	Tenant       string `json:"tenant"`
	IncusProject string `json:"incusProject"`
	Key          string `json:"key"`
}

func newSSHKeyCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "ssh-key",
		Short: "Manage the current tenant SSH public key",
	}
	command.AddCommand(newSSHKeySetCommand(config, opts))
	return command
}

func newSSHKeySetCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var keyFile string
	var dryRun bool
	command := &cobra.Command{
		Use:   "set [public-key]",
		Short: "Set the current tenant SSH public key",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := sshPublicKeyFromArgs(args, keyFile)
			if err != nil {
				return err
			}
			tenant, err := currentTenantSummary(cmd.Context(), config)
			if err != nil {
				return err
			}
			payload := sshKeySetPayload{
				Tenant:       tenant.Tenant,
				IncusProject: tenant.IncusName,
				Key:          key,
			}
			if !dryRun {
				if config.tenantSSHKeyUpdater == nil {
					return fmt.Errorf("tenant SSH key updater is not configured")
				}
				if err := config.tenantSSHKeyUpdater.SetTenantSSHKey(cmd.Context(), tenant.IncusName, key); err != nil {
					return err
				}
			}
			return writeOutput(config.stdout, opts.output, formatSSHKeySet(payload), payload)
		},
	}
	command.Flags().StringVar(&keyFile, "file", "", "read SSH public key from file")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the SSH key update without mutating tenant metadata")
	return command
}

func sshPublicKeyFromArgs(args []string, keyFile string) (string, error) {
	if keyFile != "" && len(args) > 0 {
		return "", fmt.Errorf("pass either a public key argument or --file, not both")
	}
	if keyFile != "" {
		return readSSHPublicKeyFile(keyFile)
	}
	if len(args) > 0 {
		return normalizeSSHPublicKey(args[0])
	}
	for _, candidate := range defaultSSHPublicKeyFiles() {
		key, err := readSSHPublicKeyFile(candidate)
		if err == nil {
			return key, nil
		}
	}
	return "", fmt.Errorf("SSH public key is required; pass --file ~/.ssh/id_ed25519.pub or quote the public key")
}

func defaultSSHPublicKeyFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".ssh", "id_ed25519.pub"),
		filepath.Join(home, ".ssh", "id_rsa.pub"),
	}
}

func readSSHPublicKeyFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return normalizeSSHPublicKey(string(data))
}

func normalizeSSHPublicKey(value string) (string, error) {
	key := strings.TrimSpace(value)
	if key == "" {
		return "", fmt.Errorf("SSH public key is empty")
	}
	if !strings.HasPrefix(key, "ssh-") {
		return "", fmt.Errorf("SSH public key must start with ssh-")
	}
	return key, nil
}

func formatSSHKeySet(payload sshKeySetPayload) string {
	return fmt.Sprintf("SSH key set for tenant %s", payload.Tenant)
}
