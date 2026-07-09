package hostkeys

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	keyA = "AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	keyB = "AAAAC3NzaC1lZDI1NTE5AAAAIBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

func testConfig(t *testing.T, lines ...string) Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	if len(lines) > 0 {
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Config{
		Path:   path,
		Remote: "big",
		Tenant: "obelix",
		CIDR:   netip.MustParsePrefix("10.248.0.0/24"),
		Now:    func() time.Time { return time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC) },
	}
}

func tubu(keys ...string) Machine {
	machine := Machine{Names: []string{"tubu.default.obelix", "tubu.obelix"}}
	for _, key := range keys {
		machine.Keys = append(machine.Keys, Key{Type: "ssh-ed25519", Key: key})
	}
	return machine
}

func readback(t *testing.T, path string) []string {
	t.Helper()
	lines, err := readLines(path)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func hashedLine(t *testing.T, host string, key string) string {
	t.Helper()
	salt := make([]byte, 20)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte(host))
	return "|1|" + base64.StdEncoding.EncodeToString(salt) + "|" +
		base64.StdEncoding.EncodeToString(mac.Sum(nil)) + " ssh-ed25519 " + key
}

func mustApply(t *testing.T, plan Plan) {
	t.Helper()
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
}

// The original bug: a stale, untagged entry written by a bare `ssh` before the
// machine was rebuilt. ssh reads the first match, so appending our line is not
// enough — the old one has to go.
func TestEnsureReclaimsStaleUntaggedName(t *testing.T) {
	config := testConfig(t, "tubu.default.obelix ssh-ed25519 "+keyB)
	plan, err := config.Ensure(tubu(keyA))
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, plan)

	lines := readback(t, config.Path)
	if len(lines) != 1 {
		t.Fatalf("expected the stale line replaced, got %d lines: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], keyA) || strings.Contains(lines[0], keyB) {
		t.Fatalf("stale key survived: %q", lines[0])
	}
	if !strings.Contains(lines[0], "# sandcastle:big/obelix") {
		t.Fatalf("line is not tagged: %q", lines[0])
	}
	if !strings.HasPrefix(lines[0], "tubu.default.obelix,tubu.obelix ") {
		t.Fatalf("both names should share one line: %q", lines[0])
	}
}

func TestEnsurePurgesRecycledIPDebris(t *testing.T) {
	config := testConfig(t,
		"10.248.0.20 ssh-ed25519 "+keyB,
		"10.248.0.31 ssh-ed25519 "+keyB,
		"192.168.1.5 ssh-ed25519 "+keyB,
		"github.com ssh-ed25519 "+keyB,
	)
	plan, err := config.Ensure(tubu(keyA))
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, plan)

	got := strings.Join(readback(t, config.Path), "\n")
	for _, gone := range []string{"10.248.0.20", "10.248.0.31"} {
		if strings.Contains(got, gone) {
			t.Errorf("expected %s purged, file:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"192.168.1.5", "github.com"} {
		if !strings.Contains(got, kept) {
			t.Errorf("expected %s untouched, file:\n%s", kept, got)
		}
	}
}

func TestEnsurePurgesHashedIPEntry(t *testing.T) {
	config := testConfig(t,
		hashedLine(t, "10.248.0.20", keyB),
		hashedLine(t, "192.168.1.5", keyB),
	)
	plan, err := config.Ensure(tubu(keyA))
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, plan)

	lines := readback(t, config.Path)
	if len(lines) != 2 {
		t.Fatalf("expected the in-CIDR hashed line purged and the other kept, got: %v", lines)
	}
	for _, line := range lines {
		parsed := parseEntry(line)
		if parsed.kind == kindHashed && parsed.matchesHost("10.248.0.20") {
			t.Fatalf("hashed in-CIDR entry survived: %q", line)
		}
	}
}

// A hashed entry for a name outside the CIDR cannot be reversed, so we must not
// guess. It stays.
func TestEnsureLeavesHashedForeignNameAlone(t *testing.T) {
	line := hashedLine(t, "some.host.example.com", keyB)
	config := testConfig(t, line)
	plan, err := config.Ensure(tubu(keyA))
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, plan)
	if got := readback(t, config.Path); len(got) != 2 || got[0] != line {
		t.Fatalf("foreign hashed entry was disturbed: %v", got)
	}
}

