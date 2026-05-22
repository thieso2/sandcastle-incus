package incusx

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type fakeCreateServer struct {
	project        *api.Project
	pool           *api.StoragePool
	adminPool      *api.StoragePool
	images         map[string]*api.Image
	imageAliases   map[string]*api.ImageAliasesEntry
	createdProject *api.ProjectsPost
	updatedProject *api.ProjectPut
	createdPool    *api.StoragePoolsPost
	resourceServer *fakeResourceServer
}

func (s *fakeCreateServer) GetProject(name string) (*api.Project, string, error) {
	if s.project == nil {
		return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
	}
	return s.project, "etag", nil
}

func (s *fakeCreateServer) CreateProject(project api.ProjectsPost) error {
	s.createdProject = &project
	return nil
}

func (s *fakeCreateServer) UpdateProject(name string, project api.ProjectPut, etag string) error {
	s.updatedProject = &project
	return nil
}

func (s *fakeCreateServer) UseProject(name string) TenantResourceServer {
	return s.resourceServer
}

func (s *fakeCreateServer) GetStoragePool(name string) (*api.StoragePool, string, error) {
	if s.pool != nil && s.pool.Name == name {
		return s.pool, "etag", nil
	}
	if s.adminPool != nil && s.adminPool.Name == name {
		return s.adminPool, "etag", nil
	}
	if name != config.DefaultStoragePool {
		return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
	}
	return &api.StoragePool{
		Name:   name,
		Driver: "zfs",
		StoragePoolPut: api.StoragePoolPut{
			Config: api.ConfigMap{"source": "default"},
		},
	}, "etag", nil
}

func (s *fakeCreateServer) CreateStoragePool(pool api.StoragePoolsPost) error {
	s.createdPool = &pool
	return nil
}

