package share

import (
	"context"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenantpkg "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestPlanCreateCreatesPendingOutboundOffer(t *testing.T) {
	store := &fakeStore{exists: true}
	result, err := PlanCreate(context.Background(), tenantStore(), store, CreateRequest{
		SourceTenant: "acme",
		Source:       "default:/workspace/docs",
		Recipients:   []string{"skorfman"},
		Actor:        "thieso2",
		Now:          "2026-05-27T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Share.SourceTenant != "acme" || result.Share.SourceProject != "default" || result.Share.SourceDir != "docs" || result.Share.Name != "docs" {
		t.Fatalf("share = %#v", result.Share)
	}
	if len(result.Share.Recipients) != 1 || result.Share.Recipients[0].Tenant != "skorfman" || result.Share.Recipients[0].State != RecipientStatePending {
		t.Fatalf("recipients = %#v", result.Share.Recipients)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved = %#v", store.saved)
	}
}

func TestPlanCreateRejectsSelfShare(t *testing.T) {
	_, err := PlanCreate(context.Background(), tenantStore(), &fakeStore{exists: true}, CreateRequest{
		SourceTenant: "acme",
		Source:       "default:docs",
		Recipients:   []string{"acme"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanCreateRequiresNameWhenBasenameIsNotPathSafe(t *testing.T) {
	_, err := PlanCreate(context.Background(), tenantStore(), &fakeStore{exists: true}, CreateRequest{
		SourceTenant: "acme",
		Source:       "default:Docs",
		Recipients:   []string{"skorfman"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanCreateDryRunDoesNotSave(t *testing.T) {
	store := &fakeStore{exists: true}
	result, err := PlanCreate(context.Background(), tenantStore(), store, CreateRequest{
		SourceTenant: "acme",
		Source:       "default:docs",
		Recipients:   []string{"skorfman"},
		DryRun:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DryRun {
		t.Fatal("DryRun = false")
	}
	if len(store.saved) != 0 {
		t.Fatalf("saved = %#v", store.saved)
	}
}

type fakeStore struct {
	shares []meta.TenantStorageShare
	saved  []meta.TenantStorageShare
	exists bool
}

func (s *fakeStore) GetTenantShares(ctx context.Context, incusProjectName string) ([]meta.TenantStorageShare, error) {
	return append([]meta.TenantStorageShare{}, s.shares...), nil
}

func (s *fakeStore) SetTenantShares(ctx context.Context, incusProjectName string, shares []meta.TenantStorageShare) error {
	s.saved = append([]meta.TenantStorageShare{}, shares...)
	return nil
}

func (s *fakeStore) SourceDirectoryExists(ctx context.Context, incusProjectName string, project string, workspaceRelativeDir string) (bool, error) {
	return s.exists, nil
}

func tenantStore() tenantpkg.MemoryStore {
	acmeConfig, _ := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	skorfmanConfig, _ := meta.TenantConfig(meta.Tenant{
		Tenant:      "skorfman",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.1.0/24",
	})
	return tenantpkg.MemoryStore{Projects: []tenantpkg.IncusProject{
		{Name: "sc-acme", Config: acmeConfig},
		{Name: "sc-skorfman", Config: skorfmanConfig},
	}}
}
