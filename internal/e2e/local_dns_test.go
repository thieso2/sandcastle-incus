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
	"github.com/thieso2/sandcastle-incus/internal/project"
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
	owner := safeProjectName("owner-" + runID)
	name := safeProjectName("dns-" + runID)
	domain := name + "." + e2eConfig.DomainSuffix
	ref := owner + "/" + name
	store := localDNSProjectStore(t, owner, name, domain)
	adminConfig := config.Admin{
		Remote:                e2eConfig.Remote,
		StoragePool:           e2eConfig.StoragePool,
		CIDRPool:              e2eConfig.CIDRPool,
		ProjectPrefix:         config.DefaultProjectPrefix,
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

func localDNSProjectStore(t *testing.T, owner string, name string, domain string) project.MemoryStore {
	t.Helper()
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           owner,
		Project:         name,
		Domain:          domain,
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	return project.MemoryStore{Projects: []project.IncusProject{{Name: "sc-" + owner + "-" + name, Config: configMap}}}
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
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(packet); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4096)
	n, err := conn.Read(response)
	if err != nil {
		t.Fatal(err)
	}
	return response[:n]
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
