package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/thieso2/sandcastle-incus/internal/naming"
)

// newProjectCreateV2Command is the tenant-facing `sc project create-v2` client
// for the Sandcastle Broker (ADR-0016). It authenticates to the broker with the
// tenant's restricted Incus client certificate; the broker creates the app
// project + profile and extends the tenant's cert. After it returns, the tenant
// can `incus launch` into sc2-<tenant>-<project> natively.
func newProjectCreateV2Command(config commandConfig, opts *rootOptions) *cobra.Command {
	var broker, certFile, keyFile string
	var writeRemote bool
	var incusEndpoint, incusConf, remoteName string
	command := &cobra.Command{
		Use:   "create-v2 name",
		Short: "Create a project via the Sandcastle Broker (v2 self-service)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := strings.TrimSpace(args[0])
			if err := naming.ValidateNewProjectName(project); err != nil {
				return err
			}
			if strings.TrimSpace(broker) == "" {
				return fmt.Errorf("broker URL is required (--broker https://host:9443)")
			}
			cert, err := tls.LoadX509KeyPair(certFile, keyFile)
			if err != nil {
				return fmt.Errorf("load client certificate: %w", err)
			}
			client := &http.Client{
				Timeout: 60 * time.Second,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{
						Certificates:       []tls.Certificate{cert},
						InsecureSkipVerify: true, // broker identity is pinned out-of-band; auth is by client cert
						MinVersion:         tls.VersionTLS12,
					},
				},
			}
			body, _ := json.Marshal(map[string]string{"project": project})
			url := strings.TrimRight(broker, "/") + "/v2/projects"
			resp, err := client.Post(url, "application/json", bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("contact broker: %w", err)
			}
			defer resp.Body.Close()
			payload, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("broker rejected request (%d): %s", resp.StatusCode, strings.TrimSpace(string(payload)))
			}
			var result struct {
				Tenant       string `json:"tenant"`
				Project      string `json:"project"`
				IncusProject string `json:"incusProject"`
			}
			_ = json.Unmarshal(payload, &result)
			fmt.Fprintln(config.stdout, strings.TrimSpace(string(payload)))

			// By default, drop a ready-to-use per-project incus remote so the tenant
			// can `incus <cmd> <tenant>-<project>:` with no --project flag.
			if writeRemote && result.IncusProject != "" {
				name := strings.TrimSpace(remoteName)
				if name == "" {
					name = result.Tenant + "-" + result.Project
				}
				endpoint, err := incusEndpointFromBroker(incusEndpoint, broker)
				if err != nil {
					fmt.Fprintf(config.stderr, "Note: skipped per-project remote: %v\n", err)
					return nil
				}
				if err := addProjectRemote(cmd.Context(), name, endpoint, result.IncusProject, incusConf); err != nil {
					fmt.Fprintf(config.stderr, "Note: created project but could not add remote %q: %v\n", name, err)
					return nil
				}
				fmt.Fprintf(config.stdout, "added incus remote %q → project %s (try: incus list %s:)\n", name, result.IncusProject, name)
			}
			return nil
		},
	}
	command.Flags().StringVar(&broker, "broker", "", "Sandcastle Broker URL (e.g. https://host:9443)")
	command.Flags().StringVar(&certFile, "cert", "", "tenant client certificate file")
	command.Flags().StringVar(&keyFile, "key", "", "tenant client key file")
	command.Flags().BoolVar(&writeRemote, "write-remote", true, "add a per-project incus remote after creating the project")
	command.Flags().StringVar(&incusEndpoint, "incus-endpoint", "", "Incus HTTPS endpoint for the remote (default: broker host on :8443)")
	command.Flags().StringVar(&incusConf, "incus-conf", "", "INCUS_CONF dir to write the remote into (default: $INCUS_CONF or the incus default)")
	command.Flags().StringVar(&remoteName, "remote-name", "", "name for the per-project remote (default: <tenant>-<project>)")
	return command
}

// incusEndpointFromBroker returns the explicit endpoint if set, else derives it
// from the broker URL's host on the Incus API port (8443) — the broker and the
// Incus daemon share a host in the v2 MVP.
func incusEndpointFromBroker(explicit string, brokerURL string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit), nil
	}
	u, err := neturl.Parse(brokerURL)
	if err != nil {
		return "", fmt.Errorf("parse broker URL: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("broker URL has no host")
	}
	return "https://" + net.JoinHostPort(host, "8443"), nil
}

// addProjectRemote shells out to `incus remote add` (matching v1's remote
// handling). No token is needed — the tenant's client cert is already trusted;
// --accept-certificate pins the server cert so an IP endpoint validates, and
// --project pins the remote's default project.
func addProjectRemote(ctx context.Context, name string, endpoint string, project string, incusConf string) error {
	if _, err := exec.LookPath("incus"); err != nil {
		return fmt.Errorf("incus CLI not found on PATH")
	}
	args := []string{"remote", "add", name, endpoint, "--auth-type=tls", "--accept-certificate", "--project", project}
	cmd := exec.CommandContext(ctx, "incus", args...)
	cmd.Env = os.Environ()
	if strings.TrimSpace(incusConf) != "" {
		cmd.Env = append(cmd.Env, "INCUS_CONF="+strings.TrimSpace(incusConf))
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "already exists") {
			return nil // idempotent
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}
