package project

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/naming"
)

type Status struct {
	Summary Summary `json:"summary"`
	Checks  []Check `json:"checks"`
}

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func GetStatus(ctx context.Context, store IncusProjectStore, reference string) (Status, error) {
	ref, err := naming.ParseProjectRef(reference)
	if err != nil {
		return Status{}, err
	}
	projects, err := List(ctx, store)
	if err != nil {
		return Status{}, err
	}
	for _, summary := range projects {
		if summary.Owner == ref.Owner && summary.Name == ref.Project {
			return Status{
				Summary: summary,
				Checks: []Check{
					{Name: "metadata", Status: "ok", Detail: "Sandcastle project metadata is present"},
					{Name: "cidr", Status: checkPresent(summary.PrivateCIDR), Detail: summary.PrivateCIDR},
					{Name: "domain", Status: checkPresent(summary.Domain), Detail: summary.Domain},
				},
			}, nil
		}
	}
	return Status{}, fmt.Errorf("Sandcastle project %s not found", ref.String())
}

func checkPresent(value string) string {
	if value == "" {
		return "missing"
	}
	return "ok"
}
