package dns

import (
	"strings"
	"testing"
)

func TestRenderInitial(t *testing.T) {
	files, err := RenderInitial("MyProject.Project-TLD.", "10.248.0.53")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(files))
	}
	if files[0].Path != "/etc/coredns/Corefile" {
		t.Fatalf("Corefile path = %q", files[0].Path)
	}
	if !strings.Contains(files[0].Content, "myproject.project-tld:53") {
		t.Fatalf("Corefile content = %q", files[0].Content)
	}
	if files[1].Path != "/etc/coredns/zones/db.myproject.project-tld" {
		t.Fatalf("zone path = %q", files[1].Path)
	}
	if !strings.Contains(files[1].Content, "ns IN A 10.248.0.53") {
		t.Fatalf("zone content = %q", files[1].Content)
	}
	if strings.Contains(files[1].Content, "*") {
		t.Fatalf("initial zone should not contain wildcards: %q", files[1].Content)
	}
}

func TestRenderInitialRequiresDomain(t *testing.T) {
	_, err := RenderInitial("", "10.248.0.53")
	if err == nil {
		t.Fatal("expected error")
	}
}
