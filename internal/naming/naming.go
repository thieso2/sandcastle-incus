package naming

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	DefaultIncusProjectPrefix = "sc"
	DefaultProjectName        = "default"
)

var (
	safeNamePattern           = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)
	projectNamePattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
	githubUsernameNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?$`)
	unixUsernamePattern       = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

type TenantRef struct {
	Tenant string
}

type ProjectRef struct {
	Tenant  string
	Project string
}

type MachineRef struct {
	Tenant  string
	Project string
	Machine string
}

func ParseTenantRef(value string) (TenantRef, error) {
	ref := TenantRef{Tenant: strings.TrimSpace(value)}
	if err := ref.Validate(); err != nil {
		return TenantRef{}, err
	}
	return ref, nil
}

func ParseProjectRef(value string) (ProjectRef, error) {
	tenant, project, ok := strings.Cut(value, "/")
	if !ok {
		return ProjectRef{}, fmt.Errorf("project reference must be tenant/project")
	}
	ref := ProjectRef{Tenant: tenant, Project: project}
	if err := ref.Validate(); err != nil {
		return ProjectRef{}, err
	}
	return ref, nil
}

func (r TenantRef) Validate() error {
	return ValidateTenantName(r.Tenant)
}

func (r TenantRef) String() string {
	return r.Tenant
}

func (r ProjectRef) Validate() error {
	if strings.TrimSpace(r.Tenant) != "" {
		if err := ValidateTenantName(r.Tenant); err != nil {
			return err
		}
	}
	if err := ValidateProjectName(r.Project); err != nil {
		return err
	}
	return nil
}

func (r ProjectRef) String() string {
	if strings.TrimSpace(r.Tenant) == "" {
		return r.Project
	}
	return r.Tenant + "/" + r.Project
}

func (r MachineRef) Validate() error {
	_, err := validateMachineRef(r)
	return err
}

func (r MachineRef) String() string {
	return r.Tenant + "/" + r.Project + "/" + r.Machine
}

func TenantIncusProjectName(ref TenantRef) (string, error) {
	return TenantIncusProjectNameWithPrefix(DefaultIncusProjectPrefix, ref)
}

func TenantIncusProjectNameWithPrefix(prefix string, ref TenantRef) (string, error) {
	return tenantIncusProjectNameWithPrefix(prefix, ref.Tenant, ref.Validate)
}

func PersonalTenantIncusProjectNameWithPrefix(prefix string, tenant string) (string, error) {
	return tenantIncusProjectNameWithPrefix(prefix, tenant, func() error {
		return ValidateGitHubUsernameTenantName(tenant)
	})
}

func tenantIncusProjectNameWithPrefix(prefix string, tenant string, validate func() error) (string, error) {
	prefix = strings.TrimSpace(prefix)
	if err := ValidateIncusProjectPrefix(prefix); err != nil {
		return "", err
	}
	if err := validate(); err != nil {
		return "", err
	}
	name := prefix + "-" + tenant
	// Reserve 7 chars for the "-native" suffix used by the native project.
	if len(name) > 56 {
		return "", fmt.Errorf("incus project name %q exceeds 56 characters (must leave room for -infra/-native suffixes)", name)
	}
	return name, nil
}

func ValidateTenantName(name string) error {
	if !safeNamePattern.MatchString(name) {
		return fmt.Errorf("invalid tenant %q", name)
	}
	return nil
}

func ValidateGitHubUsernameTenantName(name string) error {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return fmt.Errorf("GitHub username tenant name is required")
	}
	if !githubUsernameNamePattern.MatchString(normalized) || strings.Contains(normalized, "--") {
		return fmt.Errorf("invalid GitHub username tenant name %q", name)
	}
	return nil
}

func ValidateProjectName(name string) error {
	if !projectNamePattern.MatchString(name) {
		return fmt.Errorf("invalid project %q", name)
	}
	return nil
}

func ValidateNewProjectName(name string) error {
	if err := ValidateProjectName(name); err != nil {
		return err
	}
	if IsReservedProjectName(name) {
		return fmt.Errorf("project name %q is reserved", name)
	}
	return nil
}

func ValidateMachineName(name string) error {
	if !safeNamePattern.MatchString(name) {
		return fmt.Errorf("invalid machine %q", name)
	}
	if IsReservedInfrastructureName(name) {
		return fmt.Errorf("machine name %q is reserved", name)
	}
	return nil
}

// ValidateInstallSuffix validates a Tenant DNS Suffix used as the install
// component of a machine reference and the stem of an incus remote name
// (ADR-0020). Same single-label shape as a machine name; dashes are allowed
// (a suffix like "obelix-eu" is legal and round-trips as an opaque label).
func ValidateInstallSuffix(name string) error {
	if !safeNamePattern.MatchString(name) {
		return fmt.Errorf("invalid install suffix %q", name)
	}
	return nil
}

func ValidateUnixUsername(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("Unix username is required")
	}
	if !unixUsernamePattern.MatchString(name) {
		return fmt.Errorf("invalid Unix username %q", name)
	}
	return nil
}

func ValidateIncusProjectPrefix(prefix string) error {
	if strings.TrimSpace(prefix) == "" {
		return fmt.Errorf("incus project prefix is required")
	}
	if !safeNamePattern.MatchString(prefix) {
		return fmt.Errorf("invalid incus project prefix %q", prefix)
	}
	return nil
}

func ValidateIncusProjectName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("incus project name is required")
	}
	if !safeNamePattern.MatchString(name) {
		return fmt.Errorf("invalid incus project name %q", name)
	}
	return nil
}

func IsReservedProjectName(name string) bool {
	if name == DefaultProjectName {
		return true
	}
	return IsReservedInfrastructureName(name)
}

func IsReservedInfrastructureName(name string) bool {
	switch name {
	case "admin", "ca", "dns", "infra", "route", "tailscale", "sc-ca", "sc-dns":
		return true
	default:
		return false
	}
}

func validateMachineRef(ref MachineRef) (MachineRef, error) {
	if err := ValidateTenantName(ref.Tenant); err != nil {
		return MachineRef{}, err
	}
	if err := ValidateProjectName(ref.Project); err != nil {
		return MachineRef{}, err
	}
	if err := ValidateMachineName(ref.Machine); err != nil {
		return MachineRef{}, err
	}
	return ref, nil
}
