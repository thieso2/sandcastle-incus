package cli

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/hostkeys"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

// v2MachineNames lists every name a v2 machine answers at: its Machine Private
// Hostname, plus the short alias the default project also serves (ADR-0018).
func v2MachineNames(summary tenant.Summary, project string, machine string) []string {
	suffix := strings.TrimSpace(summary.DNSSuffix)
	if suffix == "" {
		return nil
	}
	names := []string{machine + "." + project + "." + suffix}
	if project == naming.DefaultProjectName {
		names = append(names, machine+"."+suffix)
	}
	return names
}

// hostKeysConfig binds the hostkeys package to this tenant. privateCIDR is the
// subnet read from a live machine's own interface: a restricted tenant
// certificate cannot see the tenant bridge's config, so summary.PrivateCIDR is
// empty for v2 and only the machine can tell us. An empty CIDR disables the
// recycled-IP purge rather than guessing at a subnet.
func hostKeysConfig(config commandConfig, summary tenant.Summary, privateCIDR string) hostkeys.Config {
	if strings.TrimSpace(privateCIDR) == "" {
		privateCIDR = summary.PrivateCIDR
	}
	cidr, err := netip.ParsePrefix(strings.TrimSpace(privateCIDR))
	if err != nil {
		cidr = netip.Prefix{}
	}
	return hostkeys.Config{
		Path:   hostkeys.DefaultPath(),
		Remote: config.adminConfig.Remote,
		Tenant: summary.Tenant,
		CIDR:   cidr,
		Stderr: config.stderr,
	}
}

// machineHostKeys reads the machine's host keys authoritatively over the Incus
// API, falling back to ssh-keyscan when that channel is unavailable — a VM
// whose incus-agent has not started, most often. The bool reports whether the
// keys were merely trusted on first use.
func machineHostKeys(ctx context.Context, config commandConfig, incusProject string, machineName string, privateIP string) ([]hostkeys.Key, bool, error) {
	found, err := config.tenantCreator.MachineHostKeysV2(ctx, incusProject, machineName)
	if err == nil {
		return toHostKeys(found), false, nil
	}
	verboseCLI(config, "host key: authoritative read failed: %v", err)
	scanned, scanErr := hostkeys.Keyscan(ctx, privateIP)
	if scanErr != nil {
		return nil, false, fmt.Errorf("%w (ssh-keyscan also failed: %v)", err, scanErr)
	}
	if config.stderr != nil {
		fmt.Fprintf(config.stderr, "warning: could not read %s's host keys over the Incus API (%v)\n", machineName, err)
		fmt.Fprintf(config.stderr, "         falling back to ssh-keyscan; the keys are trusted on first use\n")
	}
	return scanned, true, nil
}

func toHostKeys(found []incusx.HostKey) []hostkeys.Key {
	keys := make([]hostkeys.Key, 0, len(found))
	for _, key := range found {
		keys = append(keys, hostkeys.Key{Type: key.Type, Key: key.Key})
	}
	return keys
}

// ensureV2HostKey makes ~/.ssh/known_hosts tell the truth about this machine,
// so the SSH session that follows can demand StrictHostKeyChecking=yes. It
// reclaims the machine's names from stale untagged entries and purges recycled
// private-IP debris. Returns false when no key could be obtained at all, in
// which case the caller must fall back to accept-new.
func ensureV2HostKey(ctx context.Context, config commandConfig, summary tenant.Summary, project string, machineName string, privateIP string, privateCIDR string) bool {
	names := v2MachineNames(summary, project, machineName)
	keysConfig := hostKeysConfig(config, summary, privateCIDR)
	if len(names) == 0 || keysConfig.Path == "" {
		verboseCLI(config, "host key: no hostname or known_hosts path; leaving host key handling to ssh")
		return false
	}
	if !keysConfig.CIDR.IsValid() {
		verboseCLI(config, "host key: no private CIDR for %s; skipping recycled-IP purge", summary.Tenant)
	}
	keys, tofu, err := machineHostKeys(ctx, config, summary.V2IncusProjectName(project), machineName, privateIP)
	if err != nil {
		if config.stderr != nil {
			fmt.Fprintf(config.stderr, "warning: could not determine %s's host keys: %v\n", machineName, err)
		}
		return false
	}
	plan, err := keysConfig.Ensure(hostkeys.Machine{Names: names, Keys: keys, TOFU: tofu})
	if err != nil {
		if config.stderr != nil {
			fmt.Fprintf(config.stderr, "warning: could not update %s: %v\n", keysConfig.Path, err)
		}
		return false
	}
	reportHostKeyPlan(config, plan)
	if err := plan.Apply(); err != nil {
		if config.stderr != nil {
			fmt.Fprintf(config.stderr, "warning: could not write %s: %v\n", keysConfig.Path, err)
		}
		return false
	}
	return true
}

// reportHostKeyPlan prints every line sc is about to remove or rewrite. The
// file belongs to the user; deletions from it are never silent.
func reportHostKeyPlan(config commandConfig, plan hostkeys.Plan) {
	if config.stderr == nil {
		return
	}
	for _, warning := range plan.Warnings {
		fmt.Fprintf(config.stderr, "warning: %s\n", warning)
	}
	if plan.PurgeSkipped != "" {
		fmt.Fprintf(config.stderr, "note: %s\n", plan.PurgeSkipped)
	}
	for _, change := range plan.Changes {
		if change.Action == hostkeys.ActionAdd {
			verboseCLI(config, "known_hosts: %s", change)
			continue
		}
		fmt.Fprintf(config.stderr, "known_hosts: %s\n", change)
	}
}
