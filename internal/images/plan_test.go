package images

import (
	"context"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
)

func TestPlanSyncBaseImage(t *testing.T) {
	plan, err := PlanSync(config.LoadAdminFromEnv(), SyncRequest{SourceRef: "sandcastle/base:debian-13"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Template != "base" {
		t.Fatalf("Template = %q", plan.Template)
	}
	if plan.Alias != config.DefaultBaseImageAlias {
		t.Fatalf("Alias = %q", plan.Alias)
	}
	if !strings.Contains(plan.Description, "debian-13") {
		t.Fatalf("Description = %q", plan.Description)
	}
}

func TestPlanSyncAIImage(t *testing.T) {
	plan, err := PlanSync(config.LoadAdminFromEnv(), SyncRequest{SourceRef: "sandcastle/ai:debian-13"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Template != "ai" {
		t.Fatalf("Template = %q", plan.Template)
	}
	if plan.Alias != config.DefaultAIImageAlias {
		t.Fatalf("Alias = %q", plan.Alias)
	}
}

func TestPlanSyncRejectsUnknownImage(t *testing.T) {
	_, err := PlanSync(config.LoadAdminFromEnv(), SyncRequest{SourceRef: "other/image:latest"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "base or AI") {
		t.Fatalf("error = %q", err)
	}
}

func TestPlanBuildBaseImage(t *testing.T) {
	plan, err := PlanBuild(config.LoadAdminFromEnv(), BuildRequest{Template: "base", Tag: "sandcastle/base:debian-13"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Template != "base" || plan.Tag != "sandcastle/base:debian-13" {
		t.Fatalf("plan = %#v", plan)
	}
	if strings.Join(plan.Command, " ") != "docker build -t sandcastle/base:debian-13 -f images/base/Dockerfile images/base" {
		t.Fatalf("Command = %#v", plan.Command)
	}
}

func TestPlanBuildAllowsDockerOrPodmanTools(t *testing.T) {
	for _, tool := range []string{"docker", "podman", "/usr/local/bin/podman"} {
		t.Run(tool, func(t *testing.T) {
			plan, err := PlanBuild(config.LoadAdminFromEnv(), BuildRequest{
				Template: "base",
				Tag:      "sandcastle/base:test",
				Tool:     tool,
			})
			if err != nil {
				t.Fatal(err)
			}
			if plan.Tool != tool {
				t.Fatalf("Tool = %q", plan.Tool)
			}
		})
	}
}

func TestPlanBuildRejectsUnsupportedTool(t *testing.T) {
	_, err := PlanBuild(config.LoadAdminFromEnv(), BuildRequest{
		Template: "base",
		Tag:      "sandcastle/base:test",
		Tool:     "sh",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error = %q", err)
	}
}

func TestPlanBuildAIImageRequiresPinnedToolVersions(t *testing.T) {
	_, err := PlanBuild(config.LoadAdminFromEnv(), BuildRequest{Template: "ai"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "codex-version") {
		t.Fatalf("error = %q", err)
	}
}

func TestPlanBuildAIImage(t *testing.T) {
	plan, err := PlanBuild(config.LoadAdminFromEnv(), BuildRequest{
		Template:      "ai",
		Tag:           "sandcastle/ai:debian-13",
		Tool:          "podman",
		CodexVersion:  "1.2.3",
		ClaudeVersion: "2.3.4",
		GeminiVersion: "3.4.5",
	})
	if err != nil {
		t.Fatal(err)
	}
	command := strings.Join(plan.Command, " ")
	for _, want := range []string{
		"podman build",
		"-t sandcastle/ai:debian-13",
		"--build-arg SANDCASTLE_BASE_IMAGE=sandcastle/base:latest",
		"--build-arg CODEX_CLI_VERSION=1.2.3",
		"images/ai",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("Command = %q, want %q", command, want)
		}
	}
}

func TestLocalBuilderRunsPlannedCommand(t *testing.T) {
	runner := &fakeCommandRunner{}
	result, err := (LocalBuilder{Runner: runner}).BuildImage(context.Background(), BuildPlan{
		Template: "base",
		Tag:      "sandcastle/base:test",
		Command:  []string{"docker", "build", "-t", "sandcastle/base:test", "."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Built {
		t.Fatal("expected built result")
	}
	if runner.name != "docker" || strings.Join(runner.args, " ") != "build -t sandcastle/base:test ." {
		t.Fatalf("runner = %#v", runner)
	}
}

func TestPlanImportBaseImage(t *testing.T) {
	plan, err := PlanImport(config.LoadAdminFromEnv(), ImportRequest{
		Template:  "base",
		SourceRef: "oci:sandcastle/base:debian-13",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Alias != config.DefaultBaseImageAlias {
		t.Fatalf("Alias = %q", plan.Alias)
	}
	if strings.Join(plan.Command, " ") != "incus image copy oci:sandcastle/base:debian-13 local: --alias sandcastle/base:latest --copy-aliases --reuse" {
		t.Fatalf("Command = %#v", plan.Command)
	}
}

func TestPlanImportAllowsIncusExecutablePath(t *testing.T) {
	plan, err := PlanImport(config.LoadAdminFromEnv(), ImportRequest{
		Template:  "base",
		SourceRef: "oci:sandcastle/base:debian-13",
		Tool:      "/usr/local/bin/incus",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Command[0] != "/usr/local/bin/incus" {
		t.Fatalf("Command = %#v", plan.Command)
	}
}

func TestPlanImportRejectsUnsupportedTool(t *testing.T) {
	_, err := PlanImport(config.LoadAdminFromEnv(), ImportRequest{
		Template:  "base",
		SourceRef: "oci:sandcastle/base:debian-13",
		Tool:      "docker",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error = %q", err)
	}
}

func TestPlanImportAIImage(t *testing.T) {
	plan, err := PlanImport(config.LoadAdminFromEnv(), ImportRequest{
		Template:  "ai",
		SourceRef: "oci:sandcastle/ai:debian-13",
		Tool:      "incus",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Alias != config.DefaultAIImageAlias {
		t.Fatalf("Alias = %q", plan.Alias)
	}
}

func TestPlanImportRejectsUnknownTemplate(t *testing.T) {
	_, err := PlanImport(config.LoadAdminFromEnv(), ImportRequest{
		Template:  "unknown",
		SourceRef: "oci:sandcastle/base:debian-13",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLocalImporterRunsPlannedCommand(t *testing.T) {
	runner := &fakeCommandRunner{}
	result, err := (LocalImporter{Runner: runner}).ImportImage(context.Background(), ImportPlan{
		Template:  "base",
		SourceRef: "oci:sandcastle/base:debian-13",
		Remote:    "local",
		Alias:     "sandcastle/base:latest",
		Tool:      "incus",
		Command:   []string{"incus", "image", "copy", "oci:sandcastle/base:debian-13", "local:", "--alias", "sandcastle/base:latest", "--copy-aliases", "--reuse"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Imported {
		t.Fatal("expected imported result")
	}
	if runner.name != "incus" || strings.Join(runner.args, " ") != "image copy oci:sandcastle/base:debian-13 local: --alias sandcastle/base:latest --copy-aliases --reuse" {
		t.Fatalf("runner = %#v", runner)
	}
}

type fakeCommandRunner struct {
	name string
	args []string
}

func (f *fakeCommandRunner) Run(ctx context.Context, name string, args ...string) error {
	f.name = name
	f.args = args
	return nil
}
