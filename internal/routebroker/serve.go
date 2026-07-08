package routebroker

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/svclog"
)

type ServeRequest struct {
	Address  string
	CertFile string
	KeyFile  string
}

type ServePlan struct {
	Address  string `json:"address"`
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
}

type Runner interface {
	Serve(context.Context, ServePlan) error
}

type HTTPRunner struct {
	Server Server
}

func PlanServe(request ServeRequest) (ServePlan, error) {
	address := strings.TrimSpace(request.Address)
	if address == "" {
		address = ":9443"
	}
	certFile := strings.TrimSpace(request.CertFile)
	if certFile == "" {
		return ServePlan{}, fmt.Errorf("route broker TLS certificate file is required")
	}
	keyFile := strings.TrimSpace(request.KeyFile)
	if keyFile == "" {
		return ServePlan{}, fmt.Errorf("route broker TLS key file is required")
	}
	return ServePlan{
		Address:  address,
		CertFile: certFile,
		KeyFile:  keyFile,
	}, nil
}

func (r HTTPRunner) Serve(ctx context.Context, plan ServePlan) error {
	certificate, err := tls.LoadX509KeyPair(plan.CertFile, plan.KeyFile)
	if err != nil {
		return fmt.Errorf("load route broker TLS certificate: %w", err)
	}
	listener, err := tls.Listen("tcp", plan.Address, &tls.Config{
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		return fmt.Errorf("listen for route broker on %s: %w", plan.Address, err)
	}
	logger := svclog.New("route-broker", os.Stderr, nil)
	server := &http.Server{
		Handler: logger.HTTP(r.Server),
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = server.Shutdown(context.Background())
		case <-done:
		}
	}()
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
