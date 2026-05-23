package infra

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/caddy"
	"github.com/thieso2/sandcastle-incus/internal/certs"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

const (
	RouteBrokerName            = "sc-route-broker"
	RouteBrokerListen          = ":9443"
	RouteBrokerBinaryPath      = "/usr/local/bin/sandcastle-admin"
	RouteBrokerCertPath        = "/etc/sandcastle/route-broker/tls.crt"
	RouteBrokerKeyPath         = "/etc/sandcastle/route-broker/tls.key"
	RouteBrokerEnvPath         = "/etc/sandcastle/route-broker/env"
	RouteBrokerUnitPath        = "/etc/systemd/system/sandcastle-route-broker.service"
	RouteBrokerIncusSocketPath = "/var/lib/incus/unix.socket"
	AuthAppName                = "sc-auth-app"
	AuthAppListen              = ":9444"
	AuthAppBinaryPath          = "/usr/local/bin/sandcastle-admin"
	AuthAppDatabasePath        = "/var/lib/sandcastle/auth/auth.db"
	AuthAppEnvPath             = "/etc/sandcastle/auth-app/env"
	AuthAppUnitPath            = "/etc/systemd/system/sandcastle-auth-app.service"
	CaddyDataDir               = "/var/lib/caddy/.local/share/caddy"
	CaddyInternalRootCAPath    = "/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt"
	CaddyPKIDir                = "/etc/caddy/pki"
	CaddyPKIRootCertPath       = "/etc/caddy/pki/root.crt"
	CaddyPKIRootKeyPath        = "/etc/caddy/pki/root.key"
	InfrastructureNetworkName  = "incusbr0"
	NetworkdEth0Path           = "/etc/systemd/network/10-eth0.network"
	ResolverPath               = "/etc/resolv.conf"
	StaticNetworkScriptPath    = "/usr/local/sbin/sandcastle-infra-network"
	StaticNetworkUnitPath      = "/etc/systemd/system/sandcastle-infra-network.service"
)

type CreateRequest struct {
	UnixUser string
}

type DeleteRequest struct {
	Project string
	Purge   bool
}

type CreatePlan struct {
	Remote                string             `json:"remote"`
	Project               string             `json:"project"`
	StoragePool           string             `json:"storagePool"`
	TLSMode               string             `json:"tlsMode"`
	CaddyInstance         string             `json:"caddyInstance"`
	RouteBrokerInstance   string             `json:"routeBrokerInstance"`
	AuthAppInstance       string             `json:"authAppInstance"`
	ProjectMetadataConfig map[string]string  `json:"projectMetadataConfig"`
	Instances             []InstancePlan     `json:"instances"`
	RuntimeDirectories    []RuntimeDirectory `json:"runtimeDirectories"`
	RuntimeFiles          []RuntimeFile      `json:"runtimeFiles"`
	RuntimeBinaries       []RuntimeBinary    `json:"runtimeBinaries"`
	RuntimeCommands       []RuntimeCommand   `json:"runtimeCommands"`
	CaddyDataArchivePath  string             `json:"caddyDataArchivePath,omitempty"`
	DefaultUnixUser       string             `json:"defaultUnixUser,omitempty"`
	DeploymentName        string             `json:"deploymentName,omitempty"`
	SeedPath              string             `json:"seedPath,omitempty"`
}

type InstancePlan struct {
	Name       string            `json:"name"`
	Role       string            `json:"role"`
	ImageAlias string            `json:"imageAlias"`
	Config     map[string]string `json:"config"`
	Devices    map[string]Device `json:"devices"`
	Start      bool              `json:"start"`
}

type Device map[string]string

type RuntimeDirectory struct {
	Instance string `json:"instance"`
	Path     string `json:"path"`
	Mode     int    `json:"mode"`
}

type RuntimeFile struct {
	Instance string `json:"instance"`
	Path     string `json:"path"`
	Content  string `json:"content"`
	Mode     int    `json:"mode"`
}

type RuntimeBinary struct {
	Instance   string `json:"instance"`
	SourcePath string `json:"sourcePath"`
	TargetPath string `json:"targetPath"`
	Mode       int    `json:"mode"`
}

