package localdns

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	ServiceName          = "sandcastle-dns-forwarder"
	ServiceLabel         = "dev.sandcastle.dns-forwarder"
	ServiceStrategyMacOS = "launchd-user"
	ServiceStrategyLinux = "systemd-user"
	ServiceStrategyFile  = "file"
)

type ServicePlan struct {
	Action      string    `json:"action"`
	Platform    string    `json:"platform"`
	Strategy    string    `json:"strategy"`
	Label       string    `json:"label"`
	Executable  string    `json:"executable"`
	StatePath   string    `json:"statePath"`
	Listen      string    `json:"listen"`
	ServicePath string    `json:"servicePath"`
	Content     string    `json:"content"`
	Commands    []Command `json:"commands,omitempty"`
}

type ServiceResult struct {
	Action      string `json:"action"`
	Strategy    string `json:"strategy"`
	ServicePath string `json:"servicePath"`
}

type ServiceManager interface {
	InstallService(context.Context, ServicePlan) (ServiceResult, error)
	ReloadService(context.Context, ServicePlan) (ServiceResult, error)
	UninstallService(context.Context, ServicePlan) (ServiceResult, error)
}

type ServiceRunner interface {
	Run(context.Context, []string) error
}

type ExecServiceRunner struct{}

func (ExecServiceRunner) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("service command is empty")
	}
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

type FileServiceManager struct {
	Runner ServiceRunner
}

func PlanServiceInstall() (ServicePlan, error) {
	return planService("install")
}

func PlanServiceReload() (ServicePlan, error) {
	return planService("reload")
}

func PlanServiceUninstall() (ServicePlan, error) {
	return planService("uninstall")
}

func planService(action string) (ServicePlan, error) {
	platform := runtime.GOOS
	executable, err := serviceExecutable()
	if err != nil {
		return ServicePlan{}, err
	}
	listen := DefaultListen()
	plan := ServicePlan{
		Action:     action,
		Platform:   platform,
		Label:      ServiceLabel,
		Executable: executable,
		StatePath:  DefaultStatePath(),
		Listen:     listen,
	}
	switch platform {
	case "darwin":
		plan.Strategy = ServiceStrategyMacOS
		plan.ServicePath = filepath.Join(serviceDir(platform), ServiceLabel+".plist")
		plan.Content = launchdPlist(plan)
		plan.Commands = launchdCommands(action, plan)
	case "linux":
		plan.Strategy = ServiceStrategyLinux
		plan.ServicePath = filepath.Join(serviceDir(platform), ServiceName+".service")
		plan.Content = systemdUnit(plan)
		plan.Commands = systemdCommands(action, plan)
	default:
		plan.Strategy = ServiceStrategyFile
		plan.ServicePath = filepath.Join(serviceDir(platform), ServiceName+".service")
		plan.Content = fileOnlyService(plan)
	}
	return plan, nil
}

func DefaultListen() string {
	return "127.0.0.1:53541"
}

func (m FileServiceManager) InstallService(ctx context.Context, plan ServicePlan) (ServiceResult, error) {
	if err := writeServiceFile(plan); err != nil {
		return ServiceResult{}, err
	}
	if err := m.runCommands(ctx, plan.Commands); err != nil {
		return ServiceResult{}, err
	}
	return serviceResult(plan), nil
}

func (m FileServiceManager) ReloadService(ctx context.Context, plan ServicePlan) (ServiceResult, error) {
	if err := writeServiceFile(plan); err != nil {
		return ServiceResult{}, err
	}
	if err := m.runCommands(ctx, plan.Commands); err != nil {
		return ServiceResult{}, err
	}
	return serviceResult(plan), nil
}

func (m FileServiceManager) UninstallService(ctx context.Context, plan ServicePlan) (ServiceResult, error) {
	if err := m.runCommands(ctx, plan.Commands); err != nil {
		return ServiceResult{}, err
	}
	if err := os.Remove(plan.ServicePath); err != nil && !os.IsNotExist(err) {
		return ServiceResult{}, err
	}
	return serviceResult(plan), nil
}

func (m FileServiceManager) runCommands(ctx context.Context, commands []Command) error {
	runner := m.Runner
	if runner == nil {
		runner = ExecServiceRunner{}
	}
	for _, command := range commands {
		if err := runner.Run(ctx, command.Args); err != nil {
			return err
		}
	}
	return nil
}

