package localdns

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/naming"
	"gopkg.in/yaml.v2"
)

type CommandRunner interface {
	Run(context.Context, []string) error
}

type ExecCommandRunner struct{}

func (ExecCommandRunner) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command is empty")
	}
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

type FileManager struct {
	Runner CommandRunner
}

type State struct {
	Tenants []TenantState `yaml:"tenants" json:"tenants"`
}

type TenantState struct {
	Tenant      string        `yaml:"tenant" json:"tenant"`
	DNSSuffix   string        `yaml:"dnsSuffix" json:"dnsSuffix"`
	DNSEndpoint EndpointState `yaml:"dnsEndpoint" json:"dnsEndpoint"`
	Resolver    ResolverState `yaml:"resolver" json:"resolver"`
}

type EndpointState struct {
	IP   string `yaml:"ip" json:"ip"`
	Port int    `yaml:"port" json:"port"`
}

type ResolverState struct {
	Listen string `yaml:"listen" json:"listen"`
}

func (m FileManager) Install(ctx context.Context, plan Plan) (Result, error) {
	if err := writeLocalDNS(plan); err != nil {
		return Result{}, err
	}
	if err := m.syncPlatformResolver(ctx, plan); err != nil {
		return Result{}, err
	}
	return Result{Reference: plan.Reference, Action: "install", StatePath: plan.StatePath, ResolverPath: plan.ResolverPath}, nil
}

func (m FileManager) Refresh(ctx context.Context, plan Plan) (Result, error) {
	if err := writeLocalDNS(plan); err != nil {
		return Result{}, err
	}
	if err := m.syncPlatformResolver(ctx, plan); err != nil {
		return Result{}, err
	}
	return Result{Reference: plan.Reference, Action: "refresh", StatePath: plan.StatePath, ResolverPath: plan.ResolverPath}, nil
}

func (m FileManager) Uninstall(ctx context.Context, plan Plan) (Result, error) {
	state, err := readState(plan.StatePath)
	if err != nil {
		return Result{}, err
	}
	state.Tenants = removeTenant(state.Tenants, plan.Reference)
	if err := writeState(plan.StatePath, state); err != nil {
		return Result{}, err
	}
	if err := os.Remove(plan.ResolverPath); err != nil && !os.IsNotExist(err) {
		return Result{}, err
	}
	if err := m.syncPlatformResolver(ctx, plan); err != nil {
		return Result{}, err
	}
	return Result{Reference: plan.Reference, Action: "uninstall", StatePath: plan.StatePath, ResolverPath: plan.ResolverPath}, nil
}

func (m FileManager) syncPlatformResolver(ctx context.Context, plan Plan) error {
	state, err := readState(plan.StatePath)
	if err != nil {
		return err
	}
	commands := ResolverSyncCommands(plan.ResolverStrategy, state, plan.Listen)
	runner := m.Runner
	if runner == nil {
		runner = ExecCommandRunner{}
	}
	for _, command := range commands {
		if err := runner.Run(ctx, command.Args); err != nil {
			return err
		}
	}
	return nil
}

func writeLocalDNS(plan Plan) error {
	state, err := readState(plan.StatePath)
	if err != nil {
		return err
	}
	entry, err := tenantState(plan)
	if err != nil {
		return err
	}
	state.Tenants = upsertTenant(state.Tenants, entry)
	if err := writeState(plan.StatePath, state); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plan.ResolverPath), 0o755); err != nil {
		return err
	}
	resolver, err := resolverContent(plan)
	if err != nil {
		return err
	}
	return os.WriteFile(plan.ResolverPath, []byte(resolver), 0o644)
}

func readState(path string) (State, error) {
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	if len(content) == 0 {
		return State{}, nil
	}
	var state State
	if err := yaml.Unmarshal(content, &state); err != nil {
		return State{}, err
	}
	return state, nil
}

func writeState(path string, state State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	sort.Slice(state.Tenants, func(i, j int) bool {
		return state.Tenants[i].DNSSuffix < state.Tenants[j].DNSSuffix
	})
	content, err := yaml.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func tenantState(plan Plan) (TenantState, error) {
	dnsIP, dnsPort, err := net.SplitHostPort(plan.DNSEndpoint)
	if err != nil {
		return TenantState{}, err
	}
	port, err := strconv.Atoi(dnsPort)
	if err != nil {
		return TenantState{}, err
	}
	ref, err := naming.ParseTenantRef(plan.Reference)
	if err != nil {
		return TenantState{}, err
	}
	return TenantState{
		Tenant:    ref.Tenant,
		DNSSuffix: plan.DNSSuffix,
		DNSEndpoint: EndpointState{
			IP:   dnsIP,
			Port: port,
		},
		Resolver: ResolverState{
			Listen: plan.Listen,
		},
	}, nil
}

func resolverContent(plan Plan) (string, error) {
	host, port, err := net.SplitHostPort(plan.Listen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("nameserver %s\nport %s\n", host, port), nil
}

func upsertTenant(tenants []TenantState, entry TenantState) []TenantState {
	for index := range tenants {
		if tenants[index].Tenant == entry.Tenant {
			tenants[index] = entry
			return tenants
		}
	}
	return append(tenants, entry)
}

func removeTenant(tenants []TenantState, reference string) []TenantState {
	ref, err := naming.ParseTenantRef(reference)
	if err != nil {
		return tenants
	}
	output := tenants[:0]
	for _, entry := range tenants {
		if entry.Tenant == ref.Tenant {
			continue
		}
		output = append(output, entry)
	}
	return output
}