type RuntimeCommand struct {
	Instance    string   `json:"instance"`
	Description string   `json:"description"`
	Command     []string `json:"command"`
}

type StaticNetwork struct {
	Gateway      string            `json:"gateway"`
	PrefixLength int               `json:"prefixLength"`
	Addresses    map[string]string `json:"addresses"`
}

type Creator interface {
	CreateInfrastructure(context.Context, CreatePlan) error
}

type DeletePlan struct {
	Project            string   `json:"project"`
	IncusProjectPrefix string   `json:"incusProjectPrefix"`
	RuntimeInstances   []string `json:"runtimeInstances"`
	PurgeData          bool     `json:"purgeData"`
}

type Deleter interface {
	DeleteInfrastructure(context.Context, DeletePlan) error
}

func PlanCreate(admin config.Admin, request CreateRequest) (CreatePlan, error) {
	if err := admin.Validate(); err != nil {
		return CreatePlan{}, err
	}
	unixUser := strings.TrimSpace(request.UnixUser)
	if unixUser != "" {
		if err := naming.ValidateUnixUsername(unixUser); err != nil {
			return CreatePlan{}, err
		}
	}
	project := strings.TrimSpace(admin.InfrastructureProject)
	if project == "" {
		return CreatePlan{}, fmt.Errorf("infrastructure project is required")
	}
	projectConfig := map[string]string{
		meta.KeyKind:         "infrastructure",
		meta.KeyVersion:      "1",
		meta.Prefix + "name": project,
		"features.images":    "false",
	}
	brokerTLS, err := certs.GenerateSelfSignedServer("Sandcastle route broker", []string{RouteBrokerName, "localhost"}, time.Now().UTC())
	if err != nil {
		return CreatePlan{}, err
	}
	binaries, err := runtimeBinaries()
	if err != nil {
		return CreatePlan{}, err
	}
	tlsMode := infrastructureTLSMode(admin.InfrastructureTLSMode)
	return CreatePlan{
		Project:               project,
		StoragePool:           admin.StoragePool,
		TLSMode:               tlsMode,
		CaddyInstance:         route.InfrastructureCaddyName,
		RouteBrokerInstance:   RouteBrokerName,
		AuthAppInstance:       AuthAppName,
		ProjectMetadataConfig: projectConfig,
		Instances: []InstancePlan{
			instancePlan(admin, route.InfrastructureCaddyName, "caddy"),
			instancePlan(admin, RouteBrokerName, "route-broker"),
			instancePlan(admin, AuthAppName, "auth-app"),
		},
		RuntimeDirectories:   runtimeDirectories(tlsMode),
		RuntimeFiles:         runtimeFiles(admin, brokerTLS, unixUser),
		RuntimeBinaries:      binaries,
		RuntimeCommands:      runtimeCommands(),
		CaddyDataArchivePath: existingCaddyDataArchivePath(admin, tlsMode),
		DefaultUnixUser:      unixUser,
	}, nil
}

func runtimeDirectories(tlsMode string) []RuntimeDirectory {
	directories := []RuntimeDirectory{
		{Instance: route.InfrastructureCaddyName, Path: "/etc/caddy", Mode: 0o755},
		{Instance: RouteBrokerName, Path: "/etc/sandcastle", Mode: 0o755},
		{Instance: RouteBrokerName, Path: "/etc/sandcastle/route-broker", Mode: 0o700},
		{Instance: AuthAppName, Path: "/etc/sandcastle", Mode: 0o755},
		{Instance: AuthAppName, Path: "/etc/sandcastle/auth-app", Mode: 0o700},
		{Instance: AuthAppName, Path: "/var/lib/sandcastle/auth", Mode: 0o700},
	}
	if strings.TrimSpace(tlsMode) == "internal" {
		directories = append(directories, RuntimeDirectory{Instance: route.InfrastructureCaddyName, Path: CaddyPKIDir, Mode: 0o755})
	}
	return directories
}

