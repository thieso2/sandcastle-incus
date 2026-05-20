package localdns

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/thieso2/sandcastle-incus/internal/naming"
	"gopkg.in/yaml.v2"
)

type FileManager struct{}

type State struct {
	Projects []ProjectState `yaml:"projects" json:"projects"`
}

type ProjectState struct {
	Owner       string        `yaml:"owner" json:"owner"`
	Project     string        `yaml:"project" json:"project"`
	Domain      string        `yaml:"domain" json:"domain"`
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
	return Result{Reference: plan.Reference, Action: "install", StatePath: plan.StatePath, ResolverPath: plan.ResolverPath}, nil
}

func (m FileManager) Refresh(ctx context.Context, plan Plan) (Result, error) {
	if err := writeLocalDNS(plan); err != nil {
		return Result{}, err
	}
	return Result{Reference: plan.Reference, Action: "refresh", StatePath: plan.StatePath, ResolverPath: plan.ResolverPath}, nil
}

func (m FileManager) Uninstall(ctx context.Context, plan Plan) (Result, error) {
	state, err := readState(plan.StatePath)
	if err != nil {
		return Result{}, err
	}
	state.Projects = removeProject(state.Projects, plan.Reference)
	if err := writeState(plan.StatePath, state); err != nil {
		return Result{}, err
	}
	if err := os.Remove(plan.ResolverPath); err != nil && !os.IsNotExist(err) {
		return Result{}, err
	}
	return Result{Reference: plan.Reference, Action: "uninstall", StatePath: plan.StatePath, ResolverPath: plan.ResolverPath}, nil
}

func writeLocalDNS(plan Plan) error {
	state, err := readState(plan.StatePath)
	if err != nil {
		return err
	}
	entry, err := projectState(plan)
	if err != nil {
		return err
	}
	state.Projects = upsertProject(state.Projects, entry)
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
	sort.Slice(state.Projects, func(i, j int) bool {
		return state.Projects[i].Domain < state.Projects[j].Domain
	})
	content, err := yaml.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func projectState(plan Plan) (ProjectState, error) {
	dnsIP, dnsPort, err := net.SplitHostPort(plan.DNSEndpoint)
	if err != nil {
		return ProjectState{}, err
	}
	port, err := strconv.Atoi(dnsPort)
	if err != nil {
		return ProjectState{}, err
	}
	ref, err := naming.ParseProjectRef(plan.Reference)
	if err != nil {
		return ProjectState{}, err
	}
	return ProjectState{
		Owner:   ref.Owner,
		Project: ref.Project,
		Domain:  plan.Domain,
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

func upsertProject(projects []ProjectState, entry ProjectState) []ProjectState {
	for index := range projects {
		if projects[index].Owner == entry.Owner && projects[index].Project == entry.Project {
			projects[index] = entry
			return projects
		}
	}
	return append(projects, entry)
}

func removeProject(projects []ProjectState, reference string) []ProjectState {
	ref, err := naming.ParseProjectRef(reference)
	if err != nil {
		return projects
	}
	output := projects[:0]
	for _, entry := range projects {
		if entry.Owner == ref.Owner && entry.Project == ref.Project {
			continue
		}
		output = append(output, entry)
	}
	return output
}
