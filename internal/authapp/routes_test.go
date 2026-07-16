package authapp

import (
	"context"
	"errors"
	"testing"
)

func sampleRoute(hostname, tenant, machine string, port int) Route {
	return Route{Hostname: hostname, Tenant: tenant, Project: "default", Machine: machine, BackendPort: port}
}

func TestUpsertRoute_NewStoresAndAllocatesLocalPort(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)
	got, err := UpsertRoute(ctx, db, sampleRoute("web.acme.sc2.dev", "acme", "web", 3000))
	if err != nil {
		t.Fatal(err)
	}
	if got.LocalPort < localPortBase || got.LocalPort >= localPortCeiling {
		t.Fatalf("local port %d out of range", got.LocalPort)
	}
	stored, found, err := GetRoute(ctx, db, "web.acme.sc2.dev")
	if err != nil || !found {
		t.Fatalf("get after upsert: found=%v err=%v", found, err)
	}
	if stored.Machine != "web" || stored.BackendPort != 3000 || stored.Tenant != "acme" {
		t.Fatalf("stored route wrong: %+v", stored)
	}
}

func TestUpsertRoute_HostnameCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)
	if _, err := UpsertRoute(ctx, db, sampleRoute("Web.Acme.SC2.dev", "acme", "web", 3000)); err != nil {
		t.Fatal(err)
	}
	_, found, err := GetRoute(ctx, db, "web.acme.sc2.dev")
	if err != nil || !found {
		t.Fatalf("expected case-insensitive hit, found=%v err=%v", found, err)
	}
}

func TestUpsertRoute_IdempotentRepublish(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)
	first, err := UpsertRoute(ctx, db, sampleRoute("web.acme.sc2.dev", "acme", "web", 3000))
	if err != nil {
		t.Fatal(err)
	}
	second, err := UpsertRoute(ctx, db, sampleRoute("web.acme.sc2.dev", "acme", "web", 3000))
	if err != nil {
		t.Fatalf("idempotent re-publish should not error: %v", err)
	}
	if second.LocalPort != first.LocalPort {
		t.Fatalf("local port changed on re-publish: %d -> %d", first.LocalPort, second.LocalPort)
	}
	all, _ := ListRoutes(ctx, db)
	if len(all) != 1 {
		t.Fatalf("expected 1 route after idempotent re-publish, got %d", len(all))
	}
}

func TestUpsertRoute_SameTenantDifferentBackendConflicts(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)
	if _, err := UpsertRoute(ctx, db, sampleRoute("web.acme.sc2.dev", "acme", "web", 3000)); err != nil {
		t.Fatal(err)
	}
	_, err := UpsertRoute(ctx, db, sampleRoute("web.acme.sc2.dev", "acme", "api", 3000))
	var conflict *RouteConflictError
	if !errors.As(err, &conflict) || conflict.CrossTenant {
		t.Fatalf("expected same-tenant RouteConflictError, got %v", err)
	}
	if conflict.ExistingMachine != "web" {
		t.Fatalf("conflict should name existing machine, got %q", conflict.ExistingMachine)
	}
}

func TestUpsertRoute_CrossTenantConflicts(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)
	if _, err := UpsertRoute(ctx, db, sampleRoute("app.customer.com", "acme", "web", 3000)); err != nil {
		t.Fatal(err)
	}
	_, err := UpsertRoute(ctx, db, sampleRoute("app.customer.com", "globex", "web", 3000))
	var conflict *RouteConflictError
	if !errors.As(err, &conflict) || !conflict.CrossTenant {
		t.Fatalf("expected cross-tenant RouteConflictError, got %v", err)
	}
}

func TestUpsertRoute_DistinctLocalPorts(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)
	a, _ := UpsertRoute(ctx, db, sampleRoute("a.acme.sc2.dev", "acme", "a", 3000))
	b, _ := UpsertRoute(ctx, db, sampleRoute("b.acme.sc2.dev", "acme", "b", 3000))
	if a.LocalPort == b.LocalPort {
		t.Fatalf("two routes got the same local port %d", a.LocalPort)
	}
}

func TestListRoutesByTenant_ScopesToTenant(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)
	UpsertRoute(ctx, db, sampleRoute("a.acme.sc2.dev", "acme", "a", 3000))
	UpsertRoute(ctx, db, sampleRoute("b.globex.sc2.dev", "globex", "b", 3000))
	acme, err := ListRoutesByTenant(ctx, db, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(acme) != 1 || acme[0].Tenant != "acme" {
		t.Fatalf("expected only acme routes, got %+v", acme)
	}
}

func TestDeleteRoute_OwnerOnly(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)
	UpsertRoute(ctx, db, sampleRoute("web.acme.sc2.dev", "acme", "web", 3000))
	if _, err := DeleteRoute(ctx, db, "web.acme.sc2.dev", "globex"); err == nil {
		t.Fatal("expected error deleting another tenant's route")
	}
	removed, err := DeleteRoute(ctx, db, "web.acme.sc2.dev", "acme")
	if err != nil {
		t.Fatalf("owner delete failed: %v", err)
	}
	if removed.Machine != "web" {
		t.Fatalf("delete should return the removed route, got %+v", removed)
	}
	if _, found, _ := GetRoute(ctx, db, "web.acme.sc2.dev"); found {
		t.Fatal("route still present after delete")
	}
}

func TestRouteHostnameRegistered_AskGate(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)
	UpsertRoute(ctx, db, sampleRoute("web.acme.sc2.dev", "acme", "web", 3000))
	if ok, _ := RouteHostnameRegistered(ctx, db, "web.acme.sc2.dev"); !ok {
		t.Fatal("registered hostname should gate open")
	}
	if ok, _ := RouteHostnameRegistered(ctx, db, "evil.acme.sc2.dev"); ok {
		t.Fatal("unregistered hostname must not gate open")
	}
}
