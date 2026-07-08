package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/thieso2/sandcastle-incus/internal/certs"
	"github.com/thieso2/sandcastle-incus/internal/tlssign"
)

// newSidecarCommand groups commands that run ON a tenant sidecar (invoked there
// by systemd), not on the operator host.
func newSidecarCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sidecar",
		Short: "Commands that run on a tenant sidecar",
	}
	cmd.AddCommand(newSidecarTLSSignCommand())
	return cmd
}

// newSidecarTLSSignCommand runs the tenant-CA leaf signer HTTP service. It reads
// the tenant CA (cert+key) from local files and serves /tls/ca and
// /tls/leaf?fqdn=… on the tenant bridge (ADR-0011).
func newSidecarTLSSignCommand() *cobra.Command {
	var caCertPath, caKeyPath, suffix, listen string
	cmd := &cobra.Command{
		Use:   "tls-sign",
		Short: "Serve the tenant-CA leaf signer (runs on the sidecar)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(listen) == "" {
				return fmt.Errorf("--listen is required")
			}
			// Self-initialize the tenant CA on first run: the CA key lives ONLY on
			// the sidecar and everyone fetches the cert from /tls/ca, so generating
			// it here (once) keeps it stable and needs no external delivery. The CN
			// is suffix-based so distinct installs (idefix/obelix) get distinct
			// roots even when the tenant name (GitHub user) is identical.
			if err := ensureCA(caCertPath, caKeyPath, suffix, cmd.OutOrStdout()); err != nil {
				return err
			}
			caCert, err := os.ReadFile(caCertPath)
			if err != nil {
				return fmt.Errorf("read CA certificate: %w", err)
			}
			caKey, err := os.ReadFile(caKeyPath)
			if err != nil {
				return fmt.Errorf("read CA private key: %w", err)
			}
			handler := tlssign.Handler(caCert, caKey, suffix, time.Now)
			server := &http.Server{
				Addr:              listen,
				Handler:           handler,
				ReadHeaderTimeout: 5 * time.Second,
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sidecar tls-sign listening on %s (zone %q)\n", listen, suffix)
			return server.ListenAndServe()
		},
	}
	cmd.Flags().StringVar(&caCertPath, "ca-cert", "/etc/sandcastle/ca/ca.crt", "tenant CA certificate PEM path")
	cmd.Flags().StringVar(&caKeyPath, "ca-key", "/etc/sandcastle/ca/ca.key", "tenant CA private key PEM path")
	cmd.Flags().StringVar(&suffix, "suffix", "", "tenant DNS suffix; leaf names outside it are refused")
	cmd.Flags().StringVar(&listen, "listen", "", "listen address (e.g. 10.124.0.3:9443)")
	return cmd
}

// ensureCA generates the tenant CA at the given paths if it is not already
// present. The common name is "Sandcastle <suffix> tenant CA" — suffix-scoped so
// two installs sharing a tenant name still get distinct roots.
func ensureCA(certPath, keyPath, suffix string, out io.Writer) error {
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return nil
		}
	}
	cn := "Sandcastle " + strings.TrimSpace(suffix) + " tenant CA"
	if strings.TrimSpace(suffix) == "" {
		cn = "Sandcastle tenant CA"
	}
	ca, err := certs.GenerateCA(cn, time.Now())
	if err != nil {
		return fmt.Errorf("generate tenant CA: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(certPath, ca.CertificatePEM, 0o644); err != nil {
		return fmt.Errorf("write CA certificate: %w", err)
	}
	if err := os.WriteFile(keyPath, ca.PrivateKeyPEM, 0o600); err != nil {
		return fmt.Errorf("write CA private key: %w", err)
	}
	fmt.Fprintf(out, "generated tenant CA %q at %s\n", cn, certPath)
	return nil
}
