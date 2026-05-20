package naming

import "testing"

func TestParseProjectRef(t *testing.T) {
	ref, err := ParseProjectRef("alice/myproject")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Owner != "alice" || ref.Project != "myproject" {
		t.Fatalf("ref = %#v", ref)
	}
}

func TestParseProjectRefWithDefaultOwner(t *testing.T) {
	ref, err := ParseProjectRefWithDefaultOwner("myproject", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Owner != "alice" || ref.Project != "myproject" {
		t.Fatalf("ref = %#v", ref)
	}
}

func TestParseProjectRefWithDefaultOwnerPreservesExplicitOwner(t *testing.T) {
	ref, err := ParseProjectRefWithDefaultOwner("bob/myproject", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Owner != "bob" || ref.Project != "myproject" {
		t.Fatalf("ref = %#v", ref)
	}
}

func TestParseProjectRefWithDefaultOwnerRejectsMissingOwner(t *testing.T) {
	_, err := ParseProjectRefWithDefaultOwner("myproject", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseProjectRefRejectsMissingOwner(t *testing.T) {
	_, err := ParseProjectRef("/myproject")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseProjectRefRejectsUnsafeCase(t *testing.T) {
	_, err := ParseProjectRef("Alice/myproject")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIncusProjectName(t *testing.T) {
	ref := ProjectRef{Owner: "alice", Project: "myproject"}
	name, err := IncusProjectName(ref)
	if err != nil {
		t.Fatal(err)
	}
	if name != "sc-alice-myproject" {
		t.Fatalf("name = %q, want sc-alice-myproject", name)
	}
}

func TestIncusProjectNameRejectsInvalidPrefix(t *testing.T) {
	_, err := IncusProjectNameWithPrefix("bad_prefix", ProjectRef{Owner: "alice", Project: "myproject"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateProjectPrefix(t *testing.T) {
	if err := ValidateProjectPrefix("dev"); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"", "s", "Bad", "bad_prefix", "bad.prefix"} {
		t.Run(prefix, func(t *testing.T) {
			if err := ValidateProjectPrefix(prefix); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestReservedSandboxNames(t *testing.T) {
	for _, name := range []string{"ca", "dns", "tailscale", "sc-ca", "sc-dns", "sc-tailscale"} {
		if !IsReservedSandboxName(name) {
			t.Fatalf("%q should be reserved", name)
		}
	}
	if IsReservedSandboxName("codex") {
		t.Fatal("codex should not be reserved")
	}
}

func TestValidateSandboxName(t *testing.T) {
	if err := ValidateSandboxName("codex"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bad_name", "x", "sc-dns"} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateSandboxName(name); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
