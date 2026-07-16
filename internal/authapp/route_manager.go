package authapp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"
)

// Route status values reported by `sc route list`/`status` (Spec #111).
const (
	RouteStatusLive        = "live"         // machine running and serving
	RouteStatusUnhealthy   = "unhealthy"    // backing machine stopped or unreachable
	RouteStatusAwaitingDNS = "awaiting-dns" // custom hostname not yet resolving to the front door
)

// MachineState is a Machine's live state as the RouteBackend sees it, used by
// publish (needs the IP) and by the reconcile (prune/keep/refresh).
type MachineState struct {
	Present bool   // the instance exists in Incus
	Running bool   // it is running
	IPv4    string // its current private IPv4 (empty unless running)
}

// RouteBackend is the single injected Incus seam for Public Routes: the Auth App
// must not import incusx, so incusx implements this and it is faked in tests
// (mirroring the Provisioner's tenant.IncusTenantStore injection). It covers
// exactly the Incus operations a Route needs.
type RouteBackend interface {
	// EnsureProxyDevice creates or updates the per-Route proxy device on the
	// Auth App instance: it listens on 127.0.0.1:localPort inside the instance
	// and connects to machineIP:backendPort in the host namespace (which routes
	// to the tenant bridge on a single host). Idempotent.
	EnsureProxyDevice(ctx context.Context, deviceName string, localPort int, machineIP string, backendPort int) error
	// RemoveProxyDevice removes the per-Route proxy device. Removing an absent
	// device is not an error.
	RemoveProxyDevice(ctx context.Context, deviceName string) error
	// MachineState resolves a Machine's presence, running state, and current IP.
	// tenant+project identify the Incus app project; the backend knows the install
	// prefix. A transient Incus failure must be returned as an error (never as
	// Present=false) so the reconcile does not prune on a blip.
	MachineState(ctx context.Context, tenant, project, machine string) (MachineState, error)
}

// CaddyController writes the appliance Caddyfile and reloads Caddy. The Auth App
// runs in the same container as Caddy, so the real implementation is a local
// file write plus `caddy reload`; it is an interface so the RouteManager is
// testable without touching the filesystem.
type CaddyController interface {
	Apply(ctx context.Context, caddyfile string) error
}

// RouteManager owns the lifecycle of Public Routes on the Auth App: it is the
// single place that mutates the registry, the proxy devices, and the Caddyfile
// together. Constructed in `auth-app serve` with the incusx-backed RouteBackend
// and a local CaddyController.
type RouteManager struct {
	DB      *sql.DB
	Backend RouteBackend
	Caddy   CaddyController
	Render  CaddyRenderConfig
	// ResolveHost reports whether a hostname resolves to an address. Optional;
	// defaults to a DNS lookup. Injected in tests. Used to distinguish a custom
	// hostname still awaiting its manual CNAME (awaiting-dns) from a live one.
	ResolveHost func(ctx context.Context, host string) bool
}

// PublishRequest is a validated request to publish a Route. Hostname is the
// fully-resolved public FQDN (auto-subdomain or custom); the caller (HTTP
// handler) derives it from the Tenant identity before reaching here.
type PublishRequest struct {
	Hostname    string
	Tenant      string
	Project     string
	Machine     string
	BackendPort int
}

// Publish registers a Route, wires its proxy device to the Machine's current IP,
// and regenerates Caddy. The Machine must exist and be running (we need its IP).
func (m RouteManager) Publish(ctx context.Context, req PublishRequest) (Route, error) {
	state, err := m.Backend.MachineState(ctx, req.Tenant, req.Project, req.Machine)
	if err != nil {
		return Route{}, fmt.Errorf("resolve machine %q: %w", req.Machine, err)
	}
	if !state.Present {
		return Route{}, fmt.Errorf("machine %q does not exist", req.Machine)
	}
	if !state.Running || strings.TrimSpace(state.IPv4) == "" {
		return Route{}, fmt.Errorf("machine %q is not running (no address to route to); start it and retry", req.Machine)
	}

	route, err := UpsertRoute(ctx, m.DB, Route{
		Hostname:    req.Hostname,
		Tenant:      req.Tenant,
		Project:     req.Project,
		Machine:     req.Machine,
		BackendPort: req.BackendPort,
	})
	if err != nil {
		return Route{}, err
	}

	if err := m.Backend.EnsureProxyDevice(ctx, routeDeviceName(route.Hostname), route.LocalPort, state.IPv4, route.BackendPort); err != nil {
		return Route{}, fmt.Errorf("wire proxy device: %w", err)
	}
	if err := m.regenerate(ctx); err != nil {
		return Route{}, err
	}
	return route, nil
}

// Delete removes a Route the Tenant owns: registry row, proxy device, Caddy.
func (m RouteManager) Delete(ctx context.Context, hostname, tenant string) error {
	route, err := DeleteRoute(ctx, m.DB, hostname, tenant)
	if err != nil {
		return err
	}
	if err := m.Backend.RemoveProxyDevice(ctx, routeDeviceName(route.Hostname)); err != nil {
		return fmt.Errorf("remove proxy device: %w", err)
	}
	return m.regenerate(ctx)
}