func runtimeFiles(admin config.Admin, brokerTLS certs.KeyPair, defaultUnixUser string) []RuntimeFile {
	caddyFile := caddy.RenderInfrastructureWithOptions(nil, caddy.InfrastructureOptions{
		LetsEncryptEmail: admin.LetsEncryptEmail,
		TLSMode:          infrastructureTLSMode(admin.InfrastructureTLSMode),
		AuthHostname:     admin.AuthHostname,
		AuthUpstream:     "http://" + AuthAppName + AuthAppListen,
		InternalRootCert: CaddyPKIRootCertPath,
		InternalRootKey:  CaddyPKIRootKeyPath,
	})
	files := networkRuntimeFiles([]string{route.InfrastructureCaddyName, RouteBrokerName, AuthAppName})
	files = append(files,
		RuntimeFile{
			Instance: route.InfrastructureCaddyName,
			Path:     caddyFile.Path,
			Content:  caddyFile.Content,
			Mode:     caddyFile.Mode,
		},
		RuntimeFile{
			Instance: RouteBrokerName,
			Path:     RouteBrokerEnvPath,
			Content: strings.Join([]string{
				envLine("SANDCASTLE_ROUTE_BROKER_LISTEN", RouteBrokerListen),
				envLine("SANDCASTLE_ROUTE_BROKER_CERT", RouteBrokerCertPath),
				envLine("SANDCASTLE_ROUTE_BROKER_KEY", RouteBrokerKeyPath),
				envLine("SANDCASTLE_REMOTE", admin.Remote),
				envLine("SANDCASTLE_STORAGE_POOL", admin.StoragePool),
				envLine("SANDCASTLE_CIDR_POOL", admin.CIDRPool),
				envLine("SANDCASTLE_INCUS_PROJECT_PREFIX", admin.IncusProjectPrefix),
				envLine("SANDCASTLE_INFRA_PROJECT", admin.InfrastructureProject),
				envLine("SANDCASTLE_INFRA_HOST", admin.InfrastructureHost),
				envLine("SANDCASTLE_LETSENCRYPT_EMAIL", admin.LetsEncryptEmail),
				envLine("SANDCASTLE_INFRA_TLS_MODE", infrastructureTLSMode(admin.InfrastructureTLSMode)),
				envLine("SANDCASTLE_BASE_IMAGE", admin.Images.Base),
				envLine("SANDCASTLE_AI_IMAGE", admin.Images.AI),
				"",
			}, "\n"),
			Mode: 0o600,
		},
		RuntimeFile{
			Instance: RouteBrokerName,
			Path:     RouteBrokerCertPath,
			Content:  string(brokerTLS.CertificatePEM),
			Mode:     0o644,
		},
		RuntimeFile{
			Instance: RouteBrokerName,
			Path:     RouteBrokerKeyPath,
			Content:  string(brokerTLS.PrivateKeyPEM),
			Mode:     0o600,
		},
		RuntimeFile{
			Instance: RouteBrokerName,
			Path:     RouteBrokerUnitPath,
			Content:  "[Unit]\nDescription=Sandcastle route broker\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nEnvironmentFile=" + RouteBrokerEnvPath + "\nExecStart=" + RouteBrokerBinaryPath + " route-broker serve --listen ${SANDCASTLE_ROUTE_BROKER_LISTEN} --cert ${SANDCASTLE_ROUTE_BROKER_CERT} --key ${SANDCASTLE_ROUTE_BROKER_KEY}\nRestart=on-failure\n\n[Install]\nWantedBy=multi-user.target\n",
			Mode:     0o644,
		},
		RuntimeFile{
			Instance: AuthAppName,
			Path:     AuthAppEnvPath,
			Content: strings.Join([]string{
				envLine("SANDCASTLE_AUTH_LISTEN", AuthAppListen),
				envLine("SANDCASTLE_AUTH_DB", AuthAppDatabasePath),
				envLine("SANDCASTLE_AUTH_HOSTNAME", admin.AuthHostname),
				envLine("SANDCASTLE_AUTH_GITHUB_CLIENT_ID", admin.AuthGitHubClientID),
				envLine("SANDCASTLE_AUTH_GITHUB_CLIENT_SECRET", admin.AuthGitHubClientSecret),
				envLine("SANDCASTLE_AUTH_ADMIN_GITHUB_USERS", strings.Join(admin.AuthAdminGitHubUsers, ",")),
				envLine("SANDCASTLE_AUTH_DEBUG_DEVICE_USER", admin.AuthDebugDeviceUser),
				envLine("SANDCASTLE_AUTH_DEFAULT_UNIX_USER", defaultUnixUser),
				envLine("SANDCASTLE_AUTH_TAILSCALE_AUTHKEY", admin.AuthTailscaleAuthKey),
				envLine("SANDCASTLE_REMOTE", admin.Remote),
				envLine("SANDCASTLE_STORAGE_POOL", admin.StoragePool),
				envLine("SANDCASTLE_CIDR_POOL", admin.CIDRPool),
				envLine("SANDCASTLE_INCUS_PROJECT_PREFIX", admin.IncusProjectPrefix),
				envLine("SANDCASTLE_INFRA_PROJECT", admin.InfrastructureProject),
				envLine("SANDCASTLE_INFRA_TLS_MODE", infrastructureTLSMode(admin.InfrastructureTLSMode)),
				envLine("SANDCASTLE_BASE_IMAGE", admin.Images.Base),
				envLine("SANDCASTLE_AI_IMAGE", admin.Images.AI),
				"",
			}, "\n"),
			Mode: 0o600,
		},
		RuntimeFile{
			Instance: AuthAppName,
			Path:     AuthAppUnitPath,
			Content:  "[Unit]\nDescription=Sandcastle auth app\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nEnvironmentFile=" + AuthAppEnvPath + "\nExecStart=" + AuthAppBinaryPath + " auth-app serve --listen ${SANDCASTLE_AUTH_LISTEN} --database ${SANDCASTLE_AUTH_DB} --auth-hostname ${SANDCASTLE_AUTH_HOSTNAME} --github-client-id ${SANDCASTLE_AUTH_GITHUB_CLIENT_ID} --github-client-secret ${SANDCASTLE_AUTH_GITHUB_CLIENT_SECRET} --admin-github-users ${SANDCASTLE_AUTH_ADMIN_GITHUB_USERS} --debug-device-user ${SANDCASTLE_AUTH_DEBUG_DEVICE_USER} --default-unix-user ${SANDCASTLE_AUTH_DEFAULT_UNIX_USER} --tailscale-auth-key ${SANDCASTLE_AUTH_TAILSCALE_AUTHKEY}\nRestart=on-failure\n\n[Install]\nWantedBy=multi-user.target\n",
			Mode:     0o644,
		},
	)
	return files
}

