package localdns

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"

	"github.com/thieso2/sandcastle-incus/internal/cidr"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/project"
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
	Domain       string `json:"domain"`
	DNSEndpoint  string `json:"dnsEndpoint"`
	Listen       string `json:"listen"`
	StatePath    string `json:"statePath"`
	ResolverPath string `json:"resolverPath"`
	Platform     string `json:"platform"`
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

func PlanInstall(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request Request) (Plan, error) {
	return plan(ctx, admin, store, request)
}

func PlanRefresh(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request Request) (Plan, error) {
	return plan(ctx, admin, store, request)
}

func PlanUninstall(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request Request) (Plan, error) {
	return plan(ctx, admin, store, request)
}

func plan(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request Request) (Plan, error) {
	if err := admin.Validate(); err != nil {
		return Plan{}, err
	}
	ref, err := naming.ParseProjectRef(request.Reference)
	if err != nil {
		return Plan{}, err
	}
	summaries, err := project.List(ctx, store)
	if err != nil {
		return Plan{}, err
	}
	for _, summary := range summaries {
		if summary.Owner == ref.Owner && summary.Name == ref.Project {
			endpoint, err := dnsEndpoint(summary.PrivateCIDR)
			if err != nil {
				return Plan{}, err
			}
			return Plan{
				Reference:    ref.String(),
				IncusProject: summary.IncusName,
				Domain:       summary.Domain,
				DNSEndpoint:  endpoint,
				Listen:       net.JoinHostPort(DefaultListenIP, fmt.Sprint(DefaultListenPort)),
				StatePath:    statePath(),
				ResolverPath: filepath.Join(resolverDir(), summary.Domain),
				Platform:     runtime.GOOS,
			}, nil
		}
	}
	return Plan{}, fmt.Errorf("project %q not found", ref.String())
}

func dnsEndpoint(privateCIDR string) (string, error) {
	prefix, err := netipPrefix(privateCIDR)
	if err != nil {
		return "", err
	}
	addr, err := cidr.RoleAddress(prefix, 53)
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
	if dir := os.Getenv("SANDCASTLE_RESOLVER_DIR"); dir != "" {
		return dir
	}
	if runtime.GOOS == "darwin" {
		return "/etc/resolver"
	}
	return "/etc/sandcastle/resolver"
}
