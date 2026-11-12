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

func TestEveryUserDataVariantBakesTheScratchFilesAndRunsThemLast(t *testing.T) {
	want := map[string][2]string{
		"/usr/local/sbin/sc-scratch-setup":       {"0755", scScratchSetupScript},
		"/etc/systemd/system/sc-scratch.service": {"0644", scScratchUnit},
		"/etc/profile.d/sc-scratch.sh":           {"0644", scratchCacheSnippet},
	}
	for name, data := range map[string]string{
		"caddy":     V2DefaultProfileUserData("dev", "ssh-ed25519 AAAA", "default", "acme", "http://10.0.0.3:9443"),
		"no-signer": V2DefaultProfileUserData("dev", "ssh-ed25519 AAAA", "", "", ""),
		"dev-image": V2DevUserData("dev", "ssh-ed25519 AAAA", "default.acme"),
		"bare":      V2BareUserData("default.acme", "http://10.0.0.3:9443"),
	} {
		rendered := strings.ReplaceAll(data, "{{ v1.local_hostname }}", "machine")
		var doc struct {
			WriteFiles []struct {
				Path        string `yaml:"path"`
				Permissions string `yaml:"permissions"`
				Content     string `yaml:"content"`
			} `yaml:"write_files"`
			Runcmd []any `yaml:"runcmd"`
		}
		if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
			t.Fatalf("%s: user-data is not valid YAML: %v\n%s", name, err, data)
		}
		got := map[string][2]string{}
		for _, f := range doc.WriteFiles {
			if _, ok := want[f.Path]; ok {
				got[f.Path] = [2]string{f.Permissions, f.Content}
			}
		}
		for path, file := range want {
			if got[path] != file {
				t.Fatalf("%s: write_files %s = %q, want %q", name, path, got[path], file)
			}
		}
		n := len(doc.Runcmd)
		if n < 2 || fmt.Sprint(doc.Runcmd[n-2]) != "[systemctl enable sc-scratch.service]" ||
			fmt.Sprint(doc.Runcmd[n-1]) != "[/usr/local/sbin/sc-scratch-setup --restart-private-tmp]" {
			t.Fatalf("%s: runcmd must end by enabling the unit and running the setup with --restart-private-tmp: %v", name, doc.Runcmd)
		}
	}
}

func TestScratchUnitRunsBeforeSysinit(t *testing.T) {
	for _, want := range []string{
		"DefaultDependencies=no",
		"Before=sysinit.target systemd-tmpfiles-setup.service shutdown.target",
		"ConditionPathExists=/usr/local/sbin/sc-scratch-setup",
		"ExecStart=/usr/local/sbin/sc-scratch-setup\n",
		"WantedBy=sysinit.target",
	} {
		if !strings.Contains(scScratchUnit, want) {
			t.Fatalf("unit lacks %q:\n%s", want, scScratchUnit)
		}
	}
}

func TestScratchScriptsParse(t *testing.T) {
	for name, script := range map[string]string{"setup": scScratchSetupScript, "backfill": ScratchBackfillScript(), "check": ScratchCheckScript(), "snippet": scratchCacheSnippet} {
		if out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Fatalf("%s script does not parse: %v\n%s", name, err, out)
		}
	}
}

// scratchStubs writes stub commands into a fresh bin dir and returns a PATH
// with it first. Each stub body may append to log.
func scratchStubs(t *testing.T, dir string, stubs map[string]string) string {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return "PATH=" + bin + ":" + os.Getenv("PATH")
}

// scratchSetupHarness runs sc-scratch-setup against temp dirs with stub
// mountpoint, mount, systemd-detect-virt and systemctl commands on PATH.
type scratchSetupHarness struct {
	root, tmp, varTmp, log string
	env                    []string
}

type scratchMachine struct {
	vm             bool
	scratchMounted bool
	// virtiofsOK: the VM has an incus_scratch share to mount.
	virtiofsOK bool
	bindExit   int
}

