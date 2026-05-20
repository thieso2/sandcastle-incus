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
	RunStatus(context.Context, StatusPlan, RunSession) (StatusResult, error)
	RunDown(context.Context, DownPlan, RunSession) error
}

func PlanUp(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request UpRequest) (UpPlan, error) {
	if err := admin.Validate(); err != nil {
		return UpPlan{}, err
	}
	ref, err := naming.ParseProjectRefWithDefaultOwner(request.Reference, admin.Owner)
	if err != nil {
		return UpPlan{}, err
	}
	summary, err := findProject(ctx, store, ref)
	if err != nil {
		return UpPlan{}, err
	}
	tags, err := NormalizeAdvertiseTags(request.AdvertiseTags)
	if err != nil {
		return UpPlan{}, err
	}
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
	if redact {
		return args
	}
	return []string{"/bin/sh", "-lc", tailscaledBootstrapScript() + "; exec " + strings.Join(shellQuoteArgs(args), " ")}
}

func tailscaledBootstrapScript() string {
	return strings.Join([]string{
		"install -d /run/tailscale /var/lib/tailscale",
		"ethtool -K eth0 rx-udp-gro-forwarding on rx-gro-list off 2>/dev/null || true",
		"if ! pgrep -x tailscaled >/dev/null 2>&1; then tailscaled --state=/var/lib/tailscale/tailscaled.state >/var/log/tailscaled.log 2>&1 & fi",
		"for i in $(seq 1 50); do tailscale status >/dev/null 2>&1 && break; sleep 0.1; done",
	}, "; ")
}

func shellQuoteArgs(args []string) []string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	return quoted
}

func NormalizeAdvertiseTags(values []string) ([]string, error) {
	seen := map[string]bool{}
	output := []string{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			tag := strings.TrimSpace(part)
			if tag == "" || seen[tag] {
				continue
			}
			if err := validateAdvertiseTag(tag); err != nil {
				return nil, err
			}
			seen[tag] = true
			output = append(output, tag)
		}
	}
	return output, nil
}

func validateAdvertiseTag(tag string) error {
	if !strings.HasPrefix(tag, "tag:") || len(tag) == len("tag:") {
		return fmt.Errorf("Tailscale advertise tag %q must use tag:<name>", tag)
	}
	name := strings.TrimPrefix(tag, "tag:")
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return fmt.Errorf("Tailscale advertise tag %q must contain only lowercase letters, digits, or hyphens after tag:", tag)
		}
	}
	return nil
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
