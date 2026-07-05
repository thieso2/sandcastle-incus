package cli

import (
	"strings"
	"testing"
)

func TestParseOSRelease(t *testing.T) {
	codename, debian := parseOSRelease("PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\nID=debian\nVERSION_CODENAME=trixie\n")
	if codename != "trixie" || !debian {
		t.Fatalf("got %q %v", codename, debian)
	}
	codename, debian = parseOSRelease("ID=ubuntu\nVERSION_CODENAME=noble\n")
	if codename != "noble" || !debian {
		t.Fatalf("got %q %v", codename, debian)
	}
	if _, debian := parseOSRelease("ID=fedora\nVERSION_CODENAME=rawhide\n"); debian {
		t.Fatal("fedora must not read as debian-like")
	}
	if _, debian := parseOSRelease("ID=linuxmint\nID_LIKE=\"ubuntu debian\"\n"); !debian {
		t.Fatal("ID_LIKE debian must count")
	}
}

func TestInstallIncusScript(t *testing.T) {
	script := installIncusScript("trixie", "sc", false)
	for _, want := range []string{
		"Suites: trixie",
		"pkgs.zabbly.com/incus/stable",
		"apt-get install -y -qq incus",
		"incus admin init --minimal",
		"usermod -aG incus-admin sc",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	script = installIncusScript("noble", "", true)
	if strings.Contains(script, "admin init") || strings.Contains(script, "usermod") {
		t.Fatalf("skip-init/no-user script has extras:\n%s", script)
	}
}
