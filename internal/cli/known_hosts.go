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
	connectCache *incusx.ConnectCache
}

func newLocalKnownHostsManager(verbose bool, stderr io.Writer) localKnownHostsManager {
	return localKnownHostsManager{verbose: verbose, stderr: stderr}
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

	// Fast path: skip the network keyscan if this host was scanned recently.
	// SSH uses StrictHostKeyChecking=accept-new so the key is already in known_hosts.
	if m.connectCache != nil && m.connectCache.IsKeyscanRecent(hostname) {
		return nil
	}

	path, err := userKnownHostsPath()
	if err != nil {
		return err
	}
	if err := ensureKnownHostsFile(path); err != nil {
		return err
	}
	keys, err := m.scanKnownHost(ctx, privateIP)
	if err != nil {
		if m.stderr != nil {
			fmt.Fprintf(m.stderr, "Warning: SSH keyscan for %s failed (%v); known_hosts not updated.\n", privateIP, err)
		}
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
	if m.stderr != nil {
		fmt.Fprintf(m.stderr, "Updated SSH known_hosts for %s (%s).\n", hostname, privateIP)
	}
	if m.connectCache != nil {
		m.connectCache.MarkKeyscanned(hostname)
	}
	return nil
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

func userKnownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
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

func (m localKnownHostsManager) log(message string) {
	if !m.verbose || m.stderr == nil {
		return
	}
	fmt.Fprintln(m.stderr, "[known-hosts] "+message)
}
