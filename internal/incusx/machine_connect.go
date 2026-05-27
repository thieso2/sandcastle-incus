package incusx

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

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
	Log        func(string)
	SSHVerbose bool
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

func (e MachineConnector) log(msg string) {
	if e.Log != nil {
		e.Log(msg)
	}
}

type SSHRunner interface {
	Run(ctx context.Context, session machine.ConnectSession, args ...string) error
}

type LocalSSHRunner struct{}

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
	runner := e.Runner
	if runner == nil {
		runner = LocalSSHRunner{}
	}
	args := sshArgs(plan, e.SSHVerbose)
	e.log("run " + shellCommandLine("ssh", args))
	if err := runner.Run(ctx, session, args...); err != nil {
		return fmt.Errorf("ssh to machine %s: %w", plan.InstanceName, err)
	}
	return nil
}

func sshArgs(plan machine.ConnectPlan, verbose bool) []string {
	args := []string{
		"-A",
		"-o", "CheckHostIP=no",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	if verbose {
		args = append(args, "-v")
	}
	if plan.HostKeyAlias != "" {
		args = append(args, "-o", "HostKeyAlias="+plan.HostKeyAlias)
	}
	if plan.Interactive {
		args = append(args, "-t")
	}
	args = append(args, plan.LinuxUser+"@"+plan.SSHHost)
	if len(plan.Command) > 0 {
		args = append(args, remoteCommand(plan))
	}
	return args
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
