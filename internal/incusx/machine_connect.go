package incusx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"golang.org/x/term"
)

type MachineConnectServer interface {
	UseProject(name string) MachineConnectResourceServer
}

type MachineConnectResourceServer interface {
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}

type MachineConnector struct {
	Remote     string
	ConfigPath string
	Server     MachineConnectServer
	Runner     SSHRunner
	MoshRunner SSHRunner
	Log        func(string)
	SSHVerbose bool
	Cache      *ConnectCache
}

func NewMachineConnector(remote string) MachineConnector {
	return MachineConnector{Remote: remote}
}

func (e MachineConnector) WithVerbose(enabled bool, w io.Writer) MachineConnector {
	if enabled {
		e.SSHVerbose = true
		e.Log = func(msg string) { fmt.Fprintln(w, "[machine-connect] "+msg) }
	}
	return e
}

func (e MachineConnector) WithConnectCache(cache ConnectCache) MachineConnector {
	e.Cache = &cache
	return e
}

func (e MachineConnector) log(msg string) {
	if e.Log != nil {
		e.Log(msg)
	}
}

type SSHRunner interface {
	Run(ctx context.Context, session machine.ConnectSession, args ...string) error
}

type LocalSSHRunner struct{}
type LocalMoshRunner struct{}

var (
	sshControlMasterConfigTimeout = 500 * time.Millisecond
	sshControlMasterDialTimeout   = 100 * time.Millisecond
)

func (e MachineConnector) ConnectMachine(ctx context.Context, plan machine.ConnectPlan, session machine.ConnectSession) error {
	if plan.Managed {
		return e.connectManagedMachine(ctx, plan, session)
	}
	return e.connectUnmanagedMachine(ctx, plan, session)
}

func (e MachineConnector) connectManagedMachine(ctx context.Context, plan machine.ConnectPlan, session machine.ConnectSession) error {
	if strings.TrimSpace(plan.SSHHost) == "" {
		return fmt.Errorf("machine %s has no SSH host", plan.InstanceName)
	}
	identityKey := sshIdentityCacheKey(plan)
	identityPath := e.sshIdentity(ctx, plan, identityKey)
	e.pruneDeadSSHControlMaster(ctx, plan, identityPath)
	if plan.Mosh {
		runner := e.MoshRunner
		if runner == nil {
			runner = LocalMoshRunner{}
		}
		args := moshArgs(plan, e.SSHVerbose, identityPath)
		e.log("run " + shellCommandLine("mosh", args))
		if err := runner.Run(ctx, session, args...); err != nil {
			return fmt.Errorf("mosh to machine %s: %w", plan.InstanceName, err)
		}
		return nil
	}
	runner := e.Runner
	if runner == nil {
		runner = LocalSSHRunner{}
	}
	args := sshArgs(plan, e.SSHVerbose, identityPath)
	e.log("run " + shellCommandLine("ssh", args))
	if err := runner.Run(ctx, session, args...); err != nil {
		if identityPath != "" && e.Cache != nil && isSSHExit255(err) {
			e.Cache.InvalidateSSHIdentity(identityKey)
		}
		return fmt.Errorf("ssh to machine %s: %w", plan.InstanceName, err)
	}
	return nil
}

func sshArgs(plan machine.ConnectPlan, verbose bool, identityPath string) []string {
	args := sshSetupArgs(plan, verbose, identityPath)
	if plan.Interactive {
		args = append(args, "-t")
	}
	args = append(args, plan.LinuxUser+"@"+plan.SSHHost)
	if len(plan.Command) > 0 {
		args = append(args, remoteCommand(plan))
	}
	return args
}