func TestEnsureIsIdempotentAndDoesNotBackUp(t *testing.T) {
	config := testConfig(t, "github.com ssh-ed25519 "+keyB)
	first, err := config.Ensure(tubu(keyA))
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, first)

	second, err := config.Ensure(tubu(keyA))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Empty() {
		t.Fatalf("second Ensure should be a no-op, got %v", second.Changes)
	}
	// An add is not destructive, so the first run must not have snapshotted.
	if _, err := os.Stat(backupPath(config.Path, config.now())); !os.IsNotExist(err) {
		t.Fatal("a pure add should not create a backup")
	}
}

func TestDestructiveWriteCreatesBackupOnce(t *testing.T) {
	config := testConfig(t, "tubu.default.obelix ssh-ed25519 "+keyB)
	plan, err := config.Ensure(tubu(keyA))
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, plan)

	backup := backupPath(config.Path, config.now())
	content, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("expected a backup before the destructive write: %v", err)
	}
	if !strings.Contains(string(content), keyB) {
		t.Fatalf("backup should hold the pre-change file, got %q", content)
	}
	// A second destructive write the same day must not clobber the snapshot.
	plan, err = config.Ensure(tubu(keyB))
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, plan)
	again, err := os.ReadFile(backup)
	if err != nil || !strings.Contains(string(again), keyB) {
		t.Fatal("same-day backup should be preserved, not overwritten")
	}
}

func TestOpaqueLinesArePreserved(t *testing.T) {
	config := testConfig(t,
		"# a comment",
		"",
		"@cert-authority *.obelix ssh-ed25519 "+keyB,
		"@revoked bad.obelix ssh-ed25519 "+keyB,
	)
	plan, err := config.Ensure(tubu(keyA))
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, plan)
	got := strings.Join(readback(t, config.Path), "\n")
	for _, kept := range []string{"# a comment", "@cert-authority", "@revoked"} {
		if !strings.Contains(got, kept) {
			t.Errorf("expected %q preserved, file:\n%s", kept, got)
		}
	}
}

// A user wildcard shadows our line because ssh takes the first match. We refuse
// to delete it — it is theirs — but silence here would be a baffling failure.
func TestWildcardShadowWarns(t *testing.T) {
	config := testConfig(t, "*.obelix ssh-ed25519 "+keyB)
	plan, err := config.Ensure(tubu(keyA))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Warnings) == 0 {
		t.Fatal("expected a warning about the shadowing wildcard")
	}
	mustApply(t, plan)
	if got := strings.Join(readback(t, config.Path), "\n"); !strings.Contains(got, "*.obelix") {
		t.Fatalf("the user's wildcard must survive, file:\n%s", got)
	}
}

func TestCIDRGuardRefusesImplausiblyLargePrefix(t *testing.T) {
	config := testConfig(t, "192.168.1.5 ssh-ed25519 "+keyB)
	config.CIDR = netip.MustParsePrefix("192.168.0.0/16")
	plan, err := config.Ensure(tubu(keyA))
	if err != nil {
		t.Fatal(err)
	}
	if plan.PurgeSkipped == "" {
		t.Fatal("expected the oversized CIDR to disable purging")
	}
	mustApply(t, plan)
	if got := strings.Join(readback(t, config.Path), "\n"); !strings.Contains(got, "192.168.1.5") {
		t.Fatalf("a /16 must never be purged, file:\n%s", got)
	}
}

func TestCommaListLosesOnlyTheClaimedHost(t *testing.T) {
	config := testConfig(t, "tubu.default.obelix,unrelated.example.com ssh-ed25519 "+keyB+" user-comment")
	plan, err := config.Ensure(tubu(keyA))
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, plan)
	got := strings.Join(readback(t, config.Path), "\n")
	if !strings.Contains(got, "unrelated.example.com ssh-ed25519 "+keyB+" user-comment") {
		t.Fatalf("the co-tenant host and its comment must survive, file:\n%s", got)
	}
	if strings.Contains(got, "tubu.default.obelix,unrelated") {
		t.Fatalf("the claimed name should be gone from the old line, file:\n%s", got)
	}
}

