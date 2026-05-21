package e2e

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/tailscale"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestTailscaleAttachmentE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}
	baseSource := strings.TrimSpace(e2eConfig.Images.BaseSource)
	aiSource := strings.TrimSpace(e2eConfig.Images.AISource)
	authKey := strings.TrimSpace(e2eConfig.Tailscale.AuthKey)
	if baseSource == "" || aiSource == "" {
		t.Skip("set SANDCASTLE_E2E_BASE_IMAGE_SOURCE and SANDCASTLE_E2E_AI_IMAGE_SOURCE to already-imported Sandcastle image aliases")
	}
	if authKey == "" {
		t.Skip("set SANDCASTLE_E2E_TAILSCALE_AUTHKEY to run real Tailscale attachment e2e tests")
	}

	ctx := context.Background()
	runID := e2eConfig.DisposableRunID()
	owner := safeProjectName("owner-" + runID)
	name := safeProjectName("project-" + runID)
	sandboxName := safeProjectName("box-" + runID)
	ref := owner + "/" + name
	sandboxRef := ref + "/" + sandboxName
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-tailscale"
	aiAlias := "sandcastle/ai:" + safeToken(runID) + "-tailscale"
	adminConfig := config.Admin{
		Remote:                e2eConfig.Remote,
		StoragePool:           e2eConfig.StoragePool,
		CIDRPool:              e2eConfig.CIDRPool,
		ProjectPrefix:         config.DefaultProjectPrefix,
		InfrastructureProject: config.DefaultInfrastructureProject,
		Images: config.Images{
			Base: baseAlias,
			AI:   aiAlias,
		},
	}

	server, err := e2eInstanceServer(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupImageAlias(t, e2eConfig, server, aiAlias))
	t.Cleanup(cleanupImageAlias(t, e2eConfig, server, baseAlias))

	imageManager := incusx.NewImageManager(e2eConfig.Remote)
	syncImageAlias(t, ctx, imageManager, adminConfig, baseSource)
	syncImageAlias(t, ctx, imageManager, adminConfig, aiSource)

	store := incusx.NewProjectStore(e2eConfig.Remote)
	registerProjectDiagnostics(t, ctx, store, incusx.NewTopologyStore(e2eConfig.Remote), runID)
	creator := incusx.NewProjectCreator(e2eConfig.Remote)
	projectDeleter := incusx.NewProjectDeleter(e2eConfig.Remote)
	deletePlan, err := project.PlanDelete(adminConfig, project.DeleteRequest{Reference: ref, Purge: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable project %s", ref)
			return
		}
		if err := projectDeleter.DeleteProject(ctx, deletePlan); err != nil {
			t.Logf("cleanup failed for %s: %v", ref, err)
		}
	})

	existing, err := project.List(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	createPlan, err := project.PlanCreate(adminConfig, project.CreateRequest{
		Reference:     ref,
		Domain:        name + "." + e2eConfig.DomainSuffix,
		OccupiedCIDRs: project.OccupiedCIDRs(existing),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateProject(ctx, createPlan); err != nil {
		t.Fatal(err)
	}

	sandboxStore := incusx.NewHostOverrideManager(e2eConfig.Remote)
	createSandboxPlan, err := sandbox.PlanCreate(ctx, adminConfig, store, sandboxStore, sandbox.CreateRequest{Reference: sandboxRef})
	if err != nil {
		t.Fatal(err)
	}
	if err := incusx.NewSandboxCreator(e2eConfig.Remote).CreateSandbox(ctx, createSandboxPlan); err != nil {
		t.Fatal(err)
	}
	projectServer := server.UseProject(createPlan.IncusProject)
	hostname := sandboxName + "." + createPlan.Domain
	startSandboxHTTPApp(t, projectServer, createSandboxPlan.InstanceName, createSandboxPlan.AppPort, "sandcastle-tailscale")

	if _, err := incusx.NewDNSManager(e2eConfig.Remote).Apply(ctx, dns.Project{
		IncusName:   createPlan.IncusProject,
		Owner:       owner,
		Name:        name,
		Domain:      createPlan.Domain,
		PrivateCIDR: createPlan.PrivateCIDR,
	}); err != nil {
		t.Fatal(err)
	}

	manager := incusx.NewTailscaleManager(e2eConfig.Remote)
	upPlan, err := tailscale.PlanUp(ctx, adminConfig, store, tailscale.UpRequest{
		Reference:     ref,
		AuthKey:       authKey,
		AdvertiseTags: []string{e2eConfig.Tailscale.Tag},
	})
	if err != nil {
		t.Fatal(err)
	}
	downPlan, err := tailscale.PlanDown(ctx, adminConfig, store, tailscale.DownRequest{Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.RunDown(ctx, downPlan, tailscale.RunSession{Stdout: io.Discard, Stderr: io.Discard}); err != nil {
			t.Logf("tailscale down cleanup failed for %s: %v", ref, err)
		}
	})
	if err := manager.RunUp(ctx, upPlan, tailscale.RunSession{Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		t.Fatal(err)
	}

	statusPlan, err := tailscale.PlanStatus(ctx, adminConfig, store, tailscale.StatusRequest{Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	result := waitForTailscaleRunning(t, ctx, manager, statusPlan)
	if !containsString(result.Tailscale.AdvertisedRoutes, createPlan.PrivateCIDR) {
		t.Fatalf("advertised routes = %#v, want %s", result.Tailscale.AdvertisedRoutes, createPlan.PrivateCIDR)
	}
	if result.Tailscale.Tailnet == "" {
		t.Fatalf("expected tailnet in status: %#v", result.Tailscale)
	}
	if len(result.Tailscale.TailscaleIPs) == 0 {
		t.Fatalf("expected tailscale IPs in status: %#v", result.Tailscale)
	}

	waitForProjectDNSOverTailscale(t, net.JoinHostPort(createPlan.DNSAddress, "53"), hostname, createSandboxPlan.PrivateIP)
	waitForSandboxHTTPSOverTailscale(t, hostname, createSandboxPlan.PrivateIP, "sandcastle-tailscale")
}

func waitForTailscaleRunning(t *testing.T, ctx context.Context, manager incusx.TailscaleManager, plan tailscale.StatusPlan) tailscale.StatusResult {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last tailscale.StatusResult
	var lastErr error
	for time.Now().Before(deadline) {
		last, lastErr = manager.RunStatus(ctx, plan, tailscale.RunSession{Stderr: io.Discard})
		if lastErr == nil && strings.EqualFold(last.Tailscale.State, "Running") {
			return last
		}
		time.Sleep(time.Second)
	}
	if lastErr != nil {
		t.Fatalf("tailscale status did not become running: %v", lastErr)
	}
	t.Fatalf("tailscale state = %q, want Running", last.Tailscale.State)
	return tailscale.StatusResult{}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func waitForProjectDNSOverTailscale(t *testing.T, dnsAddr string, hostname string, wantIP string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		response, err := queryE2EDNS(dnsAddr, e2eDNSQuery(hostname), 3*time.Second)
		if err == nil {
			ips, err := parseE2EARecords(response)
			if err == nil && containsString(ips, wantIP) {
				return
			}
			if err != nil {
				last = err.Error()
			} else {
				last = fmt.Sprintf("A records = %#v", ips)
			}
		} else {
			last = err.Error()
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("DNS query %s via %s did not return %s: %s", hostname, dnsAddr, wantIP, last)
}

func queryE2EDNS(addr string, packet []byte, timeout time.Duration) ([]byte, error) {
	conn, err := net.DialTimeout("udp", addr, timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
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

func parseE2EARecords(data []byte) ([]string, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("short DNS response: %d bytes", len(data))
	}
	if rcode := data[3] & 0x0f; rcode != 0 {
		return nil, fmt.Errorf("DNS rcode = %d", rcode)
	}
	qdCount := int(binary.BigEndian.Uint16(data[4:6]))
	anCount := int(binary.BigEndian.Uint16(data[6:8]))
	offset := 12
	var err error
	for range qdCount {
		offset, err = skipE2EDNSName(data, offset)
		if err != nil {
			return nil, err
		}
		if offset+4 > len(data) {
			return nil, fmt.Errorf("truncated DNS question")
		}
		offset += 4
	}
	var ips []string
	for range anCount {
		offset, err = skipE2EDNSName(data, offset)
		if err != nil {
			return nil, err
		}
		if offset+10 > len(data) {
			return nil, fmt.Errorf("truncated DNS answer header")
		}
		recordType := binary.BigEndian.Uint16(data[offset : offset+2])
		class := binary.BigEndian.Uint16(data[offset+2 : offset+4])
		length := int(binary.BigEndian.Uint16(data[offset+8 : offset+10]))
		offset += 10
		if offset+length > len(data) {
			return nil, fmt.Errorf("truncated DNS answer data")
		}
		if recordType == 1 && class == 1 && length == net.IPv4len {
			ips = append(ips, net.IP(data[offset:offset+length]).String())
		}
		offset += length
	}
	return ips, nil
}

func skipE2EDNSName(data []byte, offset int) (int, error) {
	for {
		if offset >= len(data) {
			return 0, fmt.Errorf("truncated DNS name")
		}
		length := data[offset]
		if length&0xc0 == 0xc0 {
			if offset+2 > len(data) {
				return 0, fmt.Errorf("truncated DNS compression pointer")
			}
			return offset + 2, nil
		}
		if length&0xc0 != 0 {
			return 0, fmt.Errorf("unsupported DNS label marker 0x%x", length)
		}
		offset++
		if length == 0 {
			return offset, nil
		}
		offset += int(length)
	}
}

func waitForSandboxHTTPSOverTailscale(t *testing.T, hostname string, privateIP string, want string) {
	t.Helper()
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
				if addr == net.JoinHostPort(hostname, "443") {
					addr = net.JoinHostPort(privateIP, "443")
				}
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
			},
		},
	}
	deadline := time.Now().Add(60 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		response, err := client.Get("https://" + hostname + "/")
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK && strings.Contains(string(body), want) {
				return
			}
			if readErr != nil {
				last = readErr.Error()
			} else {
				last = fmt.Sprintf("status = %s body = %q", response.Status, string(body))
			}
		} else {
			last = err.Error()
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("HTTPS request to %s through %s did not return %q: %s", hostname, privateIP, want, last)
}
