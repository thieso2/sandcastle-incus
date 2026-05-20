package tailscale

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

const DefaultAdvertiseTag = "tag:sandcastle"

type UpRequest struct {
	Reference     string
	AuthKey       string
	AdvertiseTags []string
}

type UpPlan struct {
	Reference       string          `json:"reference"`
	Project         project.Summary `json:"project"`
	InstanceName    string          `json:"instanceName"`
	AdvertiseRoutes []string        `json:"advertiseRoutes"`
	AdvertiseTags   []string        `json:"advertiseTags,omitempty"`
	HasAuthKey      bool            `json:"hasAuthKey"`
	AuthKey         string          `json:"-"`
	Command         []string        `json:"command"`
}

type RunSession struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type Runner interface {
	RunUp(context.Context, UpPlan, RunSession) error
}

func PlanUp(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request UpRequest) (UpPlan, error) {
	if err := admin.Validate(); err != nil {
		return UpPlan{}, err
	}
	ref, err := naming.ParseProjectRef(request.Reference)
	if err != nil {
		return UpPlan{}, err
	}
	summary, err := findProject(ctx, store, ref)
	if err != nil {
		return UpPlan{}, err
	}
	tags := normalizeTags(request.AdvertiseTags)
	plan := UpPlan{
		Reference:       ref.String(),
		Project:         summary,
		InstanceName:    project.TailscaleName,
		AdvertiseRoutes: []string{summary.PrivateCIDR},
		AdvertiseTags:   tags,
		HasAuthKey:      strings.TrimSpace(request.AuthKey) != "",
		AuthKey:         strings.TrimSpace(request.AuthKey),
	}
	plan.Command = command(plan, true)
	return plan, nil
}

func ExecCommand(plan UpPlan) []string {
	return command(plan, false)
}

func command(plan UpPlan, redact bool) []string {
	args := []string{"tailscale", "up", "--advertise-routes=" + strings.Join(plan.AdvertiseRoutes, ",")}
	if len(plan.AdvertiseTags) > 0 {
		args = append(args, "--advertise-tags="+strings.Join(plan.AdvertiseTags, ","))
	}
	if plan.AuthKey != "" {
		authKey := plan.AuthKey
		if redact {
			authKey = "<redacted>"
		}
		args = append(args, "--auth-key="+authKey)
	}
	return args
}

func normalizeTags(values []string) []string {
	seen := map[string]bool{}
	output := []string{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			tag := strings.TrimSpace(part)
			if tag == "" || seen[tag] {
				continue
			}
			seen[tag] = true
			output = append(output, tag)
		}
	}
	return output
}

func findProject(ctx context.Context, store project.IncusProjectStore, ref naming.ProjectRef) (project.Summary, error) {
	projects, err := project.List(ctx, store)
	if err != nil {
		return project.Summary{}, err
	}
	for _, summary := range projects {
		if summary.Owner == ref.Owner && summary.Name == ref.Project {
			if summary.PrivateCIDR == "" {
				return project.Summary{}, fmt.Errorf("Sandcastle project %s has no private CIDR", ref.String())
			}
			return summary, nil
		}
	}
	return project.Summary{}, fmt.Errorf("Sandcastle project %s not found", ref.String())
}
