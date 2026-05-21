package localtrust

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type Request struct {
	Reference string
}

type Plan struct {
	Reference       string `json:"reference"`
	IncusProject    string `json:"incusProject"`
	DNSSuffix       string `json:"dnsSuffix"`
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

func PlanInstall(ctx context.Context, admin config.Admin, store tenant.IncusTenantStore, request Request) (Plan, error) {
	return plan(ctx, admin, store, request)
}

func PlanUninstall(ctx context.Context, admin config.Admin, store tenant.IncusTenantStore, request Request) (Plan, error) {
	return plan(ctx, admin, store, request)
}

func plan(ctx context.Context, admin config.Admin, store tenant.IncusTenantStore, request Request) (Plan, error) {
	if err := admin.Validate(); err != nil {
		return Plan{}, err
	}
	ref, err := tenantRef(request.Reference, admin.Tenant)
	if err != nil {
		return Plan{}, err
	}
	summaries, err := tenant.List(ctx, store)
	if err != nil {
		return Plan{}, err
	}
	for _, summary := range summaries {
		if summary.Tenant == ref.Tenant {
			return Plan{
				Reference:       ref.String(),
				IncusProject:    summary.IncusName,
				DNSSuffix:       summary.DNSSuffix,
				StoragePool:     summary.IncusName,
				CAVolume:        tenant.CAVolumeName,
				CertificatePath: tenant.TenantCACertPath,
				TrustName:       trustName(ref),
				Platform:        runtime.GOOS,
				Warning:         "Trusting this tenant CA allows the tenant to mint certificates trusted by this machine.",
			}, nil
		}
	}
	return Plan{}, fmt.Errorf("tenant %q not found", ref.String())
}

func tenantRef(reference string, currentTenant string) (naming.TenantRef, error) {
	value := strings.TrimSpace(reference)
	if value == "" {
		value = strings.TrimSpace(currentTenant)
	}
	if value == "" {
		return naming.TenantRef{}, fmt.Errorf("tenant reference is required")
	}
	return naming.ParseTenantRef(value)
}

func trustName(ref naming.TenantRef) string {
	return "Sandcastle " + ref.String() + " tenant CA"
}

func CertFilename(plan Plan) string {
	name := strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(plan.TrustName))
	return name + ".crt"
}
