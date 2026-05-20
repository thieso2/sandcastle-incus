package images

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
)

type SyncRequest struct {
	SourceRef string
}

type SyncPlan struct {
	SourceRef   string `json:"sourceRef"`
	Alias       string `json:"alias"`
	Template    string `json:"template"`
	Description string `json:"description"`
}

type SyncResult struct {
	SyncPlan
	Fingerprint string `json:"fingerprint"`
	Action      string `json:"action"`
}

type Manager interface {
	SyncImage(context.Context, SyncPlan) (SyncResult, error)
}

type BuildRequest struct {
	Template      string
	Tag           string
	Tool          string
	CodexVersion  string
	ClaudeVersion string
	GeminiVersion string
}

type BuildPlan struct {
	Template      string   `json:"template"`
	Tag           string   `json:"tag"`
	ContextDir    string   `json:"contextDir"`
	Dockerfile    string   `json:"dockerfile"`
	Tool          string   `json:"tool"`
	BuildArgs     []string `json:"buildArgs,omitempty"`
	Command       []string `json:"command"`
	CodexVersion  string   `json:"codexVersion,omitempty"`
	ClaudeVersion string   `json:"claudeVersion,omitempty"`
	GeminiVersion string   `json:"geminiVersion,omitempty"`
}

type BuildResult struct {
	BuildPlan
	Built bool `json:"built"`
}

type Builder interface {
	BuildImage(context.Context, BuildPlan) (BuildResult, error)
}

type ImportRequest struct {
	Template  string
	SourceRef string
	Tool      string
}

type ImportPlan struct {
	Template  string   `json:"template"`
	SourceRef string   `json:"sourceRef"`
	Remote    string   `json:"remote"`
	Alias     string   `json:"alias"`
	Tool      string   `json:"tool"`
	Command   []string `json:"command"`
}

type ImportResult struct {
	ImportPlan
	Imported bool `json:"imported"`
}

type Importer interface {
	ImportImage(context.Context, ImportPlan) (ImportResult, error)
}

func PlanSync(admin config.Admin, request SyncRequest) (SyncPlan, error) {
	if err := admin.Validate(); err != nil {
		return SyncPlan{}, err
	}
	source := strings.TrimSpace(request.SourceRef)
	if source == "" {
		return SyncPlan{}, fmt.Errorf("image source reference is required")
	}
	template, alias, err := templateAlias(admin, source)
	if err != nil {
		return SyncPlan{}, err
	}
	return SyncPlan{
		SourceRef:   source,
		Alias:       alias,
		Template:    template,
		Description: "Sandcastle " + template + " image synced from " + source,
	}, nil
}

func PlanBuild(admin config.Admin, request BuildRequest) (BuildPlan, error) {
	if err := admin.Validate(); err != nil {
		return BuildPlan{}, err
	}
	template := strings.ToLower(strings.TrimSpace(request.Template))
	if template == "" {
		return BuildPlan{}, fmt.Errorf("image template is required")
	}
	tool := strings.TrimSpace(request.Tool)
	if tool == "" {
		tool = "docker"
	}
	plan := BuildPlan{
		Template:   template,
		Tool:       tool,
		ContextDir: filepath.Join("images", template),
		Dockerfile: filepath.Join("images", template, "Dockerfile"),
	}
	switch template {
	case "base":
		plan.Tag = firstNonEmpty(request.Tag, admin.Images.Base)
	case "ai":
		plan.Tag = firstNonEmpty(request.Tag, admin.Images.AI)
		plan.CodexVersion = strings.TrimSpace(request.CodexVersion)
		plan.ClaudeVersion = strings.TrimSpace(request.ClaudeVersion)
		plan.GeminiVersion = strings.TrimSpace(request.GeminiVersion)
		if plan.CodexVersion == "" || plan.ClaudeVersion == "" || plan.GeminiVersion == "" {
			return BuildPlan{}, fmt.Errorf("AI image build requires --codex-version, --claude-version, and --gemini-version")
		}
		plan.BuildArgs = []string{
			"SANDCASTLE_BASE_IMAGE=" + admin.Images.Base,
			"CODEX_CLI_VERSION=" + plan.CodexVersion,
			"CLAUDE_CODE_VERSION=" + plan.ClaudeVersion,
			"GEMINI_CLI_VERSION=" + plan.GeminiVersion,
		}
	default:
		return BuildPlan{}, fmt.Errorf("unknown image template %q", request.Template)
	}
	if strings.TrimSpace(plan.Tag) == "" {
		return BuildPlan{}, fmt.Errorf("image tag is required")
	}
	plan.Command = buildCommand(plan)
	return plan, nil
}