func newScratchSetupHarness(t *testing.T, m scratchMachine) scratchSetupHarness {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("sc-scratch-setup uses GNU stat")
	}
	dir := t.TempDir()
	h := scratchSetupHarness{
		root:   filepath.Join(dir, "scratch"),
		tmp:    filepath.Join(dir, "tmp"),
		varTmp: filepath.Join(dir, "var-tmp"),
		log:    filepath.Join(dir, "calls.log"),
	}
	for _, d := range []string{h.tmp, h.varTmp} {
		if err := os.MkdirAll(d, 0o1777); err != nil {
			t.Fatal(err)
		}
	}
	mountpoint := "exit 1"
	if m.scratchMounted {
		mountpoint = "exit 0"
		if err := os.MkdirAll(h.root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	virt := "exit 1"
	if m.vm {
		virt = "exit 0"
	}
	virtiofsExit := 32
	if m.virtiofsOK {
		virtiofsExit = 0
	}
	path := scratchStubs(t, dir, map[string]string{
		"mountpoint":          mountpoint,
		"systemd-detect-virt": virt,
		"mount": fmt.Sprintf(`echo "mount $*" >> %q
[ "$1" = -t ] && exit %d
exit %d`, h.log, virtiofsExit, m.bindExit),
		"systemctl": fmt.Sprintf(`echo "systemctl $*" >> %q
case "$1" in
list-units) printf 'a.service loaded active running A\nb.service loaded active running B\n' ;;
show) [ "$5" = a.service ] && echo yes || echo no ;;
esac`, h.log),
	})
	h.env = append(os.Environ(), path,
		"SC_SCRATCH_ROOT="+h.root,
		"SC_SCRATCH_TMP="+h.tmp,
		"SC_SCRATCH_VAR_TMP="+h.varTmp,
	)
	return h
}

func (h scratchSetupHarness) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{"-c", scScratchSetupScript, "sc-scratch-setup"}, args...)...)
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

func (h scratchSetupHarness) bindCalls() []string {
	return []string{
		"mount --bind " + filepath.Join(h.root, "tmp") + " " + h.tmp,
		"mount --bind " + filepath.Join(h.root, "var-tmp") + " " + h.varTmp,
	}
}

func TestScratchSetupBindsTmpAndVarTmp(t *testing.T) {
	h := newScratchSetupHarness(t, scratchMachine{scratchMounted: true})
	if out, err := h.run(t); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
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
	for _, want := range h.bindCalls() {
		if !strings.Contains(calls, want) {
			t.Fatalf("missing %q in calls:\n%s", want, calls)
		}
	}
	if strings.Contains(calls, "systemctl") {
		t.Fatalf("the boot-time unit must not touch other services, got:\n%s", calls)
	}
}

func TestScratchSetupMountsTheVMShareItself(t *testing.T) {
	h := newScratchSetupHarness(t, scratchMachine{vm: true, virtiofsOK: true})
	if out, err := h.run(t); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	calls := h.calls(t)
	for _, want := range append([]string{"mount -t virtiofs incus_scratch " + h.root}, h.bindCalls()...) {
		if !strings.Contains(calls, want) {
			t.Fatalf("missing %q in calls:\n%s", want, calls)
		}
	}
}

func TestScratchSetupIsANoOpWithoutTheVolume(t *testing.T) {
	for name, m := range map[string]scratchMachine{
		"container": {},
		"vm":        {vm: true},
	} {
		h := newScratchSetupHarness(t, m)
		if out, err := h.run(t, "--restart-private-tmp"); err != nil {
			t.Fatalf("%s: setup must exit 0 without a volume: %v\n%s", name, err, out)
		}
		if calls := h.calls(t); strings.Contains(calls, "--bind") || strings.Contains(calls, "systemctl") {
			t.Fatalf("%s: nothing may be bound or restarted, got:\n%s", name, calls)
		}
		if _, err := os.Stat(filepath.Join(h.root, "tmp")); !os.IsNotExist(err) {
			t.Fatalf("%s: setup populated %s on a machine without the volume", name, h.root)
		}
	}
}

