package tenant

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

func TestEveryUserDataVariantBakesTheScratchUnit(t *testing.T) {
	for name, data := range map[string]string{
		"caddy":     V2DefaultProfileUserData("dev", "ssh-ed25519 AAAA", "default", "acme", "http://10.0.0.3:9443"),
		"no-signer": V2DefaultProfileUserData("dev", "ssh-ed25519 AAAA", "", "", ""),
		"dev-image": V2DevUserData("dev", "ssh-ed25519 AAAA", "default.acme"),
		"bare":      V2BareUserData("default.acme", "http://10.0.0.3:9443"),
	} {
		rendered := strings.ReplaceAll(data, "{{ v1.local_hostname }}", "machine")
		var doc struct {
			WriteFiles []struct {
				Path    string `yaml:"path"`
				Content string `yaml:"content"`
			} `yaml:"write_files"`
			Runcmd []any `yaml:"runcmd"`
		}
		if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
			t.Fatalf("%s: user-data is not valid YAML: %v\n%s", name, err, data)
		}
		unit := ""
		for _, f := range doc.WriteFiles {
			if f.Path == "/etc/systemd/system/sc-scratch.service" {
				unit = f.Content
			}
		}
		if unit != scScratchUnit {
			t.Fatalf("%s: write_files must carry the sc-scratch unit verbatim, got %q", name, unit)
		}
		enabled := false
		for _, cmd := range doc.Runcmd {
			if fmt.Sprint(cmd) == "[systemctl enable --now sc-scratch.service]" {
				enabled = true
			}
		}
		if !enabled {
			t.Fatalf("%s: runcmd does not enable the unit: %v", name, doc.Runcmd)
		}
	}
}

func TestScratchUnitRunsThePayloadScriptOnlyWhenPresent(t *testing.T) {
	for _, want := range []string{
		"ConditionPathExists=/.sc/platform/scratch/setup.sh",
		"ExecStart=/bin/sh /.sc/platform/scratch/setup.sh",
		"After=local-fs.target incus-agent.service",
		"Before=systemd-user-sessions.service multi-user.target",
		"Type=oneshot",
		"RemainAfterExit=yes",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(scScratchUnit, want) {
			t.Fatalf("unit lacks %q:\n%s", want, scScratchUnit)
		}
	}
}

func TestScratchFixScriptsParse(t *testing.T) {
	for name, script := range map[string]string{"setup": scratchSetupScript, "backfill": ScratchBackfillScript(), "check": ScratchCheckScript(), "rc": scratchCacheSnippet} {
		if out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Fatalf("%s script does not parse: %v\n%s", name, err, out)
		}
	}
}

// scratchSetupHarness runs setup.sh against temp dirs with stub mountpoint,
// mount, systemd-detect-virt and systemd-tmpfiles commands on PATH.
type scratchSetupHarness struct {
	root, tmp, varTmp, agentMounts, log string
	env                                 []string
}

