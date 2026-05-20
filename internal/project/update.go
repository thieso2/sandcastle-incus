package project

import (
	"context"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
)

type SSHKeyUpdater interface {
	SetProjectSSHKey(ctx context.Context, incusProjectName string, sshKey string) error
}

type ResolvedRef struct {
	IncusProject string
}

func ParseRef(admin config.Admin, reference string) (ResolvedRef, error) {
	ref, err := naming.ParseProjectRef(reference)
	if err != nil {
		return ResolvedRef{}, err
	}
	incusName, err := naming.IncusProjectNameWithPrefix(admin.ProjectPrefix, ref)
	if err != nil {
		return ResolvedRef{}, err
	}
	return ResolvedRef{IncusProject: incusName}, nil
}
