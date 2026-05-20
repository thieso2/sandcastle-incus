package localtrust

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type Request struct {
	Reference string
}

type Plan struct {
	Reference       string `json:"reference"`
	IncusProject    string `json:"incusProject"`
	Domain          string `json:"domain"`
	StoragePool     string `json:"storagePool"`
	CAVolume        string `json:"caVolume"`
	CertificatePath string `json:"certificatePath"`
	TrustName       string `json:"trustName"`
	Platform        string `json:"platform"`
	Warning         string `json:"warning"`
}

type Result struct {
	Reference string `json:"reference"`
	TrustName string `json:"trustName"`
	Platform  string `json:"platform"`
	Action    string `json:"action"`
	Target    string `json:"target,omitempty"`
}

type Manager interface {
	Install(context.Context, Plan) (Result, error)
	Uninstall(context.Context, Plan) (Result, error)
}

func PlanInstall(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request Request) (Plan, error) {
	return plan(ctx, admin, store, request)
}

func PlanUninstall(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request Request) (Plan, error) {
	return plan(ctx, admin, store, request)
}

func plan(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request Request) (Plan, error) {
	if err := admin.Validate(); err != nil {
		return Plan{}, err
	}
	ref, err := naming.ParseProjectRefWithDefaultOwner(request.Reference, admin.Owner)
	if err != nil {
		return Plan{}, err
	}
	summaries, err := project.List(ctx, store)
	if err != nil {
		return Plan{}, err
	}
	for _, summary := range summaries {
		if summary.Owner == ref.Owner && summary.Name == ref.Project {
			return Plan{
				Reference:       ref.String(),
				IncusProject:    summary.IncusName,
				Domain:          summary.Domain,
				StoragePool:     summary.IncusName,
				CAVolume:        project.CAVolumeName,
				CertificatePath: project.ProjectCACertPath,
				TrustName:       trustName(ref),
				Platform:        runtime.GOOS,
				Warning:         "Trusting this project CA allows the project to mint certificates trusted by this machine.",
			}, nil
		}
	}
	return Plan{}, fmt.Errorf("project %q not found", ref.String())
}

func trustName(ref naming.ProjectRef) string {
	return "Sandcastle " + ref.String() + " project CA"
}

func CertFilename(plan Plan) string {
	name := strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(plan.TrustName))
	return name + ".crt"
}
