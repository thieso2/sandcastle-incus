package share

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenantpkg "github.com/thieso2/sandcastle-incus/internal/tenant"
)

const (
	RecipientStatePending  = "pending"
	RecipientStateAccepted = "accepted"
	RecipientStateDeclined = "declined"
	AvailabilityAvailable  = "available"
)

type Store interface {
	GetTenantShares(ctx context.Context, incusProjectName string) ([]meta.TenantStorageShare, error)
	SetTenantShares(ctx context.Context, incusProjectName string, shares []meta.TenantStorageShare) error
	SourceDirectoryExists(ctx context.Context, incusProjectName string, project string, workspaceRelativeDir string) (bool, error)
}

type CreateRequest struct {
	SourceTenant string   `json:"source_tenant,omitempty"`
	Source       string   `json:"source"`
	Recipients   []string `json:"recipients"`
	Name         string   `json:"name,omitempty"`
	Actor        string   `json:"actor,omitempty"`
	DryRun       bool     `json:"dry_run,omitempty"`
	Now          string   `json:"now,omitempty"`
}

type ListRequest struct {
	Tenant   string `json:"tenant"`
	Outbound bool   `json:"outbound"`
}

type StatusRequest struct {
	Tenant  string `json:"tenant"`
	Project string `json:"project"`
	Name    string `json:"name"`
}

type Result struct {
	Share  meta.TenantStorageShare   `json:"share,omitempty"`
	Shares []meta.TenantStorageShare `json:"shares,omitempty"`
	DryRun bool                      `json:"dry_run,omitempty"`
}

func PlanCreate(ctx context.Context, tenants tenantpkg.IncusTenantStore, store Store, request CreateRequest) (Result, error) {
	if tenants == nil {
		return Result{}, fmt.Errorf("tenant store is required")
	}
	if store == nil {
		return Result{}, fmt.Errorf("share store is required")
	}
	sourceTenant := strings.TrimSpace(request.SourceTenant)
	if sourceTenant == "" {
		return Result{}, fmt.Errorf("source tenant is required")
	}
	if err := naming.ValidateTenantName(sourceTenant); err != nil {
		return Result{}, err
	}
	summaries, err := tenantpkg.List(ctx, tenants)
	if err != nil {
		return Result{}, err
	}
	source, ok := findTenant(summaries, sourceTenant)
	if !ok {
		return Result{}, fmt.Errorf("source tenant %s not found", sourceTenant)
	}
	project, dir, err := parseSource(request.Source)
	if err != nil {
		return Result{}, err
	}
	if !tenantHasProject(source, project) {
		return Result{}, fmt.Errorf("Sandcastle project %s not found in tenant %s", project, source.Tenant)
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = path.Base(dir)
		if err := ValidateShareName(name); err != nil {
			return Result{}, fmt.Errorf("source directory basename %q is not a valid share name; pass --name", name)
		}
	} else if err := ValidateShareName(name); err != nil {
		return Result{}, err
	}
	recipients, err := normalizeRecipients(request.Recipients, sourceTenant, summaries)
	if err != nil {
		return Result{}, err
	}
	exists, err := store.SourceDirectoryExists(ctx, source.IncusName, project, dir)
	if err != nil {
		return Result{}, err
	}
	if !exists {
		return Result{}, fmt.Errorf("source directory %s:/workspace/%s does not exist", project, dir)
	}
	existing, err := store.GetTenantShares(ctx, source.IncusName)
	if err != nil {
		return Result{}, err
	}
	for _, candidate := range existing {
		if candidate.SourceProject == project && candidate.Name == name {
			return Result{}, fmt.Errorf("Tenant Storage Share %s/%s already exists in tenant %s", project, name, source.Tenant)
		}
	}
	now := strings.TrimSpace(request.Now)
	if now == "" {
		now = time.Now().UTC().Format(time.RFC3339)
	}
	actor := strings.TrimSpace(request.Actor)
	recipientsState := make([]meta.TenantStorageShareRecipient, 0, len(recipients))
	for _, recipient := range recipients {
		recipientsState = append(recipientsState, meta.TenantStorageShareRecipient{
			Tenant:    recipient,
			State:     RecipientStatePending,
			OfferedBy: actor,
			OfferedAt: now,
		})
	}
	created := meta.TenantStorageShare{
		SourceTenant:  source.Tenant,
		SourceProject: project,
		SourceDir:     dir,
		Name:          name,
		Availability:  AvailabilityAvailable,
		CreatedBy:     actor,
		CreatedAt:     now,
		Recipients:    recipientsState,
	}
	output := append([]meta.TenantStorageShare{}, existing...)
	output = append(output, created)
	sortShares(output)
	if !request.DryRun {
		if err := store.SetTenantShares(ctx, source.IncusName, output); err != nil {
			return Result{}, err
		}
	}
	return Result{Share: created, DryRun: request.DryRun}, nil
}

func ListOutbound(ctx context.Context, tenants tenantpkg.IncusTenantStore, store Store, request ListRequest) (Result, error) {
	summary, err := resolveTenant(ctx, tenants, request.Tenant)
	if err != nil {
		return Result{}, err
	}
	shares, err := store.GetTenantShares(ctx, summary.IncusName)
	if err != nil {
		return Result{}, err
	}
	sortShares(shares)
	return Result{Shares: shares}, nil
}

