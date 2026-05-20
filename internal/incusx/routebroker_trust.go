package incusx

import (
	"context"
	"fmt"
	"strings"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
)

type RouteBrokerTrustServer interface {
	GetCertificates() ([]api.Certificate, error)
}

type RouteBrokerTrustMapper struct {
	Remote     string
	ConfigPath string
	Server     RouteBrokerTrustServer
}

func NewRouteBrokerTrustMapper(remote string) RouteBrokerTrustMapper {
	return RouteBrokerTrustMapper{Remote: remote}
}

func (m RouteBrokerTrustMapper) OwnerForFingerprint(ctx context.Context, fingerprint string) (string, error) {
	server := m.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(m.ConfigPath)
		if err != nil {
			return "", fmt.Errorf("load Incus config: %w", err)
		}
		remote := m.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return "", fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = instanceServer
	}
	certificates, err := server.GetCertificates()
	if err != nil {
		return "", fmt.Errorf("list Incus certificates: %w", err)
	}
	normalized := normalizeFingerprint(fingerprint)
	for _, certificate := range certificates {
		if normalizeFingerprint(certificate.Fingerprint) != normalized {
			continue
		}
		owner := ownerFromCertificate(certificate)
		if owner == "" {
			return "", fmt.Errorf("certificate %s is not a Sandcastle restricted user certificate", fingerprint)
		}
		return owner, nil
	}
	return "", fmt.Errorf("certificate fingerprint %s is not trusted", fingerprint)
}

func ownerFromCertificate(certificate api.Certificate) string {
	if certificate.Type != api.CertificateTypeClient || !certificate.Restricted {
		return ""
	}
	return ownerFromCertificateName(certificate.CertificatePut.Name)
}

func ownerFromCertificateName(name string) string {
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, "sandcastle-") {
		return ""
	}
	return strings.TrimPrefix(name, "sandcastle-")
}

func normalizeFingerprint(value string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), ":", "")
}
