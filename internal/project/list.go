package project

import (
	"context"
	"fmt"
	"sort"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

type IncusProject struct {
	Name   string
	Config map[string]string
}

type IncusProjectStore interface {
	ListProjects(ctx context.Context) ([]IncusProject, error)
}

type Summary struct {
	IncusName       string `json:"incusName"`
	Owner           string `json:"owner"`
	Name            string `json:"name"`
	Domain          string `json:"domain,omitempty"`
	PrivateCIDR     string `json:"privateCIDR,omitempty"`
	DefaultTemplate string `json:"defaultTemplate,omitempty"`
	Status          string `json:"status"`
}

func List(ctx context.Context, store IncusProjectStore) ([]Summary, error) {
	projects, err := store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	summaries := make([]Summary, 0, len(projects))
	for _, incusProject := range projects {
		if !meta.IsManaged(incusProject.Config) {
			continue
		}
		project, err := meta.ParseProjectConfig(incusProject.Config)
		if err != nil {
			return nil, fmt.Errorf("parse project metadata for %s: %w", incusProject.Name, err)
		}
		summaries = append(summaries, Summary{
			IncusName:       incusProject.Name,
			Owner:           project.Owner,
			Name:            project.Project,
			Domain:          project.Domain,
			PrivateCIDR:     project.PrivateCIDR,
			DefaultTemplate: project.DefaultTemplate,
			Status:          "managed",
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Owner == summaries[j].Owner {
			return summaries[i].Name < summaries[j].Name
		}
		return summaries[i].Owner < summaries[j].Owner
	})
	return summaries, nil
}

type MemoryStore struct {
	Projects []IncusProject
}

func (s MemoryStore) ListProjects(ctx context.Context) ([]IncusProject, error) {
	return s.Projects, nil
}
