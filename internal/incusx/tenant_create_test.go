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
	existingProjects map[string]*api.Project
	pool             *api.StoragePool
	adminPool        *api.StoragePool
	images           map[string]*api.Image
	imageAliases     map[string]*api.ImageAliasesEntry
	createdProjects  map[string]*api.ProjectsPost
	updatedProjects  map[string]*api.ProjectPut
	createdPool      *api.StoragePoolsPost
	projectServers   map[string]*fakeResourceServer
}

func (s *fakeCreateServer) GetProject(name string) (*api.Project, string, error) {
	if p, ok := s.existingProjects[name]; ok {
		return p, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (s *fakeCreateServer) CreateProject(project api.ProjectsPost) error {
	if s.createdProjects == nil {
		s.createdProjects = map[string]*api.ProjectsPost{}
	}
	s.createdProjects[project.Name] = &project
	return nil
}

func (s *fakeCreateServer) UpdateProject(name string, project api.ProjectPut, etag string) error {
	if s.updatedProjects == nil {
		s.updatedProjects = map[string]*api.ProjectPut{}
	}
	s.updatedProjects[name] = &project
	return nil
}

func (s *fakeCreateServer) UseProject(name string) TenantResourceServer {
	if server, ok := s.projectServers[name]; ok {
		return server
	}
	return &fakeResourceServer{
		networks:           map[string]*api.Network{},
		volumes:            map[string]*api.StorageVolume{},
		instances:          map[string]*api.Instance{},
		createdFiles:       map[string]string{},
		createdVolumeFiles: map[string]string{},
	}
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
	execStdin          []string
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

func (s *fakeResourceServer) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	if s.createdVolumeFiles == nil {
		return nil, nil, api.StatusErrorf(http.StatusNotFound, "not found")
	}
	content, ok := s.createdVolumeFiles[volumeName+":"+filePath]
	if !ok {
		return nil, nil, api.StatusErrorf(http.StatusNotFound, "not found")
	}
	return io.NopCloser(strings.NewReader(content)), nil, nil
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
	if args.Stdin != nil {
		data, err := io.ReadAll(args.Stdin)
		if err != nil {
			return nil, err
		}
		s.execStdin = append(s.execStdin, string(data))
	}
	if args.Stdout != nil {
		if _, err := args.Stdout.Write([]byte("archive")); err != nil {
			return nil, err
		}
	}
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
	mainServer := &fakeResourceServer{
		networks:           map[string]*api.Network{},
		volumes:            map[string]*api.StorageVolume{},
		instances:          map[string]*api.Instance{},
		createdFiles:       map[string]string{},
		createdVolumeFiles: map[string]string{},
	}
	infraServer := &fakeResourceServer{
		networks:           map[string]*api.Network{},
		volumes:            map[string]*api.StorageVolume{},
		instances:          map[string]*api.Instance{},
		createdFiles:       map[string]string{},
		createdVolumeFiles: map[string]string{},
	}
	server := fakeCreateServerForPlan(plan, mainServer, infraServer)
	creator := TenantCreator{Server: server}

	if err := creator.CreateTenant(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if server.createdProjects[plan.IncusProject] == nil {
		t.Fatal("expected main project to be created")
	}
	if server.createdProjects[plan.InfraProject] == nil {
		t.Fatal("expected infra project to be created")
	}
	if server.createdProjects[plan.NativeProject] == nil {
		t.Fatal("expected native project to be created")
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
		if got := server.createdProjects[plan.IncusProject].Config[key]; got != "true" {
			t.Fatalf("created main project config %s = %q, want true", key, got)
		}
	}
	if server.createdProjects[plan.InfraProject].Config["features.images"] != "true" {
		t.Fatalf("infra project config features.images = %q, want true", server.createdProjects[plan.InfraProject].Config["features.images"])
	}
	if len(mainServer.copiedImages) != len(plan.ImageAliases) {
		t.Fatalf("main copied images = %#v, want %d", mainServer.copiedImages, len(plan.ImageAliases))
	}
	if len(infraServer.copiedImages) != len(plan.InfraImageAliases) {
		t.Fatalf("infra copied images = %#v, want %d", infraServer.copiedImages, len(plan.InfraImageAliases))
	}
	if mainServer.createdNetwork == nil {
		t.Fatal("expected private network to be created")
	}
	if got := mainServer.createdNetwork.Config["ipv4.address"]; got != "10.248.0.1/24" {
		t.Fatalf("network ipv4.address = %q", got)
	}
	if len(mainServer.createdVolumes) != 3 {
		t.Fatalf("created volumes = %d, want 3", len(mainServer.createdVolumes))
	}
	if mainServer.createdVolumeFiles[tenant.CAVolumeName+":/ca.crt"] == "" {
		t.Fatal("expected CA certificate to be written")
	}
	if mainServer.createdVolumeFiles[tenant.CAVolumeName+":/ca.key"] == "" {
		t.Fatal("expected CA private key to be written")
	}
	if len(mainServer.createdInstances) != 0 {
		t.Fatalf("main server created instances = %d, want 0 (sidecars go to infra)", len(mainServer.createdInstances))
	}
	if len(infraServer.createdInstances) != 2 {
		t.Fatalf("infra server created instances = %d, want 2", len(infraServer.createdInstances))
	}
	if infraServer.createdInstances[0].Name != plan.TailscaleInstance {
		t.Fatalf("first sidecar = %q", infraServer.createdInstances[0].Name)
	}
	if got := infraServer.createdInstances[0].Devices["eth0"]["nictype"]; got != "bridged" {
		t.Fatalf("tailscale eth0 nictype = %q, want bridged", got)
	}
	if got := infraServer.createdInstances[0].Devices["eth0"]["parent"]; got != plan.PrivateNetwork {
		t.Fatalf("tailscale eth0 parent = %q, want %s", got, plan.PrivateNetwork)
	}
	if infraServer.createdInstances[1].Name != tenant.DNSName {
		t.Fatalf("second sidecar = %q", infraServer.createdInstances[1].Name)
	}
	if got := infraServer.createdInstances[1].Devices["eth0"]["nictype"]; got != "bridged" {
		t.Fatalf("dns eth0 nictype = %q, want bridged", got)
	}
	if got := infraServer.createdInstances[1].Devices["eth0"]["parent"]; got != plan.PrivateNetwork {
		t.Fatalf("dns eth0 parent = %q, want %s", got, plan.PrivateNetwork)
	}
	for _, profileName := range []string{"container", "default"} {
		profile := mainServer.profiles[profileName]
		if profile == nil {
			t.Fatalf("expected %s profile to be created", profileName)
		}
		if got := profile.Devices["root"]["pool"]; got != plan.StoragePool {
			t.Fatalf("%s root pool = %q", profileName, got)
		}
		if got := profile.Devices["root"]["path"]; got != "/" {
			t.Fatalf("%s root path = %q", profileName, got)
		}
		if got := profile.Devices["eth0"]["parent"]; got != plan.PrivateNetwork {
			t.Fatalf("%s eth0 parent = %q", profileName, got)
		}
	}
	if got := infraServer.createdFiles[tenant.DNSName+":/etc/coredns/Corefile"]; got == "" {
		t.Fatal("expected CoreDNS Corefile to be written to infra server")
	}
	if got := infraServer.createdFiles[tenant.DNSName+":/etc/coredns/zones/db.acme"]; got == "" {
		t.Fatal("expected CoreDNS zone to be written to infra server")
	}
	if len(infraServer.execCommands) != 3 {
		t.Fatalf("infra exec commands = %#v", infraServer.execCommands)
	}
	// First two execs configure networking for tailscale and dns sidecars.
	for i, name := range []string{plan.TailscaleInstance, tenant.DNSName} {
		if infraServer.execInstances[i] != name {
			t.Fatalf("exec[%d] instance = %q, want %q", i, infraServer.execInstances[i], name)
		}
		if got := strings.Join(infraServer.execCommands[i], " "); !strings.Contains(got, "sandcastle-sidecar-network.service") || !strings.Contains(got, "/usr/sbin/ip addr replace") {
			t.Fatalf("exec[%d] command = %q, want persistent sidecar network setup", i, got)
		}
	}
	// Third exec restarts CoreDNS.
	if infraServer.execInstances[2] != tenant.DNSName {
		t.Fatalf("exec[2] instance = %q, want %q", infraServer.execInstances[2], tenant.DNSName)
	}
	if got := strings.Join(infraServer.execCommands[2], " "); !strings.Contains(got, "coredns -conf /etc/coredns/Corefile") || !strings.Contains(got, "coredns.service") {
		t.Fatalf("exec[2] command = %q", got)
	}
}

func TestTenantCreatorOmitsSourceForDirStoragePool(t *testing.T) {
	plan := createPlanForTest(t)
	mainServer := &fakeResourceServer{
		networks:           map[string]*api.Network{},
		volumes:            map[string]*api.StorageVolume{},
		instances:          map[string]*api.Instance{},
		createdFiles:       map[string]string{},
		createdVolumeFiles: map[string]string{},
	}
	infraServer := &fakeResourceServer{
		networks:  map[string]*api.Network{},
		volumes:   map[string]*api.StorageVolume{},
		instances: map[string]*api.Instance{},
		createdFiles:       map[string]string{},
		createdVolumeFiles: map[string]string{},
	}
	server := fakeCreateServerForPlan(plan, mainServer, infraServer)
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
	mainServer := &fakeResourceServer{
		networks: map[string]*api.Network{plan.PrivateNetwork: {Name: plan.PrivateNetwork}},
		volumes: map[string]*api.StorageVolume{
			plan.HomeVolume:      {Name: plan.HomeVolume, Type: "custom"},
			plan.WorkspaceVolume: {Name: plan.WorkspaceVolume, Type: "custom"},
			plan.CAVolume:        {Name: plan.CAVolume, Type: "custom"},
		},
		instances:          map[string]*api.Instance{},
		createdFiles:       map[string]string{},
		createdVolumeFiles: map[string]string{},
	}
	infraServer := &fakeResourceServer{
		networks: map[string]*api.Network{},
		volumes:  map[string]*api.StorageVolume{},
		instances: map[string]*api.Instance{
			plan.TailscaleInstance: {Name: plan.TailscaleInstance, Status: "Running", StatusCode: api.Running},
			plan.DNSInstance:       {Name: plan.DNSInstance, Status: "Running", StatusCode: api.Running},
		},
		createdFiles:       map[string]string{},
		createdVolumeFiles: map[string]string{},
	}
	server := fakeCreateServerForPlan(plan, mainServer, infraServer)
	server.existingProjects = map[string]*api.Project{
		plan.IncusProject: {
			Name: plan.IncusProject,
			ProjectPut: api.ProjectPut{
				Description: "existing",
				Config:      api.ConfigMap{"features.images": "false"},
			},
		},
	}
	creator := TenantCreator{Server: server}

	if err := creator.CreateTenant(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if server.createdProjects[plan.IncusProject] != nil {
		t.Fatal("did not expect main project creation")
	}
	if server.updatedProjects[plan.IncusProject] == nil {
		t.Fatal("expected main project metadata update")
	}
	if server.updatedProjects[plan.IncusProject].Config["features.images"] != "false" {
		t.Fatalf("existing config was not preserved: %#v", server.updatedProjects[plan.IncusProject].Config)
	}
	if server.updatedProjects[plan.IncusProject].Config[meta.KeyTenant] != "acme" {
		t.Fatalf("managed metadata missing: %#v", server.updatedProjects[plan.IncusProject].Config)
	}
	if mainServer.createdNetwork != nil {
		t.Fatal("did not expect existing network creation")
	}
	if len(mainServer.createdVolumes) != 0 {
		t.Fatalf("created volumes = %d, want 0", len(mainServer.createdVolumes))
	}
	if len(mainServer.createdInstances) != 0 {
		t.Fatalf("main created instances = %d, want 0", len(mainServer.createdInstances))
	}
	if len(infraServer.createdInstances) != 0 {
		t.Fatalf("infra created instances = %d, want 0 (sidecars already running)", len(infraServer.createdInstances))
	}
	if infraServer.createdFiles[tenant.DNSName+":/etc/coredns/Corefile"] == "" {
		t.Fatal("expected DNS files to be refreshed in infra server")
	}
	if mainServer.createdVolumeFiles[tenant.CAVolumeName+":/ca.crt"] == "" {
		t.Fatal("expected CA files to be refreshed")
	}
}

func TestTenantCreatorStartsExistingStoppedSidecars(t *testing.T) {
	plan := createPlanForTest(t)
	mainServer := &fakeResourceServer{
		networks: map[string]*api.Network{plan.PrivateNetwork: {Name: plan.PrivateNetwork}},
		volumes: map[string]*api.StorageVolume{
			plan.HomeVolume:      {Name: plan.HomeVolume, Type: "custom"},
			plan.WorkspaceVolume: {Name: plan.WorkspaceVolume, Type: "custom"},
			plan.CAVolume:        {Name: plan.CAVolume, Type: "custom"},
		},
		instances:          map[string]*api.Instance{},
		createdFiles:       map[string]string{},
		createdVolumeFiles: map[string]string{},
	}
	infraServer := &fakeResourceServer{
		networks: map[string]*api.Network{},
		volumes:  map[string]*api.StorageVolume{},
		instances: map[string]*api.Instance{
			plan.TailscaleInstance: {Name: plan.TailscaleInstance, Status: "Stopped", StatusCode: api.Stopped},
			plan.DNSInstance:       {Name: plan.DNSInstance, Status: "Running", StatusCode: api.Running},
		},
		createdFiles:       map[string]string{},
		createdVolumeFiles: map[string]string{},
	}
	server := fakeCreateServerForPlan(plan, mainServer, infraServer)
	server.existingProjects = map[string]*api.Project{
		plan.IncusProject: {Name: plan.IncusProject},
	}
	creator := TenantCreator{Server: server}

	if err := creator.CreateTenant(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(infraServer.startedInstances) != 1 {
		t.Fatalf("started instances = %#v, want one", infraServer.startedInstances)
	}
	if infraServer.startedInstances[0] != plan.TailscaleInstance {
		t.Fatalf("started instance = %q", infraServer.startedInstances[0])
	}
}

func TestMergeDefaultProfilePreservesUnrelatedSettings(t *testing.T) {
	plan := createPlanForTest(t)
	desired := tenantContainerProfilePut(plan)
	existing := api.Profile{
		Name: "default",
		ProfilePut: api.ProfilePut{
			Description: "custom default",
			Config: api.ConfigMap{
				"limits.cpu": "2",
			},
			Devices: api.DevicesMap{
				"proxy": {
					"type":   "proxy",
					"listen": "tcp:0.0.0.0:8080",
				},
				"root": {
					"type": "disk",
					"pool": "old",
					"path": "/",
				},
			},
		},
	}

	merged := mergeDefaultProfile(existing, desired)

	if got := merged.Description; got != "custom default" {
		t.Fatalf("description = %q", got)
	}
	if got := merged.Config["limits.cpu"]; got != "2" {
		t.Fatalf("preserved config = %q", got)
	}
	if got := merged.Config[meta.KeyTenant]; got != plan.Reference {
		t.Fatalf("managed tenant metadata = %q", got)
	}
	if got := merged.Devices["proxy"]["listen"]; got != "tcp:0.0.0.0:8080" {
		t.Fatalf("preserved proxy device = %q", got)
	}
	if got := merged.Devices["root"]["pool"]; got != plan.StoragePool {
		t.Fatalf("root pool = %q", got)
	}
	if got := merged.Devices["eth0"]["parent"]; got != plan.PrivateNetwork {
		t.Fatalf("eth0 parent = %q", got)
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

func fakeCreateServerForPlan(plan tenant.CreatePlan, mainServer *fakeResourceServer, infraServer *fakeResourceServer) *fakeCreateServer {
	images := map[string]*api.Image{}
	imageAliases := map[string]*api.ImageAliasesEntry{}
	allAliases := append(append([]string{}, plan.ImageAliases...), plan.InfraImageAliases...)
	for _, alias := range allAliases {
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
	nativeServer := &fakeResourceServer{
		networks:           map[string]*api.Network{},
		volumes:            map[string]*api.StorageVolume{},
		instances:          map[string]*api.Instance{},
		createdFiles:       map[string]string{},
		createdVolumeFiles: map[string]string{},
	}
	return &fakeCreateServer{
		existingProjects: map[string]*api.Project{},
		images:           images,
		imageAliases:     imageAliases,
		projectServers: map[string]*fakeResourceServer{
			plan.IncusProject: mainServer,
			plan.InfraProject: infraServer,
			plan.NativeProject: nativeServer,
		},
	}
}