func writeServiceFile(plan ServicePlan) error {
	if err := os.MkdirAll(filepath.Dir(plan.ServicePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(plan.ServicePath, []byte(plan.Content), 0o644)
}

func serviceResult(plan ServicePlan) ServiceResult {
	return ServiceResult{Action: plan.Action, Strategy: plan.Strategy, ServicePath: plan.ServicePath}
}

func serviceExecutable() (string, error) {
	if executable := strings.TrimSpace(os.Getenv("SANDCASTLE_BIN")); executable != "" {
		return executable, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return executable, nil
}

func serviceDir(platform string) string {
	if dir := strings.TrimSpace(os.Getenv("SANDCASTLE_LOCAL_DNS_SERVICE_DIR")); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	switch platform {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents")
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user")
	default:
		return filepath.Join(home, ".sandcastle", "services")
	}
}

func launchdCommands(action string, plan ServicePlan) []Command {
	target := "gui/" + strconv.Itoa(os.Getuid())
	switch action {
	case "install":
		return []Command{
			{Args: []string{"launchctl", "bootstrap", target, plan.ServicePath}},
			{Args: []string{"launchctl", "kickstart", "-k", target + "/" + plan.Label}},
		}
	case "reload":
		return []Command{
			{Args: []string{"launchctl", "bootout", target + "/" + plan.Label}},
			{Args: []string{"launchctl", "bootstrap", target, plan.ServicePath}},
			{Args: []string{"launchctl", "kickstart", "-k", target + "/" + plan.Label}},
		}
	case "uninstall":
		return []Command{{Args: []string{"launchctl", "bootout", target + "/" + plan.Label}}}
	default:
		return nil
	}
}

func systemdCommands(action string, plan ServicePlan) []Command {
	switch action {
	case "install":
		return []Command{
			{Args: []string{"systemctl", "--user", "daemon-reload"}},
			{Args: []string{"systemctl", "--user", "enable", "--now", filepath.Base(plan.ServicePath)}},
		}
	case "reload":
		return []Command{
			{Args: []string{"systemctl", "--user", "daemon-reload"}},
			{Args: []string{"systemctl", "--user", "restart", filepath.Base(plan.ServicePath)}},
		}
	case "uninstall":
		return []Command{
			{Args: []string{"systemctl", "--user", "disable", "--now", filepath.Base(plan.ServicePath)}},
			{Args: []string{"systemctl", "--user", "daemon-reload"}},
		}
	default:
		return nil
	}
}

func launchdPlist(plan ServicePlan) string {
	args := []string{plan.Executable, "dns", "forwarder", "--state", plan.StatePath, "--listen", plan.Listen}
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>`)
	builder.WriteString(xmlEscape(plan.Label))
	builder.WriteString(`</string>
  <key>ProgramArguments</key>
  <array>
`)
	for _, arg := range args {
		builder.WriteString("    <string>")
		builder.WriteString(xmlEscape(arg))
		builder.WriteString("</string>\n")
	}
	builder.WriteString(`  </array>
  <key>KeepAlive</key>
  <true/>
  <key>RunAtLoad</key>
  <true/>
</dict>
</plist>
`)
	return builder.String()
}

func systemdUnit(plan ServicePlan) string {
	args := []string{plan.Executable, "dns", "forwarder", "--state", plan.StatePath, "--listen", plan.Listen}
	return "[Unit]\n" +
		"Description=Sandcastle local DNS forwarder\n\n" +
		"[Service]\n" +
		"ExecStart=" + quoteCommand(args) + "\n" +
		"Restart=always\n\n" +
		"[Install]\n" +
		"WantedBy=default.target\n"
}

func fileOnlyService(plan ServicePlan) string {
	return strings.Join([]string{
		"# Sandcastle local DNS forwarder",
		quoteCommand([]string{plan.Executable, "dns", "forwarder", "--state", plan.StatePath, "--listen", plan.Listen}),
		"",
	}, "\n")
}

func quoteCommand(args []string) string {
	quoted := make([]string, len(args))
	for index, arg := range args {
		quoted[index] = strconv.Quote(arg)
	}
	return strings.Join(quoted, " ")
}

func xmlEscape(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	value = strings.ReplaceAll(value, "'", "&apos;")
	return value
}
