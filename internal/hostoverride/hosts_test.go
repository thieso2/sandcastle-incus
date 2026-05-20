package hostoverride

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestApplyHostsEntryAddsManagedBlock(t *testing.T) {
	entry := RenderHostsEntry("alice/myproject/codex", "example.com", "10.248.0.20")
	updated := ApplyHostsEntry("127.0.0.1 localhost\n", entry)
	if !strings.Contains(updated, entry.BeginLine+"\n"+entry.Line+"\n"+entry.EndLine) {
		t.Fatalf("updated hosts missing entry: %q", updated)
	}
}

func TestApplyHostsEntryReplacesExistingManagedBlock(t *testing.T) {
	entry := RenderHostsEntry("alice/myproject/codex", "example.com", "10.248.0.20")
	existing := "127.0.0.1 localhost\n" + entry.BeginLine + "\n10.0.0.1 example.com\n" + entry.EndLine + "\n"
	updated := ApplyHostsEntry(existing, entry)
	if strings.Contains(updated, "10.0.0.1 example.com") {
		t.Fatalf("stale entry was not replaced: %q", updated)
	}
	if strings.Count(updated, entry.BeginLine) != 1 {
		t.Fatalf("managed block duplicated: %q", updated)
	}
}

func TestRemoveHostsEntryRemovesManagedBlock(t *testing.T) {
	entry := RenderHostsEntry("alice/myproject/codex", "example.com", "10.248.0.20")
	existing := "127.0.0.1 localhost\n" + entry.BeginLine + "\n" + entry.Line + "\n" + entry.EndLine + "\n"
	updated := RemoveHostsEntry(existing, entry)
	if strings.Contains(updated, "example.com") {
		t.Fatalf("managed entry was not removed: %q", updated)
	}
	if !strings.Contains(updated, "127.0.0.1 localhost") {
		t.Fatalf("unmanaged entry missing: %q", updated)
	}
}

func TestFileHostsManagerAddHostsEntry(t *testing.T) {
	path := t.TempDir() + "/hosts"
	if err := os.WriteFile(path, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := AddPlan{HostsEntry: RenderHostsEntry("alice/myproject/codex", "example.com", "10.248.0.20")}
	if err := (FileHostsManager{Path: path}).AddHostsEntry(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "10.248.0.20 example.com") {
		t.Fatalf("hosts content = %q", content)
	}
}

func TestFileHostsManagerRemoveHostsEntry(t *testing.T) {
	path := t.TempDir() + "/hosts"
	entry := RenderHostsEntry("alice/myproject/codex", "example.com", "10.248.0.20")
	if err := os.WriteFile(path, []byte(ApplyHostsEntry("127.0.0.1 localhost\n", entry)), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := RemovePlan{HostsEntry: entry}
	if err := (FileHostsManager{Path: path}).RemoveHostsEntry(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "example.com") {
		t.Fatalf("hosts content = %q", content)
	}
}
