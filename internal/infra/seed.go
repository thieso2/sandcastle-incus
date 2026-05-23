package infra

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"gopkg.in/yaml.v2"
)

const SeedVersion = 1

var deploymentNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type Seed struct {
	Version     int             `yaml:"version" json:"version"`
	Deployment  string          `yaml:"deployment" json:"deployment"`
	Infra       SeedInfra       `yaml:"infra" json:"infra"`
	Auth        SeedAuth        `yaml:"auth" json:"auth"`
	RouteBroker SeedRouteBroker `yaml:"routeBroker" json:"routeBroker"`
	Images      SeedImages      `yaml:"images" json:"images"`
	TLS         SeedTLS         `yaml:"tls,omitempty" json:"tls,omitempty"`
}

type SeedInfra struct {
	Remote           string `yaml:"remote" json:"remote"`
	StoragePool      string `yaml:"storagePool" json:"storagePool"`
	CIDRPool         string `yaml:"cidrPool" json:"cidrPool"`
	ProjectPrefix    string `yaml:"projectPrefix" json:"projectPrefix"`
	Project          string `yaml:"project" json:"project"`
	Host             string `yaml:"host,omitempty" json:"host,omitempty"`
	TLSMode          string `yaml:"tlsMode" json:"tlsMode"`
	LetsEncryptEmail string `yaml:"letsEncryptEmail,omitempty" json:"letsEncryptEmail,omitempty"`
}

type SeedAuth struct {
	Hostname           string   `yaml:"hostname,omitempty" json:"hostname,omitempty"`
	GitHubClientID     string   `yaml:"githubClientID,omitempty" json:"githubClientID,omitempty"`
	GitHubClientSecret string   `yaml:"githubClientSecret,omitempty" json:"githubClientSecret,omitempty"`
	AdminGitHubUsers   []string `yaml:"adminGitHubUsers,omitempty" json:"adminGitHubUsers,omitempty"`
	DebugDeviceUser    string   `yaml:"debugDeviceUser,omitempty" json:"debugDeviceUser,omitempty"`
	TailscaleAuthKey   string   `yaml:"tailscaleAuthKey,omitempty" json:"tailscaleAuthKey,omitempty"`
	DefaultUnixUser    string   `yaml:"defaultUnixUser,omitempty" json:"defaultUnixUser,omitempty"`
}

type SeedRouteBroker struct {
	IncusSocket string `yaml:"incusSocket,omitempty" json:"incusSocket,omitempty"`
}

type SeedImages struct {
	Base string `yaml:"base" json:"base"`
	AI   string `yaml:"ai" json:"ai"`
}

type SeedTLS struct {
	AuthHostname           string `yaml:"authHostname,omitempty" json:"authHostname,omitempty"`
	CaddyDataArchiveBase64 string `yaml:"caddyDataArchiveBase64,omitempty" json:"caddyDataArchiveBase64,omitempty"`
}

func DefaultDeploymentName(admin config.Admin) string {
	if value := strings.TrimSpace(admin.Remote); value != "" {
		return value
	}
	return config.DefaultRemote
}

func ValidateDeploymentName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("deployment name is required")
	}
	if !deploymentNamePattern.MatchString(name) {
		return fmt.Errorf("deployment name %q must contain only letters, digits, dots, underscores, and hyphens, and must start with a letter or digit", name)
	}
	return nil
}

func DefaultSeedPath(deployment string) (string, error) {
	deployment = strings.TrimSpace(deployment)
	if err := ValidateDeploymentName(deployment); err != nil {
		return "", err
	}
	return filepath.Join(config.DefaultConfigDir(), deployment+".seed.yml"), nil
}

func LoadSeed(path string) (Seed, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Seed{}, false, nil
	}
	if err != nil {
		return Seed{}, false, err
	}
	var seed Seed
	if err := yaml.Unmarshal(data, &seed); err != nil {
		return Seed{}, false, err
	}
	if seed.Version == 0 {
		seed.Version = SeedVersion
	}
	if seed.Version != SeedVersion {
		return Seed{}, true, fmt.Errorf("unsupported infrastructure seed version %d", seed.Version)
	}
	return seed, true, nil
}