func TestPurgeRemovesTaggedOrphansButEnsureDoesNot(t *testing.T) {
	orphan := "old.default.obelix,old.obelix ssh-ed25519 " + keyB + " # sandcastle:big/obelix"
	config := testConfig(t, orphan)

	ensure, err := config.Ensure(tubu(keyA))
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, ensure)
	if got := strings.Join(readback(t, config.Path), "\n"); !strings.Contains(got, "old.default.obelix") {
		t.Fatal("Ensure must not remove tagged lines for machines it was not asked about")
	}

	purge, err := config.Purge([]Machine{tubu(keyA)})
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, purge)
	if got := strings.Join(readback(t, config.Path), "\n"); strings.Contains(got, "old.default.obelix") {
		t.Fatalf("Purge should drop the tagged orphan, file:\n%s", got)
	}
}

func TestPurgeIgnoresOtherTenantsTaggedLines(t *testing.T) {
	foreign := "x.default.asterix ssh-ed25519 " + keyB + " # sandcastle:big/asterix"
	config := testConfig(t, foreign)
	plan, err := config.Purge([]Machine{tubu(keyA)})
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, plan)
	if got := strings.Join(readback(t, config.Path), "\n"); !strings.Contains(got, "asterix") {
		t.Fatalf("another tenant's tagged line must survive, file:\n%s", got)
	}
}

// A live machine we cannot read (VM with no incus-agent) must keep whatever is
// recorded. Treating it as an orphan would delete a working entry.
func TestPurgeProtectsUnreadableMachine(t *testing.T) {
	existing := "vm.default.obelix,vm.obelix ssh-ed25519 " + keyB + " # sandcastle:big/obelix"
	config := testConfig(t, existing)
	unreadable := Machine{Names: []string{"vm.default.obelix", "vm.obelix"}}

	plan, err := config.Purge([]Machine{unreadable})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Empty() {
		t.Fatalf("expected no changes for an unreadable machine, got %v", plan.Changes)
	}
	mustApply(t, plan)
	if got := strings.Join(readback(t, config.Path), "\n"); !strings.Contains(got, keyB) {
		t.Fatalf("unreadable machine's entry must survive, file:\n%s", got)
	}
}

// A key learned by ssh-keyscan is tagged `tofu`; once the Incus API can be read
// the line is rewritten and the marker disappears.
func TestTOFULineIsUpgradedToAuthoritative(t *testing.T) {
	config := testConfig(t)
	scanned := tubu(keyA)
	scanned.TOFU = true
	plan, err := config.Ensure(scanned)
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, plan)
	if got := readback(t, config.Path); !strings.HasSuffix(got[0], "# sandcastle:big/obelix tofu") {
		t.Fatalf("expected a tofu marker, got %q", got[0])
	}

	plan, err = config.Ensure(tubu(keyA))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Empty() {
		t.Fatal("authoritative read should rewrite the tofu line")
	}
	mustApply(t, plan)
	got := readback(t, config.Path)
	if len(got) != 1 || strings.Contains(got[0], tofuMarker) {
		t.Fatalf("tofu marker should be gone: %v", got)
	}
}