func PlanImport(admin config.Admin, request ImportRequest) (ImportPlan, error) {
	if err := admin.Validate(); err != nil {
		return ImportPlan{}, err
	}
	template := strings.ToLower(strings.TrimSpace(request.Template))
	if template == "" {
		return ImportPlan{}, fmt.Errorf("image template is required")
	}
	source := strings.TrimSpace(request.SourceRef)
	if source == "" {
		return ImportPlan{}, fmt.Errorf("image source reference is required")
	}
	tool := strings.TrimSpace(request.Tool)
	if tool == "" {
		tool = "incus"
	}
	alias, err := aliasForTemplate(admin, template)
	if err != nil {
		return ImportPlan{}, err
	}
	plan := ImportPlan{
		Template:  template,
		SourceRef: source,
		Remote:    strings.TrimSpace(admin.Remote),
		Alias:     alias,
		Tool:      tool,
	}
	plan.Command = importCommand(plan)
	return plan, nil
}

type LocalBuilder struct {
	Runner CommandRunner
}

type LocalImporter struct {
	Runner CommandRunner
}

type CommandRunner interface {
	Run(context.Context, string, ...string) error
}

type ExecRunner struct{}

func (b LocalBuilder) BuildImage(ctx context.Context, plan BuildPlan) (BuildResult, error) {
	var runner CommandRunner = ExecRunner{}
	if b.Runner != nil {
		runner = b.Runner
	}
	if len(plan.Command) == 0 {
		return BuildResult{}, fmt.Errorf("image build command is required")
	}
	if err := runner.Run(ctx, plan.Command[0], plan.Command[1:]...); err != nil {
		return BuildResult{}, err
	}
	return BuildResult{BuildPlan: plan, Built: true}, nil
}

func (i LocalImporter) ImportImage(ctx context.Context, plan ImportPlan) (ImportResult, error) {
	var runner CommandRunner = ExecRunner{}
	if i.Runner != nil {
		runner = i.Runner
	}
	if len(plan.Command) == 0 {
		return ImportResult{}, fmt.Errorf("image import command is required")
	}
	if err := runner.Run(ctx, plan.Command[0], plan.Command[1:]...); err != nil {
		return ImportResult{}, err
	}
	return ImportResult{ImportPlan: plan, Imported: true}, nil
}

func (r ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func buildCommand(plan BuildPlan) []string {
	args := []string{plan.Tool, "build", "-t", plan.Tag, "-f", plan.Dockerfile}
	for _, buildArg := range plan.BuildArgs {
		args = append(args, "--build-arg", buildArg)
	}
	args = append(args, plan.ContextDir)
	return args
}

func importCommand(plan ImportPlan) []string {
	return []string{
		plan.Tool,
		"image",
		"copy",
		plan.SourceRef,
		plan.Remote + ":",
		"--alias",
		plan.Alias,
		"--reuse",
	}
}

func aliasForTemplate(admin config.Admin, template string) (string, error) {
	switch template {
	case "base":
		return strings.TrimSpace(admin.Images.Base), nil
	case "ai":
		return strings.TrimSpace(admin.Images.AI), nil
	default:
		return "", fmt.Errorf("unknown image template %q", template)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func templateAlias(admin config.Admin, source string) (string, string, error) {
	baseStem := aliasStem(admin.Images.Base)
	aiStem := aliasStem(admin.Images.AI)
	sourceStem := aliasStem(source)
	switch sourceStem {
	case baseStem:
		return "base", strings.TrimSpace(admin.Images.Base), nil
	case aiStem:
		return "ai", strings.TrimSpace(admin.Images.AI), nil
	default:
		return "", "", fmt.Errorf("image source %q does not match configured Sandcastle base or AI image names", source)
	}
}

func aliasStem(value string) string {
	value = strings.TrimSpace(value)
	if before, _, ok := strings.Cut(value, ":"); ok {
		return before
	}
	return value
}