func SaveSeed(path string, seed Seed) error {
	if seed.Version == 0 {
		seed.Version = SeedVersion
	}
	if err := ValidateDeploymentName(seed.Deployment); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(seed)
	if err != nil {
		return err
	}
	temp := path + ".new"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func SeedFromAdmin(deployment string, admin config.Admin, defaultUnixUser string) Seed {
	return Seed{
		Version:    SeedVersion,
		Deployment: strings.TrimSpace(deployment),
		Infra: SeedInfra{
			Remote:           strings.TrimSpace(admin.Remote),
			StoragePool:      strings.TrimSpace(admin.StoragePool),
			CIDRPool:         strings.TrimSpace(admin.CIDRPool),
			ProjectPrefix:    strings.TrimSpace(admin.IncusProjectPrefix),
			Project:          strings.TrimSpace(admin.InfrastructureProject),
			Host:             strings.TrimSpace(admin.InfrastructureHost),
			TLSMode:          infrastructureTLSMode(admin.InfrastructureTLSMode),
			LetsEncryptEmail: strings.TrimSpace(admin.LetsEncryptEmail),
		},
		Auth: SeedAuth{
			Hostname:           strings.TrimSpace(admin.AuthHostname),
			GitHubClientID:     strings.TrimSpace(admin.AuthGitHubClientID),
			GitHubClientSecret: strings.TrimSpace(admin.AuthGitHubClientSecret),
			AdminGitHubUsers:   append([]string{}, admin.AuthAdminGitHubUsers...),
			DebugDeviceUser:    strings.TrimSpace(admin.AuthDebugDeviceUser),
			TailscaleAuthKey:   strings.TrimSpace(admin.AuthTailscaleAuthKey),
			DefaultUnixUser:    strings.TrimSpace(defaultUnixUser),
		},
		RouteBroker: SeedRouteBroker{
			IncusSocket: strings.TrimSpace(admin.RouteBrokerIncusSocket),
		},
		Images: SeedImages{
			Base: strings.TrimSpace(admin.Images.Base),
			AI:   strings.TrimSpace(admin.Images.AI),
		},
	}
}

func AdminFromSeed(seed Seed) config.Admin {
	return config.Admin{
		Remote:                 strings.TrimSpace(seed.Infra.Remote),
		StoragePool:            strings.TrimSpace(seed.Infra.StoragePool),
		CIDRPool:               strings.TrimSpace(seed.Infra.CIDRPool),
		IncusProjectPrefix:     strings.TrimSpace(seed.Infra.ProjectPrefix),
		InfrastructureProject:  strings.TrimSpace(seed.Infra.Project),
		InfrastructureHost:     strings.TrimSpace(seed.Infra.Host),
		LetsEncryptEmail:       strings.TrimSpace(seed.Infra.LetsEncryptEmail),
		InfrastructureTLSMode:  strings.TrimSpace(seed.Infra.TLSMode),
		AuthHostname:           strings.TrimSpace(seed.Auth.Hostname),
		AuthGitHubClientID:     strings.TrimSpace(seed.Auth.GitHubClientID),
		AuthGitHubClientSecret: strings.TrimSpace(seed.Auth.GitHubClientSecret),
		AuthAdminGitHubUsers:   append([]string{}, seed.Auth.AdminGitHubUsers...),
		AuthDebugDeviceUser:    strings.TrimSpace(seed.Auth.DebugDeviceUser),
		AuthTailscaleAuthKey:   strings.TrimSpace(seed.Auth.TailscaleAuthKey),
		RouteBrokerIncusSocket: strings.TrimSpace(seed.RouteBroker.IncusSocket),
		Images: config.Images{
			Base: strings.TrimSpace(seed.Images.Base),
			AI:   strings.TrimSpace(seed.Images.AI),
		},
	}
}

func ResolveSeedAdmin(seed Seed) config.Admin {
	return config.MergeAdmin(config.MergeAdmin(config.AdminDefaults(), AdminFromSeed(seed)), config.LoadAdminEnvOverrides())
}

func EmbedCaddyDataArchive(seed Seed, authHostname string, data []byte) Seed {
	if len(data) == 0 {
		return seed
	}
	seed.TLS.AuthHostname = strings.TrimSpace(authHostname)
	seed.TLS.CaddyDataArchiveBase64 = base64.StdEncoding.EncodeToString(data)
	return seed
}

func CaddyDataArchiveBytes(seed Seed, authHostname string) ([]byte, bool, error) {
	encoded := strings.TrimSpace(seed.TLS.CaddyDataArchiveBase64)
	if encoded == "" {
		return nil, false, nil
	}
	if seedHost := strings.TrimSpace(seed.TLS.AuthHostname); seedHost != "" && strings.TrimSpace(authHostname) != "" && !strings.EqualFold(seedHost, strings.TrimSpace(authHostname)) {
		return nil, true, fmt.Errorf("seed Caddy ACME data belongs to Auth Hostname %q, not %q", seedHost, strings.TrimSpace(authHostname))
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, true, fmt.Errorf("decode seed Caddy ACME data: %w", err)
	}
	if len(data) == 0 {
		return nil, false, nil
	}
	return data, true, nil
}

func EmbedExistingCaddyDataArchive(seed Seed, admin config.Admin) (Seed, error) {
	if !strings.EqualFold(infrastructureTLSMode(admin.InfrastructureTLSMode), "acme") {
		return seed, nil
	}
	path := existingCaddyDataArchivePath(admin, "acme")
	if path == "" {
		return seed, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return seed, err
	}
	return EmbedCaddyDataArchive(seed, admin.AuthHostname, data), nil
}
