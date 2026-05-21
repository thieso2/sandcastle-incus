package e2e

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/localdns"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestLocalDNSInstallForwardRefreshUninstallE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	t.Setenv("SANDCASTLE_LOCAL_DNS_STATE", filepath.Join(dir, "state", "dns.yaml"))
	t.Setenv("SANDCASTLE_RESOLVER_DIR", filepath.Join(dir, "resolver"))

	ctx := context.Background()
	runID := e2eConfig.DisposableRunID()
	name := safeTenantResourceName("dns-" + runID)
	domain := name
	ref := name
	store := localDNSTenantStore(t, name, name, domain)
	adminConfig := config.Admin{
		Remote:                e2eConfig.Remote,
		StoragePool:           e2eConfig.StoragePool,
		CIDRPool:              e2eConfig.CIDRPool,
		IncusProjectPrefix:    config.DefaultIncusProjectPrefix,
		InfrastructureProject: config.DefaultInfrastructureProject,
		Images: config.Images{
			Base: config.DefaultBaseImageAlias,
			AI:   config.DefaultAIImageAlias,
		},
	}

	upstreamOne := startE2EUDPResponder(t, []byte{0x01, 0x01})
	upstreamTwo := startE2EUDPResponder(t, []byte{0x02, 0x02})
	plan, err := localdns.PlanInstall(ctx, adminConfig, store, localdns.Request{Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	plan.DNSEndpoint = upstreamOne
	plan.Listen = localE2EUDPAddr(t)
	manager := localdns.FileManager{}
	if _, err := manager.Install(ctx, plan); err != nil {
		t.Fatal(err)
	}
	resolver, err := os.ReadFile(plan.ResolverPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resolver), "nameserver 127.0.0.1") {
		t.Fatalf("resolver = %q", resolver)
	}

	forwarderCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- localdns.Forwarder{StatePath: plan.StatePath, Listen: plan.Listen, Timeout: time.Second}.Serve(forwarderCtx)
	}()
	waitForE2EUDP(t, plan.Listen)

	response := queryE2EForwarder(t, plan.Listen, e2eDNSQuery("codex."+domain))
	if string(response) != string([]byte{0x01, 0x01}) {
		t.Fatalf("response = %#v", response)
	}

	refreshPlan, err := localdns.PlanRefresh(ctx, adminConfig, store, localdns.Request{Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	refreshPlan.DNSEndpoint = upstreamTwo
	refreshPlan.Listen = plan.Listen
	if _, err := manager.Refresh(ctx, refreshPlan); err != nil {
		t.Fatal(err)
	}
	response = queryE2EForwarder(t, plan.Listen, e2eDNSQuery("codex."+domain))
	if string(response) != string([]byte{0x02, 0x02}) {
		t.Fatalf("response after refresh = %#v", response)
	}

	uninstallPlan, err := localdns.PlanUninstall(ctx, adminConfig, store, localdns.Request{Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	uninstallPlan.Listen = plan.Listen
	if _, err := manager.Uninstall(ctx, uninstallPlan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plan.ResolverPath); !os.IsNotExist(err) {
		t.Fatalf("expected resolver removal, stat err = %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("forwarder did not stop")
	}
}

func localDNSTenantStore(t *testing.T, fallbackTenant string, name string, domain string) tenant.MemoryStore {
	t.Helper()
	tenantName := name
	if tenantName == "" {
		tenantName = fallbackTenant
	}
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      tenantName,
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	return tenant.MemoryStore{Projects: []tenant.IncusProject{{Name: "sc-" + tenantName, Config: configMap}}}
}

func startE2EUDPResponder(t *testing.T, response []byte) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buffer := make([]byte, 4096)
		for {
			_, addr, err := conn.ReadFrom(buffer)
			if err != nil {
				return
			}
			_, _ = conn.WriteTo(response, addr)
		}
	}()
	return conn.LocalAddr().String()
}

func localE2EUDPAddr(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := conn.LocalAddr().String()
	_ = conn.Close()
	return addr
}

func waitForE2EUDP(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("udp", addr)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("UDP listener %s did not start", addr)
}

func queryE2EForwarder(t *testing.T, addr string, packet []byte) []byte {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.Dial("udp", addr)
		if err != nil {
			lastErr = err
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if err := conn.SetDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
			_ = conn.Close()
			t.Fatal(err)
		}
		if _, err := conn.Write(packet); err != nil {
			lastErr = err
			_ = conn.Close()
			time.Sleep(10 * time.Millisecond)
			continue
		}
		response := make([]byte, 4096)
		n, err := conn.Read(response)
		_ = conn.Close()
		if err == nil {
			return response[:n]
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("query forwarder %s: %v", addr, lastErr)
	return nil
}

func e2eDNSQuery(name string) []byte {
	packet := []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	for _, label := range strings.Split(name, ".") {
		packet = append(packet, byte(len(label)))
		packet = append(packet, []byte(label)...)
	}
	packet = append(packet, 0x00, 0x00, 0x01, 0x00, 0x01)
	return packet
}

func TestLocalDNSServiceInstallReloadUninstallE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}
	if !e2eConfig.LocalVM {
		t.Skip("set SANDCASTLE_E2E_LOCAL_VM=1 to run disposable-VM local DNS service e2e tests")
	}

	sandcastleBin := strings.TrimSpace(e2eConfig.SandcastleBin)
	if sandcastleBin == "" {
		sandcastleBin = buildSandcastleForE2E(t)
	}
	dir := t.TempDir()
	t.Setenv("SANDCASTLE_BIN", sandcastleBin)
	t.Setenv("SANDCASTLE_LOCAL_DNS_STATE", filepath.Join(dir, "state", "dns.yaml"))
	t.Setenv("SANDCASTLE_RESOLVER_DIR", filepath.Join(dir, "resolver"))

	ctx := context.Background()
	runID := e2eConfig.DisposableRunID()
	name := safeTenantResourceName("dns-service-" + runID)
	domain := name
	ref := name
	store := localDNSTenantStore(t, name, name, domain)
	adminConfig := config.Admin{
		Remote:                e2eConfig.Remote,
		StoragePool:           e2eConfig.StoragePool,
		CIDRPool:              e2eConfig.CIDRPool,
		IncusProjectPrefix:    config.DefaultIncusProjectPrefix,
		InfrastructureProject: config.DefaultInfrastructureProject,
		Images: config.Images{
			Base: config.DefaultBaseImageAlias,
			AI:   config.DefaultAIImageAlias,
		},
	}

	upstreamOne := startE2EUDPResponder(t, []byte{0x03, 0x03})
	upstreamTwo := startE2EUDPResponder(t, []byte{0x04, 0x04})
	plan, err := localdns.PlanInstall(ctx, adminConfig, store, localdns.Request{Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	plan.DNSEndpoint = upstreamOne
	plan.Listen = localdns.DefaultListen()
	manager := localdns.FileManager{}
	if _, err := manager.Install(ctx, plan); err != nil {
		t.Fatal(err)
	}

	serviceManager := localdns.FileServiceManager{}
	uninstallBefore, err := localdns.PlanServiceUninstall()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = serviceManager.UninstallService(ctx, uninstallBefore)

	installed := false
	t.Cleanup(func() {
		if !installed {
			return
		}
		uninstallPlan, err := localdns.PlanServiceUninstall()
		if err != nil {
			t.Logf("plan local DNS service cleanup: %v", err)
			return
		}
		if _, err := serviceManager.UninstallService(context.Background(), uninstallPlan); err != nil {
			t.Logf("local DNS service cleanup failed: %v", err)
		}
	})

	installServicePlan, err := localdns.PlanServiceInstall()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serviceManager.InstallService(ctx, installServicePlan); err != nil {
		t.Fatal(err)
	}
	installed = true
	response := queryE2EForwarderEventually(t, localdns.DefaultListen(), e2eDNSQuery("codex."+domain))
	if string(response) != string([]byte{0x03, 0x03}) {
		t.Fatalf("service response = %#v", response)
	}

	refreshPlan, err := localdns.PlanRefresh(ctx, adminConfig, store, localdns.Request{Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	refreshPlan.DNSEndpoint = upstreamTwo
	refreshPlan.Listen = localdns.DefaultListen()
	if _, err := manager.Refresh(ctx, refreshPlan); err != nil {
		t.Fatal(err)
	}
	reloadPlan, err := localdns.PlanServiceReload()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serviceManager.ReloadService(ctx, reloadPlan); err != nil {
		t.Fatal(err)
	}
	response = queryE2EForwarderEventually(t, localdns.DefaultListen(), e2eDNSQuery("codex."+domain))
	if string(response) != string([]byte{0x04, 0x04}) {
		t.Fatalf("service response after reload = %#v", response)
	}

	uninstallPlan, err := localdns.PlanServiceUninstall()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serviceManager.UninstallService(ctx, uninstallPlan); err != nil {
		t.Fatal(err)
	}
	installed = false
	if _, err := os.Stat(uninstallPlan.ServicePath); !os.IsNotExist(err) {
		t.Fatalf("expected service file removal, stat err = %v", err)
	}
	waitForE2EUDPStop(t, localdns.DefaultListen(), e2eDNSQuery("codex."+domain))
}

func queryE2EForwarderEventually(t *testing.T, addr string, packet []byte) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		response, err := queryE2EForwarderResult(addr, packet)
		if err == nil {
			return response
		}
		last = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("UDP listener %s did not respond: %v", addr, last)
	return nil
}

func waitForE2EUDPStop(t *testing.T, addr string, packet []byte) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := queryE2EForwarderResult(addr, packet); err == nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		return
	}
	t.Fatalf("UDP listener %s did not stop", addr)
}

func queryE2EForwarderResult(addr string, packet []byte) ([]byte, error) {
	conn, err := net.Dial("udp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		return nil, err
	}
	if _, err := conn.Write(packet); err != nil {
		return nil, err
	}
	response := make([]byte, 4096)
	n, err := conn.Read(response)
	if err != nil {
		return nil, err
	}
	return response[:n], nil
}