// Reconcile brings every Route back in line with live Machine state. It is the
// once-per-event and periodic-safety-net pass. Per Route:
//   - Machine absent  -> prune (row + device), Caddy regenerated.
//   - Machine stopped -> keep (transient); leave the device; no Caddy change.
//   - Machine running -> (re)ensure the proxy device at the current IP (refresh
//     on IP change). Caddy is untouched — it targets the stable local port.
//
// A Machine whose state cannot be resolved (transient Incus failure) is skipped,
// never pruned. Returns the number of Routes pruned.
func (m RouteManager) Reconcile(ctx context.Context) (int, error) {
	routes, err := ListRoutes(ctx, m.DB)
	if err != nil {
		return 0, err
	}
	pruned := 0
	for _, route := range routes {
		state, err := m.Backend.MachineState(ctx, route.Tenant, route.Project, route.Machine)
		if err != nil {
			// Transient failure: never prune on a blip. Skip this Route.
			continue
		}
		switch {
		case !state.Present:
			if err := PruneRoute(ctx, m.DB, route.Hostname); err != nil {
				return pruned, err
			}
			if err := m.Backend.RemoveProxyDevice(ctx, routeDeviceName(route.Hostname)); err != nil {
				return pruned, fmt.Errorf("remove proxy device for pruned route %q: %w", route.Hostname, err)
			}
			pruned++
		case !state.Running || strings.TrimSpace(state.IPv4) == "":
			// Stopped: transient, keep the Route; nothing to refresh.
		default:
			if err := m.Backend.EnsureProxyDevice(ctx, routeDeviceName(route.Hostname), route.LocalPort, state.IPv4, route.BackendPort); err != nil {
				return pruned, fmt.Errorf("refresh proxy device for route %q: %w", route.Hostname, err)
			}
		}
	}
	if pruned > 0 {
		if err := m.regenerate(ctx); err != nil {
			return pruned, err
		}
	}
	return pruned, nil
}

// RouteStatus is a Route plus its live health, for `sc route status`/`list`.
type RouteStatus struct {
	Route
	Status string // RouteStatusLive | RouteStatusUnhealthy | RouteStatusAwaitingDNS
}

// Status resolves the live health of a Route from Machine state, plus DNS for
// custom hostnames: a running backend whose custom hostname does not yet resolve
// to the front door is awaiting-dns (the operator's manual CNAME hasn't landed,
// so no certificate has issued). Auto-subdomains sit under the Auth Hostname
// wildcard and always resolve, so they never report awaiting-dns.
func (m RouteManager) Status(ctx context.Context, route Route) RouteStatus {
	state, err := m.Backend.MachineState(ctx, route.Tenant, route.Project, route.Machine)
	if err != nil || !state.Present || !state.Running {
		return RouteStatus{Route: route, Status: RouteStatusUnhealthy}
	}
	if m.isCustomHostname(route.Hostname) && !m.hostResolves(ctx, route.Hostname) {
		return RouteStatus{Route: route, Status: RouteStatusAwaitingDNS}
	}
	return RouteStatus{Route: route, Status: RouteStatusLive}
}

// isCustomHostname reports whether hostname is a customer-supplied hostname
// rather than an auto-subdomain under the route base domain (which sits behind
// the operator's wildcard DNS and always resolves).
func (m RouteManager) isCustomHostname(hostname string) bool {
	base := strings.Trim(strings.TrimSpace(m.Render.RouteBaseDomain), ".")
	if base == "" {
		base = strings.Trim(strings.TrimSpace(m.Render.AuthHostname), ".")
	}
	return base == "" || !strings.HasSuffix(hostname, "."+base)
}

// hostResolves reports whether host resolves to an address, with a short bound.
func (m RouteManager) hostResolves(ctx context.Context, host string) bool {
	if m.ResolveHost != nil {
		return m.ResolveHost(ctx, host)
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(lookupCtx, host)
	return err == nil && len(addrs) > 0
}

// SyncCaddy writes the appliance Caddyfile from the current registry without any
// mutation — used on startup so the coexistence global block, the Auth Hostname
// site, and any existing route sites are correct before the first publish.
func (m RouteManager) SyncCaddy(ctx context.Context) error {
	return m.regenerate(ctx)
}

// regenerate renders the Caddyfile from the whole registry and applies it.
func (m RouteManager) regenerate(ctx context.Context) error {
	routes, err := ListRoutes(ctx, m.DB)
	if err != nil {
		return err
	}
	if err := m.Caddy.Apply(ctx, RenderCaddyfile(m.Render, routes)); err != nil {
		return fmt.Errorf("apply caddy config: %w", err)
	}
	return nil
}

// routeDeviceName is the stable, Incus-valid device name for a Route's proxy
// device: a hash of the Hostname keeps it unique, short, and within Incus's
// device-name charset regardless of the Hostname's characters.
func routeDeviceName(hostname string) string {
	sum := sha256.Sum256([]byte(normalizeHostname(hostname)))
	return "scroute-" + hex.EncodeToString(sum[:])[:16]
}