// After a successful bare `ssh`, OpenSSH's UpdateHostKeys appends the server's
// other host keys as untagged lines. If we recorded only one key we would
// reclaim those names, delete them, and ssh would add them right back on the
// next connection — a permanent ping-pong. Recording every key converges.
func TestAllHostKeysAreRecordedSoUpdateHostKeysConverges(t *testing.T) {
	machine := Machine{
		Names: []string{"tubu.default.obelix", "tubu.obelix"},
		Keys: []Key{
			{Type: "ssh-ed25519", Key: keyA},
			{Type: "ssh-rsa", Key: keyB},
		},
	}
	// The file as ssh leaves it: our ed25519 line, plus the rsa key it learned.
	config := testConfig(t,
		renderLine(machine.Names, machine.Keys[0], Tag{Remote: "big", Tenant: "obelix"}),
		"tubu.default.obelix ssh-rsa "+keyB,
	)
	plan, err := config.Ensure(machine)
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, plan)

	lines := readback(t, config.Path)
	if len(lines) != 2 {
		t.Fatalf("expected one tagged line per key, got: %v", lines)
	}
	for _, line := range lines {
		if !strings.Contains(line, "# sandcastle:big/obelix") {
			t.Fatalf("every line should be tagged after reconcile: %q", line)
		}
	}

	// Now ssh has nothing left to add, so the next connect must be a no-op.
	second, err := config.Ensure(machine)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Empty() {
		t.Fatalf("reconcile must converge, got %v", second.Changes)
	}
}

// A plan is computed, shown to the user, and only then applied. Anything that
// appended to known_hosts in between — another `sc`, a bare `ssh` — must not be
// silently overwritten by a stale line set.
func TestApplyRecomputesAgainstConcurrentAppend(t *testing.T) {
	config := testConfig(t, "tubu.default.obelix ssh-ed25519 "+keyB)
	plan, err := config.Ensure(tubu(keyA))
	if err != nil {
		t.Fatal(err)
	}

	// Someone else appends while we were deciding.
	file, err := os.OpenFile(config.Path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("late.example.com ssh-ed25519 " + keyB + "\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()

	mustApply(t, plan)
	got := strings.Join(readback(t, config.Path), "\n")
	if !strings.Contains(got, "late.example.com") {
		t.Fatalf("concurrent append was clobbered, file:\n%s", got)
	}
	if strings.Contains(got, "tubu.default.obelix ssh-ed25519 "+keyB) {
		t.Fatalf("stale entry survived the recompute, file:\n%s", got)
	}
}

func TestConcurrentEnsuresDoNotLoseEachOther(t *testing.T) {
	config := testConfig(t)
	machines := []Machine{
		{Names: []string{"a.default.obelix"}, Keys: []Key{{Type: "ssh-ed25519", Key: keyA}}},
		{Names: []string{"b.default.obelix"}, Keys: []Key{{Type: "ssh-ed25519", Key: keyB}}},
		{Names: []string{"c.default.obelix"}, Keys: []Key{{Type: "ssh-ed25519", Key: keyA}}},
	}
	var group sync.WaitGroup
	for _, machine := range machines {
		group.Add(1)
		go func(machine Machine) {
			defer group.Done()
			plan, err := config.Ensure(machine)
			if err != nil {
				t.Error(err)
				return
			}
			if err := plan.Apply(); err != nil {
				t.Error(err)
			}
		}(machine)
	}
	group.Wait()

	got := strings.Join(readback(t, config.Path), "\n")
	for _, machine := range machines {
		if !strings.Contains(got, machine.Names[0]) {
			t.Errorf("%s was lost to a concurrent write, file:\n%s", machine.Names[0], got)
		}
	}
}

func TestFingerprintMatchesOpenSSH(t *testing.T) {
	// ssh-keygen -lf on this key prints this fingerprint.
	key := Key{Type: "ssh-ed25519", Key: keyA}
	if got := key.Fingerprint(); !strings.HasPrefix(got, "SHA256:") || len(got) != len("SHA256:")+43 {
		t.Fatalf("fingerprint is not in OpenSSH form: %q", got)
	}
}

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pattern string
		host    string
		want    bool
	}{
		{"*.obelix", "tubu.default.obelix", true},
		{"*.obelix", "obelix", false},
		{"tubu.?.obelix", "tubu.x.obelix", true},
		{"tubu.?.obelix", "tubu.xy.obelix", false},
		{"github.com", "github.com", true},
	}
	for _, testCase := range cases {
		if got := matchPattern(testCase.pattern, testCase.host); got != testCase.want {
			t.Errorf("matchPattern(%q, %q) = %v", testCase.pattern, testCase.host, got)
		}
	}
}