func ApplyInternalCA(plan CreatePlan, ca certs.KeyPair) CreatePlan {
	if strings.TrimSpace(plan.TLSMode) != "internal" {
		return plan
	}
	plan.RuntimeFiles = append(plan.RuntimeFiles,
		RuntimeFile{
			Instance: route.InfrastructureCaddyName,
			Path:     CaddyPKIRootCertPath,
			Content:  string(ca.CertificatePEM),
			Mode:     0o644,
		},
		RuntimeFile{
			Instance: route.InfrastructureCaddyName,
			Path:     CaddyPKIRootKeyPath,
			Content:  string(ca.PrivateKeyPEM),
			Mode:     0o644,
		},
	)
	return plan
}

func infrastructureTLSMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return config.DefaultInfrastructureTLSMode
	}
	return mode
}

func LoadOrCreatePersistentInternalCA(admin config.Admin) (certs.KeyPair, error) {
	certPath, keyPath := PersistentInternalCAPaths(admin)
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil && len(certPEM) > 0 && len(keyPEM) > 0 {
		return certs.KeyPair{CertificatePEM: certPEM, PrivateKeyPEM: keyPEM}, nil
	}
	ca, err := certs.GenerateCA("Sandcastle infrastructure debug CA", time.Now().UTC())
	if err != nil {
		return certs.KeyPair{}, err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return certs.KeyPair{}, err
	}
	if err := os.WriteFile(certPath, ca.CertificatePEM, 0o644); err != nil {
		return certs.KeyPair{}, err
	}
	if err := os.WriteFile(keyPath, ca.PrivateKeyPEM, 0o600); err != nil {
		return certs.KeyPair{}, err
	}
	return ca, nil
}