func newScratchSetupHarness(t *testing.T, scratchMounted bool, container bool, mountExit int) scratchSetupHarness {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("setup.sh uses GNU stat")
	}
	dir := t.TempDir()
	h := scratchSetupHarness{
		root:        filepath.Join(dir, "scratch"),
		tmp:         filepath.Join(dir, "tmp"),
		varTmp:      filepath.Join(dir, "var", "tmp"),
		agentMounts: filepath.Join(dir, "agent-mounts.json"),
		log:         filepath.Join(dir, "calls.log"),
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	mounted := "1"
	if scratchMounted {
		mounted = `[ "$2" = "` + h.root + `" ] && exit 0; exit 1`
	}
	virt := "exit 1"
	if container {
		virt = "exit 0"
	}
	stubs := map[string]string{
		"mountpoint":          mounted,
		"systemd-detect-virt": virt,
		"mount":               `echo "mount $*" >> "` + h.log + `"; exit ` + fmt.Sprint(mountExit),
		"systemd-tmpfiles":    `echo "tmpfiles $*" >> "` + h.log + `"`,
		"sleep":               `echo sleep >> "` + h.log + `"`,
	}
	for name, body := range stubs {
		if body == "1" {
			body = "exit 1"
		}
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if scratchMounted {
		if err := os.MkdirAll(h.root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"SC_SCRATCH_ROOT="+h.root,
		"SC_SCRATCH_TMP="+h.tmp,
		"SC_SCRATCH_VAR_TMP="+h.varTmp,
		"SC_SCRATCH_AGENT_MOUNTS="+h.agentMounts,
	)
	return h
}

func (h scratchSetupHarness) run(t *testing.T) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", "-c", scratchSetupScript)
	cmd.Env = h.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (h scratchSetupHarness) calls(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(h.log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func TestScratchSetupBindsTmpAndVarTmp(t *testing.T) {
	h := newScratchSetupHarness(t, true, true, 0)
	if out, err := h.run(t); err != nil {
		t.Fatalf("setup.sh: %v\n%s", err, out)
	}
	for _, sub := range []string{"tmp", "var-tmp", "cache"} {
		info, err := os.Stat(filepath.Join(h.root, sub))
		if err != nil {
			t.Fatalf("%s not created: %v", sub, err)
		}
		if info.Mode().Perm() != 0o777 || info.Mode()&os.ModeSticky == 0 {
			t.Fatalf("%s mode = %v, want sticky 0777 (1777)", sub, info.Mode())
		}
	}
	calls := h.calls(t)
	for _, want := range []string{
		"mount --bind " + filepath.Join(h.root, "tmp") + " " + h.tmp,
		"mount --bind " + filepath.Join(h.root, "var-tmp") + " " + h.varTmp,
		"tmpfiles --create --prefix=/tmp --prefix=/var/tmp",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("missing %q in calls:\n%s", want, calls)
		}
	}
}

func TestScratchSetupIsIdempotentOnceBound(t *testing.T) {
	h := newScratchSetupHarness(t, true, true, 0)
	if err := os.MkdirAll(filepath.Join(h.root, "tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.root, "var-tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(h.varTmp), 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlink resolves to the same device and inode, which is how an
	// existing bind mount looks to the script.
	if err := os.Symlink(filepath.Join(h.root, "tmp"), h.tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(h.root, "var-tmp"), h.varTmp); err != nil {
		t.Fatal(err)
	}
	if out, err := h.run(t); err != nil {
		t.Fatalf("setup.sh: %v\n%s", err, out)
	}
	if calls := h.calls(t); calls != "" {
		t.Fatalf("an already bound machine must not be mounted again, got:\n%s", calls)
	}
}

func TestScratchSetupIsANoOpWithoutTheVolume(t *testing.T) {
	for name, h := range map[string]scratchSetupHarness{
		"container": newScratchSetupHarness(t, false, true, 0),
		"vm":        newScratchSetupHarness(t, false, false, 0),
	} {
		if name == "vm" {
			if err := os.WriteFile(h.agentMounts, []byte(`[{"source":"sc-platform","target":"/.sc/platform","fstype":"virtiofs"}]`), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if out, err := h.run(t); err != nil {
			t.Fatalf("%s: setup.sh must exit 0 without a volume: %v\n%s", name, err, out)
		}
		if calls := h.calls(t); calls != "" {
			t.Fatalf("%s: no mount and no wait expected, got:\n%s", name, calls)
		}
		if _, err := os.Stat(h.root); !os.IsNotExist(err) {
			t.Fatalf("%s: setup.sh created %s on a machine without the volume", name, h.root)
		}
	}
}

func TestScratchSetupFailsWhenTheBindFails(t *testing.T) {
	h := newScratchSetupHarness(t, true, true, 32)
	if out, err := h.run(t); err == nil {
		t.Fatalf("a failed bind mount must fail the unit, got success:\n%s", out)
	}
}

func TestScratchCacheSnippetPointsXDGAtScratch(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	if err := os.Mkdir(cache, 0o1777); err != nil {
		t.Fatal(err)
	}
	snippet := strings.ReplaceAll(scratchCacheSnippet, "/scratch/cache", cache)
	run := func(preset string) string {
		t.Helper()
		script := "unset XDG_CACHE_HOME; " + preset + "\n" + snippet + "\nprintf '%s' \"$XDG_CACHE_HOME\""
		cmd := exec.Command("sh", "-c", script)
		cmd.Env = append(os.Environ(), "USER=alice")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("snippet: %v", err)
		}
		return string(out)
	}
	if got := run(""); got != filepath.Join(cache, "alice") {
		t.Fatalf("XDG_CACHE_HOME = %q, want %s", got, filepath.Join(cache, "alice"))
	}
	info, err := os.Stat(filepath.Join(cache, "alice"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("per-user cache dir: %v %v, want mode 0700", info, err)
	}
	if got := run("XDG_CACHE_HOME=/elsewhere; export XDG_CACHE_HOME"); got != "/elsewhere" {
		t.Fatalf("a preset XDG_CACHE_HOME was replaced with %q", got)
	}
	missing := strings.ReplaceAll(scratchCacheSnippet, "/scratch/cache", filepath.Join(dir, "absent"))
	out, err := exec.Command("sh", "-c", "unset XDG_CACHE_HOME\n"+missing+"\nprintf '%s' \"${XDG_CACHE_HOME:-unset}\"").Output()
	if err != nil || string(out) != "unset" {
		t.Fatalf("without /scratch/cache XDG_CACHE_HOME must stay unset, got %q %v", out, err)
	}
}