func (s *fakeCreateServer) GetImage(ref string) (*api.Image, string, error) {
	if image := s.images[ref]; image != nil {
		return image, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (s *fakeCreateServer) GetImageAlias(name string) (*api.ImageAliasesEntry, string, error) {
	if alias := s.imageAliases[name]; alias != nil {
		return alias, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (s *fakeCreateServer) imageServer() incus.ImageServer {
	return nil
}

type fakeResourceServer struct {
	networks           map[string]*api.Network
	volumes            map[string]*api.StorageVolume
	profiles           map[string]*api.Profile
	instances          map[string]*api.Instance
	images             map[string]*api.Image
	imageAliases       map[string]*api.ImageAliasesEntry
	createdNetwork     *api.NetworksPost
	createdVolumes     []api.StorageVolumesPost
	createdVolumeFiles map[string]string
	createdInstances   []api.InstancesPost
	copiedImages       []string
	createdFiles       map[string]string
	updatedInstances   []string
	startedInstances   []string
	execInstances      []string
	execCommands       [][]string
}

func (s *fakeResourceServer) GetNetwork(name string) (*api.Network, string, error) {
	if network := s.networks[name]; network != nil {
		return network, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (s *fakeResourceServer) CreateNetwork(network api.NetworksPost) error {
	s.createdNetwork = &network
	s.networks[network.Name] = &api.Network{Name: network.Name}
	return nil
}

func (s *fakeResourceServer) GetStoragePoolVolume(pool string, volType string, name string) (*api.StorageVolume, string, error) {
	if volume := s.volumes[name]; volume != nil {
		return volume, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (s *fakeResourceServer) CreateStoragePoolVolume(pool string, volume api.StorageVolumesPost) error {
	s.createdVolumes = append(s.createdVolumes, volume)
	s.volumes[volume.Name] = &api.StorageVolume{Name: volume.Name, Type: volume.Type}
	return nil
}

func (s *fakeResourceServer) GetProfile(name string) (*api.Profile, string, error) {
	if profile := s.profiles[name]; profile != nil {
		return profile, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (s *fakeResourceServer) CreateProfile(profile api.ProfilesPost) error {
	if s.profiles == nil {
		s.profiles = map[string]*api.Profile{}
	}
	s.profiles[profile.Name] = &api.Profile{Name: profile.Name, ProfilePut: profile.ProfilePut}
	return nil
}

func (s *fakeResourceServer) UpdateProfile(name string, profile api.ProfilePut, etag string) error {
	if s.profiles == nil {
		s.profiles = map[string]*api.Profile{}
	}
	s.profiles[name] = &api.Profile{Name: name, ProfilePut: profile}
	return nil
}

func (s *fakeResourceServer) CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error {
	if s.createdVolumeFiles == nil {
		s.createdVolumeFiles = map[string]string{}
	}
	content, err := io.ReadAll(args.Content)
	if err != nil {
		return err
	}
	s.createdVolumeFiles[volumeName+":"+filePath] = string(content)
	return nil
}

func (s *fakeResourceServer) GetInstance(name string) (*api.Instance, string, error) {
	if instance := s.instances[name]; instance != nil {
		return instance, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (s *fakeResourceServer) CreateInstance(instance api.InstancesPost) (incus.Operation, error) {
	s.createdInstances = append(s.createdInstances, instance)
	status := "Stopped"
	statusCode := api.Stopped
	if instance.Start {
		status = "Running"
		statusCode = api.Running
	}
	s.instances[instance.Name] = &api.Instance{Name: instance.Name, Status: status, StatusCode: statusCode}
	return fakeOperation{}, nil
}

func (s *fakeResourceServer) UpdateInstance(name string, instance api.InstancePut, etag string) (incus.Operation, error) {
	s.updatedInstances = append(s.updatedInstances, name)
	if s.instances[name] == nil {
		s.instances[name] = &api.Instance{Name: name}
	}
	s.instances[name].InstancePut = instance
	return fakeOperation{}, nil
}

func (s *fakeResourceServer) UpdateInstanceState(name string, state api.InstanceStatePut, etag string) (incus.Operation, error) {
	if state.Action == "start" {
		s.startedInstances = append(s.startedInstances, name)
		if s.instances[name] != nil {
			s.instances[name].Status = "Running"
			s.instances[name].StatusCode = api.Running
		}
	}
	return fakeOperation{}, nil
}

func (s *fakeResourceServer) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	if s.createdFiles == nil {
		s.createdFiles = map[string]string{}
	}
	if args.Type == "directory" {
		s.createdFiles[instanceName+":"+path] = "<dir>"
		return nil
	}
	content, err := io.ReadAll(args.Content)
	if err != nil {
		return err
	}
	s.createdFiles[instanceName+":"+path] = string(content)
	return nil
}

func (s *fakeResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	s.execInstances = append(s.execInstances, instanceName)
	s.execCommands = append(s.execCommands, exec.Command)
	if args.DataDone != nil {
		close(args.DataDone)
	}
	return fakeOperation{}, nil
}

func (s *fakeResourceServer) GetImage(ref string) (*api.Image, string, error) {
	if image := s.images[ref]; image != nil {
		return image, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (s *fakeResourceServer) GetImageAlias(name string) (*api.ImageAliasesEntry, string, error) {
	if alias := s.imageAliases[name]; alias != nil {
		return alias, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (s *fakeResourceServer) CreateImageAlias(alias api.ImageAliasesPost) error {
	if s.imageAliases == nil {
		s.imageAliases = map[string]*api.ImageAliasesEntry{}
	}
	s.imageAliases[alias.Name] = &api.ImageAliasesEntry{
		Name:                 alias.Name,
		Type:                 alias.Type,
		ImageAliasesEntryPut: alias.ImageAliasesEntryPut,
	}
	return nil
}

func (s *fakeResourceServer) CopyImageFrom(source TenantCreateServer, image api.Image, aliases []api.ImageAlias) (incus.RemoteOperation, error) {
	if s.images == nil {
		s.images = map[string]*api.Image{}
	}
	if s.imageAliases == nil {
		s.imageAliases = map[string]*api.ImageAliasesEntry{}
	}
	s.images[image.Fingerprint] = &api.Image{Fingerprint: image.Fingerprint}
	s.copiedImages = append(s.copiedImages, image.Fingerprint)
	for _, alias := range aliases {
		s.imageAliases[alias.Name] = &api.ImageAliasesEntry{
			Name: alias.Name,
			Type: "container",
			ImageAliasesEntryPut: api.ImageAliasesEntryPut{
				Description: alias.Description,
				Target:      image.Fingerprint,
			},
		}
	}
	return fakeRemoteOperation{}, nil
}

type fakeOperation struct{}

func (fakeOperation) AddHandler(func(api.Operation)) (*incus.EventTarget, error) { return nil, nil }
func (fakeOperation) Cancel() error                                              { return nil }
func (fakeOperation) Get() api.Operation                                         { return api.Operation{} }
func (fakeOperation) GetWebsocket(string) (*websocket.Conn, error)               { return nil, nil }
func (fakeOperation) RemoveHandler(*incus.EventTarget) error                     { return nil }
func (fakeOperation) Refresh() error                                             { return nil }
func (fakeOperation) Wait() error                                                { return nil }
func (fakeOperation) WaitContext(context.Context) error                          { return nil }

type fakeRemoteOperation struct{}

func (fakeRemoteOperation) AddHandler(func(api.Operation)) (*incus.EventTarget, error) {
	return nil, nil
}
func (fakeRemoteOperation) CancelTarget() error                { return nil }
func (fakeRemoteOperation) GetTarget() (*api.Operation, error) { return &api.Operation{}, nil }
func (fakeRemoteOperation) Wait() error                        { return nil }

func TestTenantCreatorCreatesMissingResources(t *testing.T) {
	plan := createPlanForTest(t)
	resourceServer := &fakeResourceServer{
		networks:           map[string]*api.Network{},
		volumes:            map[string]*api.StorageVolume{},
		instances:          map[string]*api.Instance{},
		createdFiles:       map[string]string{},
		createdVolumeFiles: map[string]string{},
	}
	server := fakeCreateServerForPlan(plan, resourceServer)
	creator := TenantCreator{Server: server}

	if err := creator.CreateTenant(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if server.createdProject == nil {
		t.Fatal("expected project to be created")
	}
	if server.createdProject.Name != "sc-acme" {
		t.Fatalf("created Incus project = %q", server.createdProject.Name)
	}
	if got := server.createdPool.Config["source"]; got != "default/sc-acme" {
		t.Fatalf("created pool source = %q", got)
	}
	for _, key := range []string{
		"features.images",
		"features.profiles",
		"features.storage.buckets",
		"features.storage.volumes",
	} {
		if got := server.createdProject.Config[key]; got != "true" {
			t.Fatalf("created Incus project config %s = %q, want true", key, got)
		}
	}
	if len(resourceServer.copiedImages) != len(plan.ImageAliases) {
		t.Fatalf("copied images = %#v, want %d", resourceServer.copiedImages, len(plan.ImageAliases))
	}
	if resourceServer.createdNetwork == nil {
		t.Fatal("expected private network to be created")
	}
	if got := resourceServer.createdNetwork.Config["ipv4.address"]; got != "10.248.0.1/24" {
		t.Fatalf("network ipv4.address = %q", got)
	}
	if len(resourceServer.createdVolumes) != 3 {
		t.Fatalf("created volumes = %d, want 3", len(resourceServer.createdVolumes))
	}
	if resourceServer.createdVolumeFiles[tenant.CAVolumeName+":/ca.crt"] == "" {
		t.Fatal("expected CA certificate to be written")
	}
	if resourceServer.createdVolumeFiles[tenant.CAVolumeName+":/ca.key"] == "" {
		t.Fatal("expected CA private key to be written")
	}
	if len(resourceServer.createdInstances) != 2 {
		t.Fatalf("created instances = %d, want 2", len(resourceServer.createdInstances))
	}
	if resourceServer.createdInstances[0].Name != plan.TailscaleInstance {
		t.Fatalf("first sidecar = %q", resourceServer.createdInstances[0].Name)
	}
	if got := resourceServer.createdInstances[0].Devices["eth0"]["ipv4.address"]; got != "10.248.0.2" {
		t.Fatalf("tailscale address = %q", got)
	}
	if resourceServer.createdInstances[1].Name != tenant.DNSName {
		t.Fatalf("second sidecar = %q", resourceServer.createdInstances[1].Name)
	}
	if got := resourceServer.createdInstances[1].Devices["eth0"]["ipv4.address"]; got != "10.248.0.3" {
		t.Fatalf("dns address = %q", got)
	}
	if got := resourceServer.createdFiles[tenant.DNSName+":/etc/coredns/Corefile"]; got == "" {
		t.Fatal("expected CoreDNS Corefile to be written")
	}
	if got := resourceServer.createdFiles[tenant.DNSName+":/etc/coredns/zones/db.acme"]; got == "" {
		t.Fatal("expected CoreDNS zone to be written")
	}
	if len(resourceServer.execCommands) != 3 {
		t.Fatalf("exec commands = %#v", resourceServer.execCommands)
	}
	// First two execs configure networking for tailscale and dns sidecars.
	for i, name := range []string{plan.TailscaleInstance, tenant.DNSName} {
		if resourceServer.execInstances[i] != name {
			t.Fatalf("exec[%d] instance = %q, want %q", i, resourceServer.execInstances[i], name)
		}
		if got := strings.Join(resourceServer.execCommands[i], " "); !strings.Contains(got, "sandcastle-sidecar-network.service") || !strings.Contains(got, "/usr/sbin/ip addr replace") {
			t.Fatalf("exec[%d] command = %q, want persistent sidecar network setup", i, got)
		}
	}
	// Third exec restarts CoreDNS.
	if resourceServer.execInstances[2] != tenant.DNSName {
		t.Fatalf("exec[2] instance = %q, want %q", resourceServer.execInstances[2], tenant.DNSName)
	}
	if got := strings.Join(resourceServer.execCommands[2], " "); !strings.Contains(got, "coredns -conf /etc/coredns/Corefile") || !strings.Contains(got, "coredns.service") {
		t.Fatalf("exec[2] command = %q", got)
	}
}

func TestTenantCreatorOmitsSourceForDirStoragePool(t *testing.T) {
	plan := createPlanForTest(t)
	resourceServer := &fakeResourceServer{
		networks:           map[string]*api.Network{},
		volumes:            map[string]*api.StorageVolume{},
		instances:          map[string]*api.Instance{},
		createdFiles:       map[string]string{},
		createdVolumeFiles: map[string]string{},
	}
	server := fakeCreateServerForPlan(plan, resourceServer)
	server.adminPool = &api.StoragePool{
		Name:   plan.AdminStoragePool,
		Driver: "dir",
		StoragePoolPut: api.StoragePoolPut{
			Config: api.ConfigMap{"source": "/var/lib/incus/storage-pools/default"},
		},
	}
	creator := TenantCreator{Server: server}

	if err := creator.CreateTenant(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if _, ok := server.createdPool.Config["source"]; ok {
		t.Fatalf("created dir pool config = %#v, want no source", server.createdPool.Config)
	}
}

func TestTenantCreatorUpdatesExistingProjectMetadata(t *testing.T) {
	plan := createPlanForTest(t)
	resourceServer := &fakeResourceServer{
		networks: map[string]*api.Network{plan.PrivateNetwork: {Name: plan.PrivateNetwork}},
		volumes: map[string]*api.StorageVolume{
			plan.HomeVolume:      {Name: plan.HomeVolume, Type: "custom"},
			plan.WorkspaceVolume: {Name: plan.WorkspaceVolume, Type: "custom"},
			plan.CAVolume:        {Name: plan.CAVolume, Type: "custom"},
		},
		instances: map[string]*api.Instance{
			plan.TailscaleInstance: {Name: plan.TailscaleInstance, Status: "Running", StatusCode: api.Running},
			plan.DNSInstance:       {Name: plan.DNSInstance, Status: "Running", StatusCode: api.Running},
		},
		createdFiles:       map[string]string{},
		createdVolumeFiles: map[string]string{},
	}
	server := fakeCreateServerForPlan(plan, resourceServer)
	server.project = &api.Project{
		Name: plan.IncusProject,
		ProjectPut: api.ProjectPut{
			Description: "existing",
			Config:      api.ConfigMap{"features.images": "false"},
		},
	}
	creator := TenantCreator{Server: server}

	if err := creator.CreateTenant(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if server.createdProject != nil {
		t.Fatal("did not expect project creation")
	}
	if server.updatedProject == nil {
		t.Fatal("expected project metadata update")
	}
	if server.updatedProject.Config["features.images"] != "false" {
		t.Fatalf("existing config was not preserved: %#v", server.updatedProject.Config)
	}
	if server.updatedProject.Config[meta.KeyTenant] != "acme" {
		t.Fatalf("managed metadata missing: %#v", server.updatedProject.Config)
	}
	if resourceServer.createdNetwork != nil {
		t.Fatal("did not expect existing network creation")
	}
	if len(resourceServer.createdVolumes) != 0 {
		t.Fatalf("created volumes = %d, want 0", len(resourceServer.createdVolumes))
	}
	if len(resourceServer.createdInstances) != 0 {
		t.Fatalf("created instances = %d, want 0", len(resourceServer.createdInstances))
	}
	if resourceServer.createdFiles[tenant.DNSName+":/etc/coredns/Corefile"] == "" {
		t.Fatal("expected DNS files to be refreshed")
	}
	if resourceServer.createdVolumeFiles[tenant.CAVolumeName+":/ca.crt"] == "" {
		t.Fatal("expected CA files to be refreshed")
	}
}

func TestTenantCreatorStartsExistingStoppedSidecars(t *testing.T) {
	plan := createPlanForTest(t)
	resourceServer := &fakeResourceServer{
		networks: map[string]*api.Network{plan.PrivateNetwork: {Name: plan.PrivateNetwork}},
		volumes: map[string]*api.StorageVolume{
			plan.HomeVolume:      {Name: plan.HomeVolume, Type: "custom"},
			plan.WorkspaceVolume: {Name: plan.WorkspaceVolume, Type: "custom"},
			plan.CAVolume:        {Name: plan.CAVolume, Type: "custom"},
		},
		instances: map[string]*api.Instance{
			plan.TailscaleInstance: {Name: plan.TailscaleInstance, Status: "Stopped", StatusCode: api.Stopped},
			plan.DNSInstance:       {Name: plan.DNSInstance, Status: "Running", StatusCode: api.Running},
		},
		createdFiles:       map[string]string{},
		createdVolumeFiles: map[string]string{},
	}
	server := fakeCreateServerForPlan(plan, resourceServer)
	server.project = &api.Project{Name: plan.IncusProject}
	creator := TenantCreator{Server: server}

	if err := creator.CreateTenant(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(resourceServer.startedInstances) != 1 {
		t.Fatalf("started instances = %#v, want one", resourceServer.startedInstances)
	}
	if resourceServer.startedInstances[0] != plan.TailscaleInstance {
		t.Fatalf("started instance = %q", resourceServer.startedInstances[0])
	}
}

func createPlanForTest(t *testing.T) tenant.CreatePlan {
	t.Helper()
	plan, err := tenant.PlanCreate(config.LoadAdminFromEnv(), tenant.CreateRequest{
		Reference: "acme",
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func fakeCreateServerForPlan(plan tenant.CreatePlan, resourceServer *fakeResourceServer) *fakeCreateServer {
	images := map[string]*api.Image{}
	imageAliases := map[string]*api.ImageAliasesEntry{}
	for _, alias := range plan.ImageAliases {
		fingerprint := "fingerprint-" + alias
		images[fingerprint] = &api.Image{Fingerprint: fingerprint}
		imageAliases[alias] = &api.ImageAliasesEntry{
			Name: alias,
			Type: "container",
			ImageAliasesEntryPut: api.ImageAliasesEntryPut{
				Description: "image alias " + alias,
				Target:      fingerprint,
			},
		}
	}
	return &fakeCreateServer{
		images:         images,
		imageAliases:   imageAliases,
		resourceServer: resourceServer,
	}
}