func PersistentInternalCAPaths(admin config.Admin) (string, string) {
	dir := strings.TrimSpace(os.Getenv("SANDCASTLE_INFRA_CA_DIR"))
	if dir == "" {
		dir = filepath.Join(config.DefaultConfigDir(), "infra-ca", pathSafe(admin.Remote), pathSafe(admin.InfrastructureProject))
	}
	return filepath.Join(dir, "root.crt"), filepath.Join(dir, "root.key")
}

func CaddyDataArchivePath(admin config.Admin) string {
	path := strings.TrimSpace(os.Getenv("SANDCASTLE_INFRA_CADDY_DATA_ARCHIVE"))
	if path != "" {
		return path
	}
	return filepath.Join(config.DefaultConfigDir(), "infra-caddy-data", pathSafe(admin.Remote), pathSafe(admin.InfrastructureProject), "caddy-data.tgz")
}

func existingCaddyDataArchivePath(admin config.Admin, tlsMode string) string {
	if !strings.EqualFold(strings.TrimSpace(tlsMode), "acme") {
		return ""
	}
	path := CaddyDataArchivePath(admin)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return ""
	}
	return path
}

func pathSafe(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return replacer.Replace(value)
}

func networkRuntimeFiles(instances []string) []RuntimeFile {
	content := strings.Join([]string{
		"[Match]",
		"Name=eth0",
		"",
		"[Network]",
		"DHCP=yes",
		"IPv6AcceptRA=yes",
		"",
	}, "\n")
	files := make([]RuntimeFile, 0, len(instances))
	for _, instance := range instances {
		files = append(files, RuntimeFile{
			Instance: instance,
			Path:     NetworkdEth0Path,
			Content:  content,
			Mode:     0o644,
		})
	}
	return files
}

func networkBootstrapShell() []string {
	return []string{
		"systemctl enable systemd-networkd",
		"systemctl restart systemd-networkd",
		"networkctl renew eth0 || true",
		"for i in $(seq 1 50); do ip -4 addr show dev eth0 | grep -q 'inet ' && break; sleep 0.1; done",
		"ip -4 addr show dev eth0 | grep -q 'inet '",
	}
}

func ApplyStaticNetwork(plan CreatePlan, network StaticNetwork) CreatePlan {
	plan.RuntimeFiles = withoutRuntimePath(plan.RuntimeFiles, NetworkdEth0Path)
	for _, instance := range plan.Instances {
		address := strings.TrimSpace(network.Addresses[instance.Name])
		if address == "" {
			continue
		}
		plan.RuntimeFiles = append(plan.RuntimeFiles,
			RuntimeFile{
				Instance: instance.Name,
				Path:     StaticNetworkScriptPath,
				Content:  staticNetworkScript(address, network.PrefixLength, network.Gateway),
				Mode:     0o755,
			},
			RuntimeFile{
				Instance: instance.Name,
				Path:     StaticNetworkUnitPath,
				Content:  staticNetworkUnit(),
				Mode:     0o644,
			},
			RuntimeFile{
				Instance: instance.Name,
				Path:     ResolverPath,
				Content:  "nameserver " + network.Gateway + "\n",
				Mode:     0o644,
			},
		)
	}
	if authAddress := strings.TrimSpace(network.Addresses[AuthAppName]); authAddress != "" {
		replaceRuntimeFileContent(&plan, route.InfrastructureCaddyName, "/etc/caddy/Caddyfile", func(content string) string {
			return strings.ReplaceAll(content, "http://"+AuthAppName+AuthAppListen, "http://"+authAddress+AuthAppListen)
		})
	}
	oldBootstrap := strings.Join(networkBootstrapShell(), "; ")
	newBootstrap := strings.Join(staticNetworkBootstrapShell(), "; ")
	for i := range plan.RuntimeCommands {
		if len(plan.RuntimeCommands[i].Command) >= 3 {
			plan.RuntimeCommands[i].Command[2] = strings.Replace(plan.RuntimeCommands[i].Command[2], oldBootstrap, newBootstrap, 1)
		}
	}
	return plan
}