func sshSetupArgs(plan machine.ConnectPlan, verbose bool, identityPath string) []string {
	args := []string{
		"-A",
		"-o", "CheckHostIP=no",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	if strings.TrimSpace(plan.KnownHostsFile) != "" {
		args = append(args, "-o", "UserKnownHostsFile="+plan.KnownHostsFile)
	}
	if verbose {
		args = append(args, "-v")
	}
	if plan.HostKeyAlias != "" {
		args = append(args, "-o", "HostKeyAlias="+plan.HostKeyAlias)
	}
	if strings.TrimSpace(identityPath) != "" {
		args = append(args, "-o", "IdentitiesOnly=yes", "-i", identityPath)
	}
	return args
}

type sshControlMasterConfig struct {
	Master string
	Path   string
}

func (e MachineConnector) pruneDeadSSHControlMaster(ctx context.Context, plan machine.ConnectPlan, identityPath string) {
	config, err := sshEffectiveControlMasterConfig(ctx, plan, identityPath)
	if err != nil || !config.enabled() {
		return
	}
	info, err := os.Stat(config.Path)
	if err != nil {
		return
	}
	if info.Mode()&os.ModeSocket == 0 {
		return
	}
	if sshControlSocketAccepts(config.Path) {
		return
	}
	if err := os.Remove(config.Path); err != nil {
		e.log("remove stale ssh control socket failed " + config.Path + ": " + err.Error())
		return
	}
	e.log("removed stale ssh control socket " + config.Path)
}

func sshEffectiveControlMasterConfig(ctx context.Context, plan machine.ConnectPlan, identityPath string) (sshControlMasterConfig, error) {
	configCtx, cancel := context.WithTimeout(ctx, sshControlMasterConfigTimeout)
	defer cancel()
	args := append([]string{"-G"}, sshSetupArgs(plan, false, identityPath)...)
	args = append(args, plan.LinuxUser+"@"+plan.SSHHost)
	command := exec.CommandContext(configCtx, "ssh", args...)
	command.Stdin = strings.NewReader("")
	output, err := command.Output()
	if err != nil {
		return sshControlMasterConfig{}, err
	}
	return parseSSHControlMasterConfig(string(output)), nil
}

func parseSSHControlMasterConfig(output string) sshControlMasterConfig {
	var config sshControlMasterConfig
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "controlmaster":
			config.Master = strings.ToLower(strings.TrimSpace(value))
		case "controlpath":
			config.Path = strings.TrimSpace(value)
		}
	}
	return config
}

func (c sshControlMasterConfig) enabled() bool {
	if strings.TrimSpace(c.Path) == "" || strings.EqualFold(c.Path, "none") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(c.Master)) {
	case "yes", "ask", "auto", "autoask":
		return true
	default:
		return false
	}
}

