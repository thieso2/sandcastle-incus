package domain

import "testing"

func TestValidateProjectDomainNormalizesAllowedPrivateDomain(t *testing.T) {
	domain, err := ValidateProjectDomain("MyProject.Project-TLD.", Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if domain != "myproject.project-tld" {
		t.Fatalf("domain = %q", domain)
	}
}

func TestValidateProjectDomainRejectsMalformedDomain(t *testing.T) {
	for _, candidate := range []string{"bad domain", "bad/name", "bad..name", "-bad.name", "bad-.name", "bad_name"} {
		t.Run(candidate, func(t *testing.T) {
			if _, err := ValidateProjectDomain(candidate, Policy{}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestValidateProjectDomainRejectsDeniedFinalLabels(t *testing.T) {
	for _, candidate := range []string{"project.com", "project.local", "project.test", "project.alt", "project.home.arpa"} {
		t.Run(candidate, func(t *testing.T) {
			if _, err := ValidateProjectDomain(candidate, Policy{}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestValidateProjectDomainAllowsExplicitLabSuffixes(t *testing.T) {
	domain, err := ValidateProjectDomain("Project.Test", Policy{
		AllowedSuffixes: []string{"test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if domain != "project.test" {
		t.Fatalf("domain = %q", domain)
	}
}

func TestValidateProjectDomainRejectsAdminDeniedSuffixes(t *testing.T) {
	_, err := ValidateProjectDomain("project.corp.private", Policy{
		DeniedSuffixes: []string{"corp.private"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
