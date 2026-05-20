package incusx

import (
	"context"
	"fmt"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

type TrustServer interface {
	GetCertificates() ([]api.Certificate, error)
	UpdateCertificate(fingerprint string, certificate api.CertificatePut, ETag string) error
	CreateCertificateToken(certificate api.CertificatesPost) (incus.Operation, error)
}

type TrustManager struct {
	Remote     string
	ConfigPath string
	Server     TrustServer
}

func NewTrustManager(remote string) TrustManager {
	return TrustManager{Remote: remote}
}

func (m TrustManager) Grant(ctx context.Context, plan usertrust.UserPlan) error {
	server, err := m.server()
	if err != nil {
		return err
	}
	cert, err := findCertificate(server, plan.CertificateName)
	if err != nil {
		return err
	}
	projects := mergeProjects(cert.Projects, plan.Projects)
	return server.UpdateCertificate(cert.Fingerprint, api.CertificatePut{
		Name:        cert.Name,
		Type:        api.CertificateTypeClient,
		Restricted:  true,
		Projects:    projects,
		Certificate: cert.Certificate,
		Description: plan.Description,
	}, "")
}

func (m TrustManager) CreateToken(ctx context.Context, plan usertrust.UserPlan) (usertrust.TokenResult, error) {
	server, err := m.server()
	if err != nil {
		return usertrust.TokenResult{}, err
	}
	op, err := server.CreateCertificateToken(api.CertificatesPost{
		Token: true,
		CertificatePut: api.CertificatePut{
			Name:        plan.CertificateName,
			Type:        api.CertificateTypeClient,
			Restricted:  plan.Restricted,
			Projects:    plan.Projects,
			Description: plan.Description,
		},
	})
	if err != nil {
		return usertrust.TokenResult{}, err
	}
	opAPI := op.Get()
	token, err := (&opAPI).ToCertificateAddToken()
	if err != nil {
		return usertrust.TokenResult{}, err
	}
	return usertrust.TokenResult{
		User:            plan.User,
		CertificateName: plan.CertificateName,
		Restricted:      plan.Restricted,
		Projects:        plan.Projects,
		Token:           token.String(),
	}, nil
}

func (m TrustManager) server() (TrustServer, error) {
	if m.Server != nil {
		return m.Server, nil
	}
	loaded, err := cliconfig.LoadConfig(m.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load Incus config: %w", err)
	}
	remote := m.Remote
	if remote == "" {
		remote = loaded.DefaultRemote
	}
	server, err := loaded.GetInstanceServer(remote)
	if err != nil {
		return nil, fmt.Errorf("connect to Incus remote %q: %w", remote, err)
	}
	return server, nil
}

func findCertificate(server TrustServer, name string) (api.Certificate, error) {
	certificates, err := server.GetCertificates()
	if err != nil {
		return api.Certificate{}, fmt.Errorf("list Incus certificates: %w", err)
	}
	for _, cert := range certificates {
		if cert.Name == name {
			return cert, nil
		}
	}
	return api.Certificate{}, fmt.Errorf("restricted certificate %q not found; create a token first and add the client certificate", name)
}

func mergeProjects(existing []string, added []string) []string {
	seen := map[string]bool{}
	merged := make([]string, 0, len(existing)+len(added))
	for _, project := range append(existing, added...) {
		if project == "" || seen[project] {
			continue
		}
		seen[project] = true
		merged = append(merged, project)
	}
	return merged
}
