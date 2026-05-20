package routebroker

import (
	"context"
	"fmt"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

type Principal struct {
	Fingerprint string `json:"fingerprint"`
	Owner       string `json:"owner"`
}

type TrustMapper interface {
	OwnerForFingerprint(context.Context, string) (string, error)
}

func PrincipalFromFingerprint(ctx context.Context, mapper TrustMapper, fingerprint string) (Principal, error) {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return Principal{}, fmt.Errorf("client certificate fingerprint is required")
	}
	if mapper == nil {
		return Principal{}, fmt.Errorf("trust mapper is required")
	}
	owner, err := mapper.OwnerForFingerprint(ctx, fingerprint)
	if err != nil {
		return Principal{}, err
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return Principal{}, fmt.Errorf("client certificate %s is not mapped to a Sandcastle owner", fingerprint)
	}
	return Principal{Fingerprint: fingerprint, Owner: owner}, nil
}

func AuthorizeAdd(principal Principal, plan route.AddPlan) error {
	if strings.TrimSpace(principal.Owner) == "" {
		return fmt.Errorf("route principal owner is required")
	}
	if principal.Owner != plan.Project.Owner {
		return fmt.Errorf("owner %s cannot route sandbox owned by %s", principal.Owner, plan.Project.Owner)
	}
	return nil
}

func AuthorizeRemove(principal Principal, routeMetadata meta.Route) error {
	if strings.TrimSpace(principal.Owner) == "" {
		return fmt.Errorf("route principal owner is required")
	}
	if principal.Owner != routeMetadata.TargetOwner {
		return fmt.Errorf("owner %s cannot remove route owned by %s", principal.Owner, routeMetadata.TargetOwner)
	}
	return nil
}
