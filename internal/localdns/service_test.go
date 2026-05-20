package localdns

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPlanServiceInstallUsesForwarderCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SANDCASTLE_BIN", "/usr/local/bin/sandcastle")
	t.Setenv("SANDCASTLE_LOCAL_DNS_STATE", filepath.Join(dir, "dns.yaml"))
	t.Setenv("SANDCASTLE_LOCAL_DNS_SERVICE_DIR", filepath.Join(dir, "services"))

	plan, err := PlanServiceInstall()
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action != "install" {
		t.Fatalf("Action = %q", plan.Action)
	}
	if plan.Executable != "/usr/local/bin/sandcastle" {
		t.Fatalf("Executable = %q", plan.Executable)
	}
	if !strings.Contains(plan.Content, "dns") || !strings.Contains(plan.Content, "forwarder") {
		t.Fatalf("service content missing forwarder command: %s", plan.Content)
	}
	if !strings.Contains(plan.Content, plan.StatePath) || !strings.Contains(plan.Content, plan.Listen) {
		t.Fatalf("service content missing state/listen: %s", plan.Content)
	}
	switch runtime.GOOS {
	case "darwin":
		if plan.Strategy != ServiceStrategyMacOS || !strings.HasSuffix(plan.ServicePath, ".plist") {
			t.Fatalf("strategy/path = %q %q", plan.Strategy, plan.ServicePath)
		}
	case "linux":
		if plan.Strategy != ServiceStrategyLinux || !strings.HasSuffix(plan.ServicePath, ".service") {
			t.Fatalf("strategy/path = %q %q", plan.Strategy, plan.ServicePath)
		}
	}
}

func TestFileServiceManagerInstallReloadAndUninstall(t *testing.T) {
	dir := t.TempDir()
	plan := ServicePlan{
		Action:      "install",
		Strategy:    ServiceStrategyLinux,
		Label:       ServiceLabel,
		ServicePath: filepath.Join(dir, "sandcastle-dns-forwarder.service"),
		Content:     "[Service]\nExecStart=/bin/sandcastle dns forwarder\n",
		Commands: []Command{
			{Args: []string{"systemctl", "--user", "daemon-reload"}},
			{Args: []string{"systemctl", "--user", "enable", "--now", "sandcastle-dns-forwarder.service"}},
		},
	}
	runner := &recordingServiceRunner{}
	manager := FileServiceManager{Runner: runner}
	result, err := manager.InstallService(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "install" {
		t.Fatalf("Action = %q", result.Action)
	}
	content, err := os.ReadFile(plan.ServicePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != plan.Content {
		t.Fatalf("service file = %q", content)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("commands = %#v", runner.commands)
	}

	plan.Action = "reload"
	plan.Content = "[Service]\nExecStart=/bin/sandcastle dns forwarder --state /tmp/dns.yaml\n"
	plan.Commands = []Command{{Args: []string{"systemctl", "--user", "restart", "sandcastle-dns-forwarder.service"}}}
	result, err = manager.ReloadService(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "reload" {
		t.Fatalf("Action = %q", result.Action)
	}
	content, err = os.ReadFile(plan.ServicePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != plan.Content {
		t.Fatalf("service file = %q", content)
	}

	plan.Action = "uninstall"
	plan.Commands = []Command{{Args: []string{"systemctl", "--user", "disable", "--now", "sandcastle-dns-forwarder.service"}}}
	result, err = manager.UninstallService(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "uninstall" {
		t.Fatalf("Action = %q", result.Action)
	}
	if _, err := os.Stat(plan.ServicePath); !os.IsNotExist(err) {
		t.Fatalf("expected service file removal, stat err = %v", err)
	}
}

type recordingServiceRunner struct {
	commands [][]string
}

func (r *recordingServiceRunner) Run(ctx context.Context, args []string) error {
	r.commands = append(r.commands, append([]string(nil), args...))
	return nil
}
