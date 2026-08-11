package cli

import (
	"strings"
	"testing"
)

func TestRemoteBuildTemplatesBase(t *testing.T) {
	templates, err := remoteBuildTemplates("base")
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 1 || templates[0] != "base" {
		t.Fatalf("templates = %#v", templates)
	}
}

func TestRemoteBuildTemplatesAI(t *testing.T) {
	templates, err := remoteBuildTemplates("ai")
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 1 || templates[0] != "ai" {
		t.Fatalf("templates = %#v", templates)
	}
}

func TestRemoteBuildTemplatesDev(t *testing.T) {
	templates, err := remoteBuildTemplates("dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 1 || templates[0] != "dev" {
		t.Fatalf("templates = %#v", templates)
	}
}

func TestRemoteBuildTemplatesAllIncludesDev(t *testing.T) {
	templates, err := remoteBuildTemplates("all")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"base", "ai", "dev"}
	if len(templates) != len(want) {
		t.Fatalf("templates = %#v, want %#v", templates, want)
	}
	for i, tpl := range want {
		if templates[i] != tpl {
			t.Fatalf("templates = %#v, want %#v", templates, want)
		}
	}
}

func TestRemoteBuildTemplatesRejectsUnknown(t *testing.T) {
	_, err := remoteBuildTemplates("bogus")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("error = %q", err)
	}
}
