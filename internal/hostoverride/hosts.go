package hostoverride

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const DefaultHostsPath = "/etc/hosts"

type HostsManager interface {
	AddHostsEntry(context.Context, AddPlan) error
	RemoveHostsEntry(context.Context, DeletePlan) error
}

type FileHostsManager struct {
	Path string
}

func NewFileHostsManager(path string) FileHostsManager {
	if strings.TrimSpace(path) == "" {
		path = DefaultHostsPath
	}
	return FileHostsManager{Path: path}
}

func (m FileHostsManager) AddHostsEntry(ctx context.Context, plan AddPlan) error {
	content, err := os.ReadFile(m.Path)
	if err != nil {
		return fmt.Errorf("read hosts file %s: %w", m.Path, err)
	}
	updated := ApplyHostsEntry(string(content), plan.HostsEntry)
	if err := os.WriteFile(m.Path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write hosts file %s: %w", m.Path, err)
	}
	return nil
}

func (m FileHostsManager) RemoveHostsEntry(ctx context.Context, plan DeletePlan) error {
	content, err := os.ReadFile(m.Path)
	if err != nil {
		return fmt.Errorf("read hosts file %s: %w", m.Path, err)
	}
	updated := RemoveHostsEntry(string(content), plan.HostsEntry)
	if err := os.WriteFile(m.Path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write hosts file %s: %w", m.Path, err)
	}
	return nil
}

func ApplyHostsEntry(content string, entry HostsEntry) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	output := make([]string, 0, len(lines)+3)
	skip := false
	replaced := false
	for _, line := range lines {
		switch {
		case line == entry.BeginLine:
			skip = true
			replaced = true
			output = append(output, entry.BeginLine, entry.Line, entry.EndLine)
		case line == entry.EndLine && skip:
			skip = false
		case skip:
			continue
		default:
			output = append(output, line)
		}
	}
	updated := strings.Join(trimTrailingEmptyLines(output), "\n")
	if !replaced {
		if strings.TrimSpace(updated) != "" {
			updated += "\n"
		}
		updated += entry.BeginLine + "\n" + entry.Line + "\n" + entry.EndLine
	}
	return updated + "\n"
}

func RemoveHostsEntry(content string, entry HostsEntry) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	output := make([]string, 0, len(lines))
	skip := false
	for _, line := range lines {
		switch {
		case line == entry.BeginLine:
			skip = true
		case line == entry.EndLine && skip:
			skip = false
		case skip:
			continue
		default:
			output = append(output, line)
		}
	}
	return strings.Join(trimTrailingEmptyLines(output), "\n") + "\n"
}

func trimTrailingEmptyLines(lines []string) []string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
