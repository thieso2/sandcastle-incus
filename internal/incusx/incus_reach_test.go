package incusx

import (
	"strings"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/meta"
)

func TestListenAddressPort(t *testing.T) {
	for address, want := range map[string]string{
		"":             "8443",
		":8443":        "8443",
		":443":         "443",
		"[::]:9443":    "9443",
		"10.0.0.1:443": "443",
		"10.0.0.1":     "8443",
		"[::]":         "8443",
		" :443 ":       "443",
	} {
		if got := listenAddressPort(address); got != want {
			t.Errorf("listenAddressPort(%q) = %q, want %q", address, got, want)
		}
	}
}

type fakeServerWithListener struct {
	*fakeBinaryUpdateServer
	httpsAddress string
}

func (f fakeServerWithListener) GetServer() (*api.Server, string, error) {
	server := &api.Server{}
	server.Config = map[string]string{"core.https_address": f.httpsAddress}
	return server, "", nil
}

func TestUpdateTenantSidecarReachFollowsHostListener(t *testing.T) {
	resource := &fakeBinaryUpdateResource{instance: &api.Instance{InstancePut: api.InstancePut{Config: map[string]string{
		meta.KeyKind: meta.KindSidecar,
	}}}}
	server := fakeServerWithListener{
		fakeBinaryUpdateServer: &fakeBinaryUpdateServer{
			project: &api.Project{ProjectPut: api.ProjectPut{Config: map[string]string{
				meta.KeyV2CIDR: "10.249.7.0/24",
			}}},
			resource: resource,
		},
		httpsAddress: ":443",
	}
	creator := TenantCreator{Server: server}

	if _, err := creator.UpdateTenantSidecar("sc2", "acme", []byte("binary"), "v1.2.3"); err != nil {
		t.Fatalf("UpdateTenantSidecar: %v", err)
	}
	want := "tailscale serve --bg --tcp=8443 tcp://10.249.7.1:443"
	for _, command := range resource.execs {
		if strings.Contains(command, want) {
			return
		}
	}
	t.Fatalf("Incus Reach does not forward to the host listener :443; commands: %q", resource.execs)
}
