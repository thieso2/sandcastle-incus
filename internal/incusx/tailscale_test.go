package incusx

import (
	"context"
	"io"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/tailscale"
)

type fakeTailscaleServer struct {
	resource *fakeTailscaleResource
}

func (s fakeTailscaleServer) UseProject(name string) TailscaleResourceServer {
	return s.resource
}

type fakeTailscaleResource struct {
	instanceName string
	exec         api.InstanceExecPost
}

func (r *fakeTailscaleResource) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	r.instanceName = instanceName
	r.exec = exec
	if args.DataDone != nil {
		close(args.DataDone)
	}
	return fakeOperation{}, nil
}

func TestTailscaleManagerRunsUpInSidecar(t *testing.T) {
	resource := &fakeTailscaleResource{}
	manager := TailscaleManager{Server: fakeTailscaleServer{resource: resource}}
	err := manager.RunUp(context.Background(), tailscale.UpPlan{
		Project:         project.Summary{IncusName: "sc-alice-myproject"},
		InstanceName:    project.TailscaleName,
		AdvertiseRoutes: []string{"10.248.0.0/24"},
		AdvertiseTags:   []string{"tag:sandcastle"},
		AuthKey:         "tskey-secret",
	}, tailscale.RunSession{Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if resource.instanceName != project.TailscaleName {
		t.Fatalf("instanceName = %q", resource.instanceName)
	}
	command := strings.Join(resource.exec.Command, " ")
	if !strings.Contains(command, "--advertise-routes=10.248.0.0/24") {
		t.Fatalf("command = %q", command)
	}
	if !strings.Contains(command, "--advertise-tags=tag:sandcastle") {
		t.Fatalf("command = %q", command)
	}
	if !strings.Contains(command, "--auth-key=tskey-secret") {
		t.Fatalf("command = %q", command)
	}
}
