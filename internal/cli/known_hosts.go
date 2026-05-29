package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/incusx"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
)

type machineKnownHostsManager interface {
	RefreshMachine(ctx context.Context, plan machine.CreatePlan) error
}

type localKnownHostsManager struct {
	verbose      bool
	stderr       io.Writer
	remote       string
	connectCache *incusx.ConnectCache
}

func newLocalKnownHostsManager(remote string, verbose bool, stderr io.Writer) localKnownHostsManager {
	return localKnownHostsManager{remote: remote, verbose: verbose, stderr: stderr}
}

func (m localKnownHostsManager) WithConnectCache(cache incusx.ConnectCache) localKnownHostsManager {
	m.connectCache = &cache
	return m
}

func (m localKnownHostsManager) RefreshMachine(ctx context.Context, plan machine.CreatePlan) error {
	hostname := strings.TrimSpace(plan.Hostname)
	privateIP := strings.TrimSpace(plan.PrivateIP)
	if hostname == "" {
		return fmt.Errorf("machine %s has no hostname for SSH known_hosts refresh", plan.InstanceName)
	}
	if privateIP == "" {
		return fmt.Errorf("machine %s has no private IP for SSH known_hosts refresh", plan.InstanceName)
	}

	path := incusx.TenantKnownHostsPath(m.remote, plan.Tenant.Tenant)
	if err := ensureKnownHostsFile(path); err != nil {
		return err
	}
	if m.knownHostsRecentlyRefreshed(path, hostname, privateIP) {
		m.log("known_hosts cache hit for " + hostname + " and " + privateIP)
		return nil
	}
	keys, err := m.scanKnownHost(ctx, privateIP)
	if err != nil {
		return nil
	}
	if err := m.removeKnownHost(ctx, path, hostname); err != nil {
		return err
	}
	if err := m.removeKnownHost(ctx, path, privateIP); err != nil {
		return err
	}
	var entries []byte
	entries = append(entries, rewriteKnownHostKeys(keys, hostname)...)
	entries = append(entries, rewriteKnownHostKeys(keys, privateIP)...)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := file.Write(entries); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	m.log("updated " + path + " for " + hostname + " and " + privateIP)
	if m.connectCache != nil {
		m.connectCache.MarkKeyscanned(hostname)
		m.connectCache.MarkKeyscanned(privateIP)
	}
	if m.stderr != nil {
		fmt.Fprintf(m.stderr, "Updated SSH known_hosts for %s (%s).\n", hostname, privateIP)
	}
	return nil
}

func (m localKnownHostsManager) knownHostsRecentlyRefreshed(path string, hostname string, privateIP string) bool {
	if m.connectCache == nil {
		return false
	}
	if !m.connectCache.IsKeyscanRecent(hostname) || !m.connectCache.IsKeyscanRecent(privateIP) {
		return false
	}
	return knownHostsFileContainsHost(path, hostname) && knownHostsFileContainsHost(path, privateIP)
}

func (m localKnownHostsManager) removeKnownHost(ctx context.Context, path string, host string) error {
	args := []string{"-R", host, "-f", path}
	m.log("run " + shellCommandLine(append([]string{"ssh-keygen"}, args...)))
	output, err := exec.CommandContext(ctx, "ssh-keygen", args...).CombinedOutput()
	if m.verbose && len(output) > 0 && m.stderr != nil {
		fmt.Fprint(m.stderr, string(output))
	}
	if err != nil {
		return fmt.Errorf("remove stale SSH host key for %s: %w", host, err)
	}
	return nil
}

func (m localKnownHostsManager) scanKnownHost(ctx context.Context, host string) ([]byte, error) {
	args := []string{"-T", "2", "-t", "ed25519,ecdsa,rsa", host}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		m.log("run " + shellCommandLine(append([]string{"ssh-keyscan"}, args...)))
		command := exec.CommandContext(ctx, "ssh-keyscan", args...)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if m.verbose && stderr.Len() > 0 && m.stderr != nil {
			fmt.Fprint(m.stderr, stderr.String())
		}
		keys := filterKnownHostKeys(output)
		if err == nil && len(keys) > 0 {
			return keys, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("no host keys returned")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("scan SSH host key for %s: %w", host, lastErr)
}

func filterKnownHostKeys(output []byte) []byte {
	var filtered bytes.Buffer
	for _, line := range bytes.Split(output, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || bytes.HasPrefix(line, []byte("#")) {
			continue
		}
		filtered.Write(line)
		filtered.WriteByte('\n')
	}
	return filtered.Bytes()
}

func rewriteKnownHostKeys(output []byte, host string) []byte {
	var rewritten bytes.Buffer
	for _, line := range bytes.Split(output, []byte("\n")) {
		fields := bytes.Fields(line)
		if len(fields) < 3 {
			continue
		}
		fields[0] = []byte(host)
		rewritten.Write(bytes.Join(fields, []byte(" ")))
		rewritten.WriteByte('\n')
	}
	return rewritten.Bytes()
}

func refreshMachineKnownHosts(ctx context.Context, config commandConfig, plan machine.CreatePlan) error {
	if config.knownHosts == nil {
		return nil
	}
	return config.knownHosts.RefreshMachine(ctx, plan)
}

func withTenantKnownHostsFile(config commandConfig, plan machine.ConnectPlan) machine.ConnectPlan {
	if plan.Managed && strings.TrimSpace(plan.KnownHostsFile) == "" {
		plan.KnownHostsFile = incusx.TenantKnownHostsPath(config.adminConfig.Remote, plan.Tenant.Tenant)
	}
	return plan
}

func ensureKnownHostsFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}

func knownHostsFileContainsHost(path string, host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range bytes.Split(content, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || bytes.HasPrefix(line, []byte("#")) || bytes.HasPrefix(line, []byte("|")) {
			continue
		}
		fields := bytes.Fields(line)
		if len(fields) == 0 {
			continue
		}
		for _, candidate := range bytes.Split(fields[0], []byte(",")) {
			if string(candidate) == host {
				return true
			}
		}
	}
	return false
}

func (m localKnownHostsManager) log(message string) {
	if !m.verbose || m.stderr == nil {
		return
	}
	fmt.Fprintln(m.stderr, "[known-hosts] "+message)
}
