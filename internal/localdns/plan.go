package localdns

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/cidr"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

const (
	DefaultListenIP   = "127.0.0.1"
	DefaultListenPort = 53541
)

type Request struct {
	Reference string
}

type Plan struct {
	Reference    string `json:"reference"`
	IncusProject string `json:"incusProject"`
	DNSSuffix    string `json:"dnsSuffix"`
	DNSEndpoint  string `json:"dnsEndpoint"`
	Listen       string `json:"listen"`
	StatePath    string `json:"statePath"`
	ResolverPath string `json:"resolverPath"`
	Platform     string `json:"platform"`

	ResolverStrategy string    `json:"resolverStrategy"`
	ResolverCommands []Command `json:"resolverCommands,omitempty"`
}

type Result struct {
	Reference    string `json:"reference"`
	Action       string `json:"action"`
	StatePath    string `json:"statePath"`
	ResolverPath string `json:"resolverPath"`
}

type Manager interface {
	Install(context.Context, Plan) (Result, error)
	Refresh(context.Context, Plan) (Result, error)
	Uninstall(context.Context, Plan) (Result, error)
}

func PlanInstall(ctx context.Context, admin config.Admin, store tenant.IncusTenantStore, request Request) (Plan, error) {
	return plan(ctx, admin, store, request)
}

func PlanRefresh(ctx context.Context, admin config.Admin, store tenant.IncusTenantStore, request Request) (Plan, error) {
	return plan(ctx, admin, store, request)
}

func PlanUninstall(ctx context.Context, admin config.Admin, store tenant.IncusTenantStore, request Request) (Plan, error) {
	return plan(ctx, admin, store, request)
}

func plan(ctx context.Context, admin config.Admin, store tenant.IncusTenantStore, request Request) (Plan, error) {
	if err := admin.Validate(); err != nil {
		return Plan{}, err
	}
	ref, err := tenantRef(request.Reference, admin.Tenant)
	if err != nil {
		return Plan{}, err
	}
	summaries, err := tenant.List(ctx, store)
	if err != nil {
		return Plan{}, err
	}
	for _, summary := range summaries {
		if summary.Tenant == ref.Tenant {
			endpoint, err := dnsEndpoint(summary.PrivateCIDR)
			if err != nil {
				return Plan{}, err
			}
			platform := runtime.GOOS
			listen := net.JoinHostPort(DefaultListenIP, fmt.Sprint(DefaultListenPort))
			return Plan{
				Reference:        ref.String(),
				IncusProject:     summary.IncusName,
				DNSSuffix:        summary.DNSSuffix,
				DNSEndpoint:      endpoint,
				Listen:           listen,
				StatePath:        statePath(),
				ResolverPath:     ResolverPath(platform, summary.DNSSuffix),
				Platform:         platform,
				ResolverStrategy: ResolverStrategy(platform),
				ResolverCommands: ResolverCommands(platform, summary.DNSSuffix, listen),
			}, nil
		}
	}
	return Plan{}, fmt.Errorf("tenant %q not found", ref.String())
}

func tenantRef(reference string, currentTenant string) (naming.TenantRef, error) {
	value := strings.TrimSpace(reference)
	if value == "" {
		value = strings.TrimSpace(currentTenant)
	}
	if value == "" {
		return naming.TenantRef{}, fmt.Errorf("tenant reference is required")
	}
	return naming.ParseTenantRef(value)
}

func dnsEndpoint(privateCIDR string) (string, error) {
	prefix, err := netipPrefix(privateCIDR)
	if err != nil {
		return "", err
	}
	addr, err := cidr.RoleAddress(prefix, cidr.DNSHostOctet)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(addr.String(), "53"), nil
}

func netipPrefix(value string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return netip.Prefix{}, err
	}
	return prefix, nil
}

func statePath() string {
	return DefaultStatePath()
}

func DefaultStatePath() string {
	if path := os.Getenv("SANDCASTLE_LOCAL_DNS_STATE"); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", "sandcastle-dns.yaml")
	}
	return filepath.Join(home, ".sandcastle", "dns.yaml")
}

func resolverDir() string {
	return DefaultResolverDir()
}

func DefaultResolverDir() string {
	if dir := resolverDirOverride(); dir != "" {
		return dir
	}
	if runtime.GOOS == "darwin" {
		return "/etc/resolver"
	}
	return "/etc/sandcastle/resolver"
}

func resolverDirOverride() string {
	return os.Getenv("SANDCASTLE_RESOLVER_DIR")
}
