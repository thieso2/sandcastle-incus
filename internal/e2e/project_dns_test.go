package e2e

import (
	"bytes"
	"context"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
)

func TestProjectDNSE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}
	baseSource := strings.TrimSpace(e2eConfig.Images.BaseSource)
	aiSource := strings.TrimSpace(e2eConfig.Images.AISource)
	if baseSource == "" || aiSource == "" {
		t.Skip("set SANDCASTLE_E2E_BASE_IMAGE_SOURCE and SANDCASTLE_E2E_AI_IMAGE_SOURCE to already-imported Sandcastle image aliases")
	}

	ctx := context.Background()
	runID := e2eConfig.DisposableRunID()
	owner := safeProjectName("owner-" + runID)
	name := safeProjectName("dns-" + runID)
	firstSandboxName := safeProjectName("codex-" + runID)
	secondSandboxName := safeProjectName("claude-" + runID)
	ref := owner + "/" + name
	firstSandboxRef := ref + "/" + firstSandboxName
	secondSandboxRef := ref + "/" + secondSandboxName
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-dns"
	aiAlias := "sandcastle/ai:" + safeToken(runID) + "-dns"
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
	createProjectPlan, err := project.PlanCreate(adminConfig, project.CreateRequest{
		Reference:     ref,
		Domain:        name + "." + e2eConfig.DomainSuffix,
		OccupiedCIDRs: project.OccupiedCIDRs(existing),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateProject(ctx, createProjectPlan); err != nil {
		t.Fatal(err)
	}

	sandboxStore := incusx.NewHostOverrideManager(e2eConfig.Remote)
	createFirstSandboxPlan, err := sandbox.PlanCreate(ctx, adminConfig, store, sandboxStore, sandbox.CreateRequest{Reference: firstSandboxRef})
	if err != nil {
		t.Fatal(err)
	}
	sandboxCreator := incusx.NewSandboxCreator(e2eConfig.Remote)
	if err := sandboxCreator.CreateSandbox(ctx, createFirstSandboxPlan); err != nil {
		t.Fatal(err)
	}
	createSecondSandboxPlan, err := sandbox.PlanCreate(ctx, adminConfig, store, sandboxStore, sandbox.CreateRequest{Reference: secondSandboxRef})
	if err != nil {
		t.Fatal(err)
	}
	if createSecondSandboxPlan.PrivateIP == createFirstSandboxPlan.PrivateIP {
		t.Fatalf("second sandbox reused private IP %s", createSecondSandboxPlan.PrivateIP)
	}
	if err := sandboxCreator.CreateSandbox(ctx, createSecondSandboxPlan); err != nil {
		t.Fatal(err)
	}

	if _, err := incusx.NewDNSManager(e2eConfig.Remote).Apply(ctx, dns.Project{
		IncusName:   createProjectPlan.IncusProject,
		Owner:       owner,
		Name:        name,
		Domain:      createProjectPlan.Domain,
		PrivateCIDR: createProjectPlan.PrivateCIDR,
	}); err != nil {
		t.Fatal(err)
	}

	projectServer := server.UseProject(createProjectPlan.IncusProject)
	firstExact := firstSandboxName + "." + createProjectPlan.Domain
	firstWildcard := "app." + firstExact
	secondExact := secondSandboxName + "." + createProjectPlan.Domain
	absent := "app." + createProjectPlan.Domain
	assertCoreDNSAnswer(t, projectServer, createFirstSandboxPlan.InstanceName, createProjectPlan.DNSAddress, firstExact, createFirstSandboxPlan.PrivateIP)
	assertCoreDNSAnswer(t, projectServer, createFirstSandboxPlan.InstanceName, createProjectPlan.DNSAddress, firstWildcard, createFirstSandboxPlan.PrivateIP)
	assertCoreDNSAnswer(t, projectServer, createFirstSandboxPlan.InstanceName, createProjectPlan.DNSAddress, secondExact, createSecondSandboxPlan.PrivateIP)
	assertCoreDNSNoAnswer(t, projectServer, createFirstSandboxPlan.InstanceName, createProjectPlan.DNSAddress, absent)
}

func assertCoreDNSAnswer(t *testing.T, server incus.InstanceServer, instance string, dnsAddress string, name string, wantIP string) {
	t.Helper()
	output := execInstanceOutput(t, server, instance, []string{"python3", "-c", dnsQueryScript(dnsAddress, name)})
	if !strings.Contains(output, "IP "+wantIP) {
		t.Fatalf("DNS query %s output = %q, want IP %s", name, output, wantIP)
	}
}

func assertCoreDNSNoAnswer(t *testing.T, server incus.InstanceServer, instance string, dnsAddress string, name string) {
	t.Helper()
	output := execInstanceOutput(t, server, instance, []string{"python3", "-c", dnsQueryScript(dnsAddress, name)})
	if strings.Contains(output, "IP ") {
		t.Fatalf("DNS query %s output = %q, want no IP answers", name, output)
	}
}

func execInstanceOutput(t *testing.T, server incus.InstanceServer, instance string, command []string) string {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	done := make(chan bool)
	op, err := server.ExecInstance(instance, api.InstanceExecPost{
		Command:   command,
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdout:   &stdout,
		Stderr:   &stderr,
		DataDone: done,
	})
	if err != nil {
		t.Fatalf("exec %s in %s: %v", strings.Join(command, " "), instance, err)
	}
	if err := op.Wait(); err != nil {
		t.Fatalf("wait for exec %s in %s: %v\nstderr: %s", strings.Join(command, " "), instance, err, stderr.String())
	}
	<-done
	if stderr.Len() > 0 {
		t.Logf("stderr from %s: %s", instance, stderr.String())
	}
	return stdout.String()
}

func dnsQueryScript(server string, name string) string {
	return `
import socket, struct, sys
server = ` + pythonQuote(server) + `
name = ` + pythonQuote(name) + `
packet = bytearray(b'\x12\x34\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00')
for label in name.split('.'):
    packet.append(len(label))
    packet.extend(label.encode('ascii'))
packet.extend(b'\x00\x00\x01\x00\x01')
sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
sock.settimeout(2)
sock.sendto(packet, (server, 53))
data, _ = sock.recvfrom(512)
rcode = data[3] & 0x0f
answer_count = struct.unpack('!H', data[6:8])[0]
print('RCODE', rcode)
offset = 12
for _ in range(data[4] << 8 | data[5]):
    while data[offset] != 0:
        offset += data[offset] + 1
    offset += 5
for _ in range(answer_count):
    if data[offset] & 0xc0 == 0xc0:
        offset += 2
    else:
        while data[offset] != 0:
            offset += data[offset] + 1
        offset += 1
    atype, aclass, ttl, rdlen = struct.unpack('!HHIH', data[offset:offset+10])
    offset += 10
    rdata = data[offset:offset+rdlen]
    offset += rdlen
    if atype == 1 and aclass == 1 and rdlen == 4:
        print('IP', socket.inet_ntoa(rdata))
`
}

func pythonQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "\\'") + "'"
}