func sshControlSocketAccepts(path string) bool {
	conn, err := net.DialTimeout("unix", path, sshControlMasterDialTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func moshArgs(plan machine.ConnectPlan, verbose bool, identityPath string) []string {
	sshCommand := shellCommandLine("ssh", sshSetupArgs(plan, verbose, identityPath))
	args := []string{
		"--ssh=" + sshCommand,
		plan.LinuxUser + "@" + plan.SSHHost,
	}
	if command := remoteCommand(plan); command != "" {
		args = append(args, "--", "bash", "-lc", command)
	}
	return args
}

func (e MachineConnector) sshIdentity(ctx context.Context, plan machine.ConnectPlan, key string) string {
	if e.Cache == nil || strings.TrimSpace(key) == "" {
		return ""
	}
	if identityPath, ok := e.Cache.LookupSSHIdentity(key); ok {
		if sshIdentityFileExists(identityPath) {
			e.log("ssh identity cache hit " + identityPath)
			return identityPath
		}
		e.Cache.InvalidateSSHIdentity(key)
	}
	for _, candidate := range sshIdentityCandidates(ctx, plan) {
		if err := probeSSHIdentity(ctx, plan, candidate); err == nil {
			e.Cache.StoreSSHIdentity(key, candidate)
			e.log("ssh identity cache store " + candidate)
			return candidate
		}
	}
	return ""
}

func sshIdentityCacheKey(plan machine.ConnectPlan) string {
	tenantName := strings.TrimSpace(plan.Tenant.Tenant)
	projectName := strings.TrimSpace(plan.Project)
	machineName := strings.TrimSpace(plan.Name)
	if tenantName == "" || projectName == "" || machineName == "" {
		return ""
	}
	return tenantName + ":" + projectName + "/" + machineName
}

func sshIdentityCandidates(ctx context.Context, plan machine.ConnectPlan) []string {
	args := []string{
		"-G",
		"-o", "CheckHostIP=no",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	if plan.HostKeyAlias != "" {
		args = append(args, "-o", "HostKeyAlias="+plan.HostKeyAlias)
	}
	args = append(args, plan.LinuxUser+"@"+plan.SSHHost)
	output, err := exec.CommandContext(ctx, "ssh", args...).Output()
	if err != nil {
		return defaultSSHIdentityCandidates()
	}
	var candidates []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || key != "identityfile" {
			continue
		}
		identityPath := expandSSHIdentityPath(value)
		if identityPath == "" || seen[identityPath] || !sshIdentityFileExists(identityPath) {
			continue
		}
		seen[identityPath] = true
		candidates = append(candidates, identityPath)
	}
	if len(candidates) == 0 {
		return defaultSSHIdentityCandidates()
	}
	return prioritizeSSHIdentityCandidates(candidates)
}

func defaultSSHIdentityCandidates() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	names := []string{"id_ed25519", "id_ecdsa", "id_rsa"}
	candidates := make([]string, 0, len(names))
	for _, name := range names {
		identityPath := filepath.Join(home, ".ssh", name)
		if sshIdentityFileExists(identityPath) {
			candidates = append(candidates, identityPath)
		}
	}
	return candidates
}

func prioritizeSSHIdentityCandidates(candidates []string) []string {
	output := append([]string{}, candidates...)
	for i := 1; i < len(output); i++ {
		for j := i; j > 0 && sshIdentityPriority(output[j]) < sshIdentityPriority(output[j-1]); j-- {
			output[j], output[j-1] = output[j-1], output[j]
		}
	}
	return output
}

func sshIdentityPriority(identityPath string) int {
	name := filepath.Base(identityPath)
	switch {
	case strings.Contains(name, "ed25519"):
		return 0
	case strings.Contains(name, "ecdsa"):
		return 1
	case strings.Contains(name, "rsa"):
		return 2
	default:
		return 3
	}
}

func expandSSHIdentityPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "none" {
		return ""
	}
	if strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
	}
	if !filepath.IsAbs(value) {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			value = filepath.Join(home, value)
		}
	}
	return filepath.Clean(value)
}

func sshIdentityFileExists(identityPath string) bool {
	info, err := os.Stat(identityPath)
	return err == nil && !info.IsDir()
}

func probeSSHIdentity(ctx context.Context, plan machine.ConnectPlan, identityPath string) error {
	args := []string{
		"-A",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=3",
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
		"-o", "PreferredAuthentications=publickey",
		"-o", "PasswordAuthentication=no",
		"-o", "KbdInteractiveAuthentication=no",
		"-o", "NumberOfPasswordPrompts=0",
		"-o", "CheckHostIP=no",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	if plan.HostKeyAlias != "" {
		args = append(args, "-o", "HostKeyAlias="+plan.HostKeyAlias)
	}
	args = append(args, "-o", "IdentitiesOnly=yes", "-i", identityPath, plan.LinuxUser+"@"+plan.SSHHost, "true")
	command := exec.CommandContext(ctx, "ssh", args...)
	command.Stdin = strings.NewReader("")
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run()
}

func isSSHExit255(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == 255
}

func remoteCommand(plan machine.ConnectPlan) string {
	command := remoteShellCommand(plan.Command)
	if command == "" || (plan.Interactive && command == "/bin/bash -l") {
		command = "exec /bin/bash -l"
	} else {
		command = "exec /bin/bash -lc " + shellRemoteQuote(command)
	}
	if strings.TrimSpace(plan.WorkingDir) == "" {
		return command
	}
	return "cd " + shellRemoteQuote(plan.WorkingDir) + " && " + command
}

func remoteShellCommand(args []string) string {
	if len(args) == 1 {
		return strings.TrimSpace(args[0])
	}
	return strings.TrimSpace(strings.Join(shellRemoteQuoteArgs(args), " "))
}

func shellRemoteQuoteArgs(args []string) []string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellRemoteQuote(arg))
	}
	return quoted
}

func shellRemoteQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z' ||
			r >= 'a' && r <= 'z' ||
			r >= '0' && r <= '9' ||
			strings.ContainsRune("-_./:=@%", r))
	}) == -1 {
		return value
	}
	return shellQuote(value)
}

func shellCommandLine(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellDisplayQuote(name))
	for _, arg := range args {
		parts = append(parts, shellDisplayQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellDisplayQuote(value string) string {
	if value == "" {
		return `""`
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z' ||
			r >= 'a' && r <= 'z' ||
			r >= '0' && r <= '9' ||
			strings.ContainsRune("-_./:=@", r))
	}) == -1 {
		return value
	}
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		`$`, `\$`,
		"`", "\\`",
		"\n", `\n`,
	)
	return `"` + replacer.Replace(value) + `"`
}

func (r LocalSSHRunner) Run(ctx context.Context, session machine.ConnectSession, args ...string) error {
	command := exec.CommandContext(ctx, "ssh", args...)
	command.Stdin = session.Stdin
	command.Stdout = session.Stdout
	command.Stderr = session.Stderr
	return command.Run()
}

func (r LocalMoshRunner) Run(ctx context.Context, session machine.ConnectSession, args ...string) error {
	command := exec.CommandContext(ctx, "mosh", args...)
	command.Stdin = session.Stdin
	command.Stdout = session.Stdout
	command.Stderr = session.Stderr
	return command.Run()
}

func (e MachineConnector) connectUnmanagedMachine(ctx context.Context, plan machine.ConnectPlan, session machine.ConnectSession) error {
	server := e.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(e.ConfigPath)
		if err != nil {
			return fmt.Errorf("load Incus config: %w", err)
		}
		remote := e.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkMachineConnectServer{inner: instanceServer}
	}
	projectServer := server.UseProject(plan.Tenant.IncusName)
	userID := plan.UserID
	groupID := plan.GroupID
	if userID == 0 && plan.LinuxUser != "root" {
		userID = machine.DefaultLinuxUID
	}
	if groupID == 0 && plan.LinuxUser != "root" {
		groupID = machine.DefaultLinuxGID
	}
	home := "/home/" + plan.LinuxUser
	if plan.LinuxUser == "root" {
		home = "/root"
	}
	execPost := api.InstanceExecPost{
		Command:     plan.Command,
		Cwd:         plan.WorkingDir,
		User:        uint32(userID),
		Group:       uint32(groupID),
		Interactive: plan.Interactive,
		WaitForWS:   true,
		Environment: map[string]string{
			"HOME": home,
			"USER": plan.LinuxUser,
		},
	}
	execPost.RecordOutput = false
	if execPost.Interactive {
		if file, ok := session.Stdin.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
			width, height, err := term.GetSize(int(file.Fd()))
			if err == nil {
				execPost.Width = width
				execPost.Height = height
			}
			oldState, err := term.MakeRaw(int(file.Fd()))
			if err == nil {
				defer term.Restore(int(file.Fd()), oldState)
			}
		}
	}
	dataDone := make(chan bool)
	op, err := projectServer.ExecInstance(plan.InstanceName, execPost, &incus.InstanceExecArgs{
		Stdin:    session.Stdin,
		Stdout:   session.Stdout,
		Stderr:   session.Stderr,
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("connect to machine %s: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for machine %s session: %w", plan.InstanceName, err)
	}
	<-dataDone
	return nil
}

type sdkMachineConnectServer struct {
	inner incus.InstanceServer
}

func (s sdkMachineConnectServer) UseProject(name string) MachineConnectResourceServer {
	return sdkMachineConnectResourceServer{inner: s.inner.UseProject(name)}
}

type sdkMachineConnectResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkMachineConnectResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return s.inner.ExecInstance(instanceName, exec, args)
}
