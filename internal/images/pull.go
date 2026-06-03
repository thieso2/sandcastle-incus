package images

import (
	"context"
	"fmt"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
)

// SourceProject is the Incus project that holds the deployment's published
// Sandcastle Images. Published images are marked public there so restricted
// tenant users can pull them into their own project.
const SourceProject = "default"

type PullRequest struct {
	Remote        string
	TenantProject string   // the user's tenant Incus project (Summary.IncusName)
	Templates     []string // subset of {base, ai}; empty means both
}

type PullPlan struct {
	Remote        string     `json:"remote"`
	TenantProject string     `json:"tenantProject"`
	SourceProject string     `json:"sourceProject"`
	Aliases       []string   `json:"aliases"`
	Commands      [][]string `json:"commands"`
}

type PullResult struct {
	PullPlan
	Pulled []string `json:"pulled"`
}

type Puller interface {
	PullImages(context.Context, PullPlan) (PullResult, error)
}

// PlanPull builds the in-remote copies that refresh a tenant project's image
// aliases from the deployment's published (public) images in the default
// project. The copy is server-side on the same remote, so it is architecture
// independent and works from any client with a restricted tenant certificate.
func PlanPull(admin config.Admin, request PullRequest) (PullPlan, error) {
	if err := admin.Validate(); err != nil {
		return PullPlan{}, err
	}
	remote := firstNonEmpty(strings.TrimSpace(request.Remote), strings.TrimSpace(admin.Remote))
	if remote == "" {
		return PullPlan{}, fmt.Errorf("remote is required")
	}
	project := strings.TrimSpace(request.TenantProject)
	if project == "" {
		return PullPlan{}, fmt.Errorf("tenant project is required")
	}

	templates := request.Templates
	if len(templates) == 0 {
		templates = []string{"base", "ai"}
	}

	plan := PullPlan{
		Remote:        remote,
		TenantProject: project,
		SourceProject: SourceProject,
	}
	for _, template := range templates {
		alias, err := aliasForTemplate(admin, strings.ToLower(strings.TrimSpace(template)))
		if err != nil {
			return PullPlan{}, err
		}
		plan.Aliases = append(plan.Aliases, alias)
		plan.Commands = append(plan.Commands, []string{
			"incus", "image", "copy",
			remote + ":" + alias,
			remote + ":",
			"--project", SourceProject,
			"--target-project", project,
			"--copy-aliases", "--reuse",
		})
	}
	return plan, nil
}

// LocalPuller runs the pull copies over the local incus CLI, which uses the
// caller's (restricted) certificate.
type LocalPuller struct {
	Runner IncusRunner
}

func (p LocalPuller) PullImages(ctx context.Context, plan PullPlan) (PullResult, error) {
	runner := p.Runner
	if runner == nil {
		runner = incusCLIRunner{}
	}
	result := PullResult{PullPlan: plan}
	for i, command := range plan.Commands {
		if _, err := runner.Run(ctx, nil, command[1:]...); err != nil {
			return result, err
		}
		if i < len(plan.Aliases) {
			result.Pulled = append(result.Pulled, plan.Aliases[i])
		}
	}
	return result, nil
}
