package infra

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

type CaddyDataExportRequest struct {
	ArchivePath string `json:"archivePath"`
}

type CaddyDataExportPlan struct {
	Remote      string `json:"remote"`
	Project     string `json:"project"`
	Instance    string `json:"instance"`
	SourcePath  string `json:"sourcePath"`
	ArchivePath string `json:"archivePath"`
}

type CaddyDataExportResult struct {
	Project     string `json:"project"`
	Instance    string `json:"instance"`
	SourcePath  string `json:"sourcePath"`
	ArchivePath string `json:"archivePath"`
}

type CaddyDataExporter interface {
	ExportCaddyData(context.Context, CaddyDataExportPlan) (CaddyDataExportResult, error)
}

func PlanCaddyDataExport(admin config.Admin, request CaddyDataExportRequest) (CaddyDataExportPlan, error) {
	if err := admin.Validate(); err != nil {
		return CaddyDataExportPlan{}, err
	}
	path := strings.TrimSpace(request.ArchivePath)
	if path == "" {
		path = CaddyDataArchivePath(admin)
	}
	if path == "" {
		return CaddyDataExportPlan{}, fmt.Errorf("Caddy data archive path is required")
	}
	return CaddyDataExportPlan{
		Remote:      admin.Remote,
		Project:     admin.InfrastructureProject,
		Instance:    route.InfrastructureCaddyName,
		SourcePath:  CaddyDataDir,
		ArchivePath: path,
	}, nil
}

func EnsureCaddyDataArchiveParent(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o700)
}
