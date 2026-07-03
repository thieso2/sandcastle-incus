package incusx

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"

	"github.com/thieso2/sandcastle-incus/internal/dns"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

// ReconcileV2TenantsDNS registers DNS A-records for every running machine in each
// v2 tenant's default project into that tenant's sidecar CoreDNS zone, so freeform
// `incus launch` machines resolve as <name>.<suffix> without any manual step. It is
// idempotent — a machine that vanished is dropped on the next pass. This is the
// auto-registration the sidecar dnsmasq fallthrough never delivered.
//
// v2 tenants are the `kind=infra` projects (they hold the sidecar + the tenant's
// CIDR/suffix); the machines run in the sibling `<infra>-default` project.
func ReconcileV2TenantsDNS(ctx context.Context, server incus.InstanceServer, store tenant.IncusTenantStore) error {
	if server == nil {
		return nil
	}
	projects, err := store.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("list projects for DNS reconcile: %w", err)
	}

	var errs []string
	infra := 0
	for _, project := range projects {
		if !meta.IsManaged(project.Config) || project.Config[meta.KeyKind] != meta.KindInfra {
			continue
		}
		infra++
		suffix := strings.TrimSpace(project.Config[keyV2Suffix])
		cidr := strings.TrimSpace(project.Config[keyV2CIDR])
		if suffix == "" || cidr == "" {
			continue
		}
		if err := reconcileOneV2TenantDNS(server, project.Name, suffix, cidr); err != nil {
			errs = append(errs, project.Name+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("DNS reconcile: %s", strings.Join(errs, "; "))
	}
	return nil
}

func reconcileOneV2TenantDNS(server incus.InstanceServer, infraProject, suffix, cidr string) error {
	defaultProject := infraProject + "-" + naming.DefaultProjectName
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return err
	}

	instances, err := server.UseProject(defaultProject).GetInstancesFull(api.InstanceTypeAny)
	if err != nil {
		return fmt.Errorf("list %s instances: %w", defaultProject, err)
	}
	machines := []meta.Machine{}
	for _, instance := range instances {
		ip := instanceTenantIPv4(instance, prefix)
		if ip == "" {
			continue
		}
		// No Project → short name <name>.<suffix> only (see dns.RenderTenant).
		machines = append(machines, meta.Machine{Name: instance.Name, PrivateIP: ip, Running: instance.IsActive()})
	}

	result, err := dns.PlanApply(dns.Tenant{
		IncusName:    infraProject,
		InfraProject: infraProject,
		DNSSuffix:    suffix,
		PrivateCIDR:  cidr,
	}, machines)
	if err != nil {
		return err
	}
	// The v2 sidecar instance is named after the infra project (SidecarInstance ==
	// infraProject), not the v1 "sc-dns" instance writeDNSFiles targets.
	sidecar := server.UseProject(infraProject)
	for _, dir := range []string{"/etc/coredns", "/etc/coredns/zones"} {
		if err := sidecar.CreateInstanceFile(infraProject, dir, incus.InstanceFileArgs{Type: "directory", Mode: 0o755}); err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	// CoreDNS's file plugin only reloads a zone when its SOA serial INCREASES.
	// RenderTenant emits serial 1, so make it monotonic (seconds since epoch).
	serial := time.Now().Unix()
	for _, file := range result.Files {
		// Only (re)write the CoreDNS config; the resolver files (/etc/resolv.conf,
		// upstream-resolv.conf) are static, set once at provisioning.
		if !strings.HasPrefix(file.Path, "/etc/coredns") {
			continue
		}
		content := file.Content
		if strings.Contains(file.Path, "/zones/db.") {
			content = strings.Replace(content,
				"hostmaster."+suffix+". 1 ",
				fmt.Sprintf("hostmaster.%s. %d ", suffix, serial), 1)
		}
		if err := sidecar.CreateInstanceFile(infraProject, file.Path, incus.InstanceFileArgs{
			Content:   strings.NewReader(content),
			Type:      "file",
			Mode:      file.Mode,
			WriteMode: "overwrite",
		}); err != nil {
			return fmt.Errorf("write %s: %w", file.Path, err)
		}
	}
	// Reload CoreDNS to pick up the new zone.
	op, err := sidecar.ExecInstance(infraProject, api.InstanceExecPost{
		Command:   []string{"/bin/sh", "-c", "systemctl reload coredns 2>/dev/null || systemctl restart coredns"},
		WaitForWS: true,
	}, nil)
	if err != nil {
		return fmt.Errorf("reload coredns: %w", err)
	}
	return op.Wait()
}

// instanceTenantIPv4 returns the instance's eth0 IPv4 that falls inside the tenant
// CIDR (skips loopback/tailnet/other addresses), or "" if none.
func instanceTenantIPv4(instance api.InstanceFull, cidr netip.Prefix) string {
	if instance.State == nil {
		return ""
	}
	for name, iface := range instance.State.Network {
		if name == "lo" {
			continue
		}
		for _, addr := range iface.Addresses {
			if addr.Family != "inet" {
				continue
			}
			ip, err := netip.ParseAddr(addr.Address)
			if err == nil && cidr.Contains(ip) {
				return addr.Address
			}
		}
	}
	return ""
}
