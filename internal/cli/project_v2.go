package cli

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
			fmt.Fprintln(config.stdout, strings.TrimSpace(string(payload)))
			return nil
		},
	}
	command.Flags().StringVar(&broker, "broker", "", "Sandcastle Broker URL (e.g. https://host:9443)")
	command.Flags().StringVar(&certFile, "cert", "", "tenant client certificate file")
	command.Flags().StringVar(&keyFile, "key", "", "tenant client key file")
	return command
}
