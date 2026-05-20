package project

import (
	"context"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
)

type DeleteRequest struct {
	Reference string
	Purge     bool
}

type DeletePlan struct {
	Reference         string   `json:"reference"`
	IncusProject      string   `json:"incusProject"`
	PrivateNetwork    string   `json:"privateNetwork"`
	StoragePool       string   `json:"storagePool"`
	DurableVolumes    []string `json:"durableVolumes"`
	SidecarInstances  []string `json:"sidecarInstances"`
	PurgeDurableState bool     `json:"purgeDurableState"`
}

type Deleter interface {
	DeleteProject(context.Context, DeletePlan) error
}

func PlanDelete(admin config.Admin, request DeleteRequest) (DeletePlan, error) {
	if err := admin.Validate(); err != nil {
		return DeletePlan{}, err
	}
	ref, err := naming.ParseProjectRef(request.Reference)
	if err != nil {
		return DeletePlan{}, err
	}
	incusName, err := naming.IncusProjectNameWithPrefix(admin.ProjectPrefix, ref)
	if err != nil {
		return DeletePlan{}, err
	}
	return DeletePlan{
		Reference:         ref.String(),
		IncusProject:      incusName,
		PrivateNetwork:    PrivateNetworkName(incusName),
		StoragePool:       admin.StoragePool,
		DurableVolumes:    []string{HomeVolumeName, WorkspaceVolumeName, CAVolumeName},
		SidecarInstances:  []string{TailscaleInstanceName(incusName), DNSName},
		PurgeDurableState: request.Purge,
	}, nil
}
