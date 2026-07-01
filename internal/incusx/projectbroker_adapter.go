package incusx

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/projectbroker"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

// ProjectBrokerCreator implements projectbroker.ProjectCreator: it performs the
// privileged v2 project scaffolding (CreateProjectV2) and extends the tenant's
// restricted certificate to include the new project so the tenant's native
// incus client can immediately use it.
type ProjectBrokerCreator struct {
	Creator TenantCreator
	Trust   usertrust.Manager
}

func (p ProjectBrokerCreator) CreateTenantProject(ctx context.Context, tenant string, project string) (projectbroker.ProjectResult, error) {
	res, err := p.Creator.CreateProjectV2(ctx, tenant, project)
	if err != nil {
		return projectbroker.ProjectResult{}, err
	}
	if p.Trust != nil {
		if err := p.Trust.Grant(ctx, usertrust.UserPlan{
			User:            tenant,
			CertificateName: usertrust.RestrictedName(tenant),
			RemoteName:      usertrust.RestrictedName(tenant),
			Restricted:      true,
			Projects:        []string{res.IncusProject},
			Description:     "Sandcastle v2 tenant " + tenant,
		}); err != nil {
			return projectbroker.ProjectResult{}, fmt.Errorf("extend tenant certificate for %s: %w", res.IncusProject, err)
		}
	}
	return projectbroker.ProjectResult{
		Tenant:       res.Tenant,
		Project:      res.Project,
		IncusProject: res.IncusProject,
		Bridge:       res.Bridge,
		DNSSuffix:    res.DNSSuffix,
	}, nil
}