func TestScratchSetupIsIdempotentOnceBound(t *testing.T) {
	h := newScratchSetupHarness(t, scratchMachine{scratchMounted: true})
	for _, pair := range [][2]string{{"tmp", h.tmp}, {"var-tmp", h.varTmp}} {
		if err := os.MkdirAll(filepath.Join(h.root, pair[0]), 0o755); err != nil {
			t.Fatal(err)
		}
		// A symlink resolves to the same device and inode, which is how an
		// existing bind mount looks to the script.
		if err := os.Remove(pair[1]); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(h.root, pair[0]), pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := h.run(t, "--restart-private-tmp"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if calls := h.calls(t); calls != "" {
		t.Fatalf("an already bound machine must be neither mounted again nor restarted, got:\n%s", calls)
	}
}

func TestScratchSetupRestartsOnlyPrivateTmpServicesAfterBinding(t *testing.T) {
	h := newScratchSetupHarness(t, scratchMachine{scratchMounted: true})
	if out, err := h.run(t, "--restart-private-tmp"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	calls := h.calls(t)
	if !strings.Contains(calls, "systemctl try-restart a.service") {
		t.Fatalf("a PrivateTmp service was not restarted:\n%s", calls)
	}
	if strings.Contains(calls, "try-restart b.service") {
		t.Fatalf("a service without PrivateTmp was restarted:\n%s", calls)
	}
}

func TestScratchSetupFailsWhenTheBindFails(t *testing.T) {
	h := newScratchSetupHarness(t, scratchMachine{scratchMounted: true, bindExit: 32})
	if out, err := h.run(t, "--restart-private-tmp"); err == nil {
		t.Fatalf("a failed bind mount must fail the unit, got success:\n%s", out)
	}
	if calls := h.calls(t); strings.Contains(calls, "systemctl") {
		t.Fatalf("nothing was bound, so nothing may be restarted:\n%s", calls)
	}
}

func TestScratchBackfillInstallsWhatTheCheckAccepts(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	enabled := filepath.Join(dir, "enabled")
	path := scratchStubs(t, dir, map[string]string{
		"mountpoint": "exit 0",
		"systemctl": fmt.Sprintf(`echo "systemctl $*" >> %q
case "$1" in
enable) touch %q ;;
is-enabled) [ -e %q ] ;;
esac`, log, enabled, enabled),
	})
	paths := map[string]string{}
	relocate := func(script string) string {
		for _, f := range scratchGuestFiles {
			script = strings.ReplaceAll(script, f.path, paths[f.path])
		}
		return script
	}
	for _, f := range scratchGuestFiles {
		paths[f.path] = filepath.Join(dir, filepath.Base(f.path))
	}
	run := func(script string) string {
		t.Helper()
		cmd := exec.Command("sh", "-c", relocate(script))
		cmd.Env = append(os.Environ(), path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("script failed: %v\n%s", err, out)
		}
		return string(out)
	}
	if got := run(ScratchCheckScript()); got != "scratch: NEEDS FIX\n" {
		t.Fatalf("check on a bare machine = %q", got)
	}
	run(ScratchBackfillScript())
	for _, f := range scratchGuestFiles {
		data, err := os.ReadFile(paths[f.path])
		if err != nil || string(data) != relocate(f.content) {
			t.Fatalf("%s not installed verbatim: %v\n%s", f.path, err, data)
		}
	}
	if calls, _ := os.ReadFile(log); strings.Contains(string(calls), "start") {
		t.Fatalf("the backfill must enable the unit, never start it:\n%s", calls)
	}
	if got := run(ScratchCheckScript()); got != "scratch: OK\n" {
		t.Fatalf("check after backfill = %q", got)
	}
	if err := os.Chmod(paths[scScratchSetupPath], 0o644); err != nil {
		t.Fatal(err)
	}
	if got := run(ScratchCheckScript()); got != "scratch: NEEDS FIX\n" {
		t.Fatalf("check with a non-executable setup script = %q", got)
	}
	run(ScratchBackfillScript())
	if err := os.WriteFile(paths[scScratchProfilePath], []byte("# old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := run(ScratchCheckScript()); got != "scratch: NEEDS FIX\n" {
		t.Fatalf("check with a stale profile.d file = %q", got)
	}
	run(ScratchBackfillScript())
	if err := os.Remove(enabled); err != nil {
		t.Fatal(err)
	}
	if got := run(ScratchCheckScript()); got != "scratch: NEEDS FIX\n" {
		t.Fatalf("check with the unit disabled = %q", got)
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
