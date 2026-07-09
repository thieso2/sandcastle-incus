package hostkeys

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Keyscan asks the host itself for its public keys. This is trust on first use:
// whatever answers on port 22 is believed. It exists only as a fallback for
// machines the Incus API cannot read — a VM whose incus-agent is not running —
// and every line it produces is tagged `tofu` so a later connect that can read
// the machine authoritatively will replace it.
func Keyscan(ctx context.Context, host string) ([]Key, error) {
	args := []string{"-T", "2", "-t", "ed25519,ecdsa,rsa", host}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		command := exec.CommandContext(ctx, "ssh-keyscan", args...)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if err == nil {
			if keys := keyscanKeys(output); len(keys) > 0 {
				return keys, nil
			}
			lastErr = fmt.Errorf("no host keys returned")
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("scan SSH host keys for %s: %w", host, lastErr)
}

// keyscanKeys collects every key ssh-keyscan reported, in the order the -t list
// asked for them, so the resulting known_hosts lines are stable across runs.
func keyscanKeys(output []byte) []Key {
	var keys []Key
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		keys = append(keys, Key{Type: fields[1], Key: fields[2]})
	}
	return keys
}