func GetOutbound(ctx context.Context, tenants tenantpkg.IncusTenantStore, store Store, request StatusRequest) (Result, error) {
	summary, err := resolveTenant(ctx, tenants, request.Tenant)
	if err != nil {
		return Result{}, err
	}
	project := strings.TrimSpace(request.Project)
	if err := naming.ValidateProjectName(project); err != nil {
		return Result{}, err
	}
	name := strings.TrimSpace(request.Name)
	if err := ValidateShareName(name); err != nil {
		return Result{}, err
	}
	shares, err := store.GetTenantShares(ctx, summary.IncusName)
	if err != nil {
		return Result{}, err
	}
	for _, candidate := range shares {
		if candidate.SourceProject == project && candidate.Name == name {
			return Result{Share: candidate}, nil
		}
	}
	return Result{}, fmt.Errorf("Tenant Storage Share %s/%s not found in tenant %s", project, name, summary.Tenant)
}

func ValidateShareName(name string) error {
	if err := naming.ValidateProjectName(strings.TrimSpace(name)); err != nil {
		return fmt.Errorf("invalid share name %q", name)
	}
	if strings.Contains(name, ".") || strings.Contains(name, "/") {
		return fmt.Errorf("invalid share name %q", name)
	}
	return nil
}

func ParseStatusRef(value string) (string, string, error) {
	project, name, ok := strings.Cut(strings.TrimSpace(value), "/")
	if !ok {
		return "", "", fmt.Errorf("share reference must be project/share-name")
	}
	if err := naming.ValidateProjectName(project); err != nil {
		return "", "", err
	}
	if err := ValidateShareName(name); err != nil {
		return "", "", err
	}
	return project, name, nil
}

func parseSource(value string) (string, string, error) {
	project, rawDir, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok {
		return "", "", fmt.Errorf("share source must be project:/workspace/dir or project:dir")
	}
	if err := naming.ValidateProjectName(project); err != nil {
		return "", "", err
	}
	rawDir = strings.TrimSpace(rawDir)
	rawDir = strings.TrimPrefix(rawDir, "/workspace/")
	if rawDir == "" || rawDir == "/workspace" {
		return "", "", fmt.Errorf("share source must be below /workspace")
	}
	if strings.Contains(rawDir, "\\") || path.IsAbs(rawDir) {
		return "", "", fmt.Errorf("share source directory must be relative to /workspace")
	}
	for _, segment := range strings.Split(rawDir, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", "", fmt.Errorf("share source directory %q must not contain empty, . or .. segments", rawDir)
		}
	}
	cleaned := path.Clean(rawDir)
	if cleaned == "." {
		return "", "", fmt.Errorf("share source must be below /workspace")
	}
	return project, cleaned, nil
}

func normalizeRecipients(input []string, sourceTenant string, summaries []tenantpkg.Summary) ([]string, error) {
	seen := map[string]bool{}
	output := make([]string, 0, len(input))
	for _, raw := range input {
		recipient := strings.TrimSpace(raw)
		if err := naming.ValidateTenantName(recipient); err != nil {
			return nil, err
		}
		if recipient == sourceTenant {
			return nil, fmt.Errorf("source tenant cannot offer a share to itself")
		}
		if _, ok := findTenant(summaries, recipient); !ok {
			return nil, fmt.Errorf("recipient tenant %s not found", recipient)
		}
		if seen[recipient] {
			continue
		}
		seen[recipient] = true
		output = append(output, recipient)
	}
	if len(output) == 0 {
		return nil, fmt.Errorf("at least one recipient tenant is required")
	}
	sort.Strings(output)
	return output, nil
}

func resolveTenant(ctx context.Context, tenants tenantpkg.IncusTenantStore, tenantName string) (tenantpkg.Summary, error) {
	summaries, err := tenantpkg.List(ctx, tenants)
	if err != nil {
		return tenantpkg.Summary{}, err
	}
	tenantName = strings.TrimSpace(tenantName)
	if err := naming.ValidateTenantName(tenantName); err != nil {
		return tenantpkg.Summary{}, err
	}
	if summary, ok := findTenant(summaries, tenantName); ok {
		return summary, nil
	}
	return tenantpkg.Summary{}, fmt.Errorf("Sandcastle tenant %s not found", tenantName)
}

func findTenant(summaries []tenantpkg.Summary, tenantName string) (tenantpkg.Summary, bool) {
	for _, summary := range summaries {
		if summary.Tenant == tenantName {
			return summary, true
		}
	}
	return tenantpkg.Summary{}, false
}

func tenantHasProject(summary tenantpkg.Summary, project string) bool {
	for _, candidate := range summary.Projects {
		if candidate.Name == project {
			return true
		}
	}
	return false
}

func sortShares(shares []meta.TenantStorageShare) {
	sort.Slice(shares, func(i, j int) bool {
		if shares[i].SourceProject != shares[j].SourceProject {
			return shares[i].SourceProject < shares[j].SourceProject
		}
		return shares[i].Name < shares[j].Name
	})
}
