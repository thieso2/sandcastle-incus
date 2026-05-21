package incusx

import (
	"context"
	"fmt"
	"sort"
	"strings"

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
	certs, err := findCertificates(server, plan.CertificateName)
	if err != nil {
		return err
	}
	for _, cert := range certs {
		if err := validateGrantCertificate(cert, plan.CertificateName); err != nil {
			return err
		}
		projects := mergeProjects(cert.Projects, plan.Projects)
		if err := server.UpdateCertificate(cert.Fingerprint, api.CertificatePut{
			Name:        cert.Name,
			Type:        api.CertificateTypeClient,
			Restricted:  true,
			Projects:    projects,
			Certificate: cert.Certificate,
			Description: plan.Description,
		}, ""); err != nil {
			return fmt.Errorf("update certificate %s: %w", cert.Fingerprint[:12], err)
		}
	}
	return nil
}

func (m TrustManager) Revoke(ctx context.Context, plan usertrust.UserPlan) error {
	server, err := m.server()
	if err != nil {
		return err
	}
	certs, err := findCertificates(server, plan.CertificateName)
	if err != nil {
		return err
	}
	for _, cert := range certs {
		if err := validateGrantCertificate(cert, plan.CertificateName); err != nil {
			return err
		}
		projects := removeProjects(cert.Projects, plan.Projects)
		if err := server.UpdateCertificate(cert.Fingerprint, api.CertificatePut{
			Name:        cert.Name,
			Type:        api.CertificateTypeClient,
			Restricted:  true,
			Projects:    projects,
			Certificate: cert.Certificate,
			Description: plan.Description,
		}, ""); err != nil {
			return fmt.Errorf("update certificate %s: %w", cert.Fingerprint[:12], err)
		}
	}
	return nil
}

func (m TrustManager) ListTenantUsers(ctx context.Context, plan usertrust.TenantUsersPlan) (usertrust.TenantUsersResult, error) {
	server, err := m.server()
	if err != nil {
		return usertrust.TenantUsersResult{}, err
	}
	certificates, err := server.GetCertificates()
	if err != nil {
		return usertrust.TenantUsersResult{}, fmt.Errorf("list Incus certificates: %w", err)
	}
	users := []string{}
	for _, cert := range certificates {
		if cert.Type != api.CertificateTypeClient || !cert.Restricted {
			continue
		}
		if !containsProject(cert.Projects, plan.IncusProject) {
			continue
		}
		user := strings.TrimPrefix(cert.Name, usertrust.CertificateNamePrefix)
		if user == "" {
			user = cert.Name
		}
		users = append(users, user)
	}
	sort.Strings(users)
	return usertrust.TenantUsersResult{
		Tenant:       plan.Tenant,
		IncusProject: plan.IncusProject,
		Users:        users,
	}, nil
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
	remoteName := plan.RemoteName
	if remoteName == "" {
		remoteName = usertrust.RestrictedName(plan.User)
	}
	return usertrust.TokenResult{
		User:            plan.User,
		CertificateName: plan.CertificateName,
		RemoteName:      remoteName,
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

func findCertificates(server TrustServer, name string) ([]api.Certificate, error) {
	certificates, err := server.GetCertificates()
	if err != nil {
		return nil, fmt.Errorf("list Incus certificates: %w", err)
	}
	var matches []api.Certificate
	for _, cert := range certificates {
		if cert.Name == name {
			matches = append(matches, cert)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("restricted certificate %q not found; create a token first and add the client certificate", name)
	}
	return matches, nil
}

func validateGrantCertificate(certificate api.Certificate, name string) error {
	if certificate.Type != api.CertificateTypeClient {
		return fmt.Errorf("restricted certificate %q is %q, want client certificate", name, certificate.Type)
	}
	if !certificate.Restricted {
		return fmt.Errorf("restricted certificate %q is not restricted", name)
	}
	return nil
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

func removeProjects(existing []string, removed []string) []string {
	removedSet := map[string]bool{}
	for _, project := range removed {
		removedSet[project] = true
	}
	result := make([]string, 0, len(existing))
	for _, project := range existing {
		if project == "" || removedSet[project] {
			continue
		}
		result = append(result, project)
	}
	return result
}

func containsProject(projects []string, want string) bool {
	for _, project := range projects {
		if project == want {
			return true
		}
	}
	return false
}