func withoutRuntimePath(files []RuntimeFile, path string) []RuntimeFile {
	output := files[:0]
	for _, file := range files {
		if file.Path != path {
			output = append(output, file)
		}
	}
	return output
}

func replaceRuntimeFileContent(plan *CreatePlan, instance string, path string, replace func(string) string) {
	for i := range plan.RuntimeFiles {
		if plan.RuntimeFiles[i].Instance == instance && plan.RuntimeFiles[i].Path == path {
			plan.RuntimeFiles[i].Content = replace(plan.RuntimeFiles[i].Content)
		}
	}
}

func staticNetworkScript(address string, prefixLength int, gateway string) string {
	return strings.Join([]string{
		"#!/bin/sh",
		"set -eu",
		"/usr/sbin/ip link set eth0 up",
		fmt.Sprintf("/usr/sbin/ip addr replace %s/%d dev eth0", address, prefixLength),
		"/usr/sbin/ip route replace default via " + gateway,
		"",
	}, "\n")
}

func staticNetworkUnit() string {
	return strings.Join([]string{
		"[Unit]",
		"Description=Sandcastle infrastructure static network",
		"After=network-pre.target",
		"Before=network-online.target",
		"",
		"[Service]",
		"Type=oneshot",
		"ExecStart=" + StaticNetworkScriptPath,
		"RemainAfterExit=yes",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}, "\n")
}

func staticNetworkBootstrapShell() []string {
	return []string{
		"systemctl daemon-reload",
		"systemctl enable sandcastle-infra-network.service",
		"systemctl restart sandcastle-infra-network.service",
		"ip -4 addr show dev eth0 | grep -q 'inet '",
	}
}

func envLine(key string, value string) string {
	return key + "=" + shellQuote(value)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func RuntimeBinarySource() string {
	source := strings.TrimSpace(os.Getenv("SANDCASTLE_ADMIN_BIN"))
	if source == "" {
		source = strings.TrimSpace(os.Getenv("SANDCASTLE_BIN"))
	}
	if source == "" {
		if executable, err := os.Executable(); err == nil {
			source = defaultRuntimeBinarySource(executable)
		}
	}
	return source
}

func defaultRuntimeBinarySource(executable string) string {
	if runtime.GOOS == "linux" {
		return executable
	}
	dir := filepath.Dir(executable)
	base := filepath.Base(executable)
	for _, candidate := range []string{
		filepath.Join(dir, "linux-amd64", base),
		filepath.Join(dir, "linux-amd64", "sc-adm"),
		filepath.Join(dir, "linux-amd64", "sandcastle-admin"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return executable
}

func runtimeBinaries() ([]RuntimeBinary, error) {
	source := RuntimeBinarySource()
	if source == "" {
		return nil, nil
	}
	if err := rejectUnsupportedRuntimeBinary(source); err != nil {
		return nil, err
	}
	return []RuntimeBinary{
		{Instance: RouteBrokerName, SourcePath: source, TargetPath: RouteBrokerBinaryPath, Mode: 0o755},
		{Instance: AuthAppName, SourcePath: source, TargetPath: AuthAppBinaryPath, Mode: 0o755},
	}, nil
}

func rejectUnsupportedRuntimeBinary(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open infrastructure runtime binary %s: %w", path, err)
	}
	defer file.Close()
	var magic [4]byte
	n, err := file.Read(magic[:])
	if err != nil && err != io.EOF {
		return fmt.Errorf("read infrastructure runtime binary %s: %w", path, err)
	}
	if n < len(magic) {
		return nil
	}
	switch string(magic[:]) {
	case "\xcf\xfa\xed\xfe", "\xce\xfa\xed\xfe", "\xfe\xed\xfa\xcf", "\xfe\xed\xfa\xce", "\xca\xfe\xba\xbe", "\xca\xfe\xba\xbf":
		return fmt.Errorf("infrastructure runtime binary %s is a macOS Mach-O binary; set SANDCASTLE_ADMIN_BIN to a Linux amd64 sandcastle-admin binary, for example bin/linux-amd64/sc-adm after running `mise run build:linux-amd64`", path)
	default:
		return nil
	}
}

func runtimeCommands() []RuntimeCommand {
	return []RuntimeCommand{
		{
			Instance:    route.InfrastructureCaddyName,
			Description: "start infrastructure Caddy",
			Command: []string{"/bin/sh", "-lc", strings.Join(append(networkBootstrapShell(),
				"install -d /etc/caddy",
				"chmod 0755 /etc/caddy/pki 2>/dev/null || true",
				"chmod 0644 /etc/caddy/pki/root.crt /etc/caddy/pki/root.key 2>/dev/null || true",
				"systemctl restart caddy",
				"for i in $(seq 1 50); do systemctl is-active caddy >/dev/null 2>&1 && exit 0; sleep 0.1; done",
				"systemctl is-active caddy",
			), "; ")},
		},
		{
			Instance:    RouteBrokerName,
			Description: "start route broker service",
			Command: []string{"/bin/sh", "-lc", strings.Join(append(networkBootstrapShell(),
				"systemctl daemon-reload",
				"systemctl enable sandcastle-route-broker",
				"systemctl restart sandcastle-route-broker",
				"for i in $(seq 1 50); do systemctl is-active sandcastle-route-broker >/dev/null 2>&1 && exit 0; sleep 0.1; done",
				"systemctl is-active sandcastle-route-broker",
			), "; ")},
		},
		{
			Instance:    AuthAppName,
			Description: "start auth app service",
			Command: []string{"/bin/sh", "-lc", strings.Join(append(networkBootstrapShell(),
				"systemctl daemon-reload",
				"systemctl enable sandcastle-auth-app",
				"systemctl restart sandcastle-auth-app",
				"for i in $(seq 1 50); do systemctl is-active sandcastle-auth-app >/dev/null 2>&1 && exit 0; sleep 0.1; done",
				"systemctl is-active sandcastle-auth-app",
			), "; ")},
		},
	}
}

func instancePlan(admin config.Admin, name string, role string) InstancePlan {
	instanceConfig := map[string]string{
		meta.KeyKind:         "infrastructure",
		meta.KeyVersion:      "1",
		meta.Prefix + "name": name,
		meta.Prefix + "role": role,
	}
	devices := map[string]Device{
		"root": {
			"type": "disk",
			"pool": admin.StoragePool,
			"path": "/",
		},
		"eth0": {
			"type":    "nic",
			"nictype": "bridged",
			"parent":  InfrastructureNetworkName,
		},
	}
	if name == route.InfrastructureCaddyName {
		devices["http"] = Device{
			"type":    "proxy",
			"listen":  "tcp:0.0.0.0:80",
			"connect": "tcp:127.0.0.1:80",
		}
		devices["https"] = Device{
			"type":    "proxy",
			"listen":  "tcp:0.0.0.0:443",
			"connect": "tcp:127.0.0.1:443",
		}
	}
	if (name == RouteBrokerName || name == AuthAppName) && strings.TrimSpace(admin.RouteBrokerIncusSocket) != "" {
		devices["incus-socket"] = Device{
			"type":   "disk",
			"source": strings.TrimSpace(admin.RouteBrokerIncusSocket),
			"path":   RouteBrokerIncusSocketPath,
		}
		instanceConfig["security.privileged"] = "true"
	}
	return InstancePlan{
		Name:       name,
		Role:       role,
		ImageAlias: admin.Images.Base,
		Config:     instanceConfig,
		Devices:    devices,
		Start:      true,
	}
}

func PlanDelete(admin config.Admin, request DeleteRequest) (DeletePlan, error) {
	if err := admin.Validate(); err != nil {
		return DeletePlan{}, err
	}
	project := strings.TrimSpace(request.Project)
	if project == "" {
		project = strings.TrimSpace(admin.InfrastructureProject)
	}
	if project == "" {
		return DeletePlan{}, fmt.Errorf("infrastructure project is required")
	}
	if err := naming.ValidateIncusProjectName(project); err != nil {
		return DeletePlan{}, err
	}
	return DeletePlan{
		Project:            project,
		IncusProjectPrefix: strings.TrimSpace(admin.IncusProjectPrefix),
		RuntimeInstances:   []string{route.InfrastructureCaddyName, RouteBrokerName, AuthAppName},
		PurgeData:          request.Purge,
	}, nil
}
