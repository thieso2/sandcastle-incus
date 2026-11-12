package tenant

import (
	"reflect"
	"strings"
	"testing"
)

func TestMachineScratchVolumeName(t *testing.T) {
	if got := (MachineScratch{Machine: "web"}).VolumeName(); got != "web-scratch" {
		t.Fatalf("VolumeName = %q, want web-scratch", got)
	}
	long := strings.Repeat("a", 30) + "-" + strings.Repeat("b", 32)
	other := strings.Repeat("a", 30) + "-" + strings.Repeat("c", 32)
	got := (MachineScratch{Machine: long}).VolumeName()
	if len(got) > 64 || !strings.HasSuffix(got, "-scratch") || !strings.HasPrefix(got, strings.Repeat("a", 30)) {
		t.Fatalf("long VolumeName = %q (%d chars), want <=64 chars ending -scratch", got, len(got))
	}
	if got == (MachineScratch{Machine: other}).VolumeName() {
		t.Fatalf("two long machine names sharing a prefix map to the same volume %q", got)
	}
	if got != (MachineScratch{Machine: long}).VolumeName() {
		t.Fatal("VolumeName is not deterministic")
	}
	edge := strings.Repeat("m", 56)
	if got := (MachineScratch{Machine: edge}).VolumeName(); got != edge+"-scratch" {
		t.Fatalf("a 56-char machine name fits as-is, got %q", got)
	}
}

func TestMachineScratchVolumeConfig(t *testing.T) {
	scratch := MachineScratch{Machine: "web", Pool: "tank", Size: "20GiB"}
	want := map[string]string{
		"user.sandcastle.scratch": "true",
		"snapshots.schedule":      "",
		"size":                    "20GiB",
		"initial.uid":             "0",
		"initial.gid":             "0",
		"initial.mode":            "0755",
	}
	if got := scratch.VolumeConfig(false); !reflect.DeepEqual(got, want) {
		t.Fatalf("VolumeConfig(false) = %v, want %v", got, want)
	}
	want["security.shifted"] = "true"
	if got := scratch.VolumeConfig(true); !reflect.DeepEqual(got, want) {
		t.Fatalf("VolumeConfig(true) = %v, want %v", got, want)
	}
	if !IsScratchVolume(scratch.VolumeConfig(false)) {
		t.Fatal("the volume's own config is not recognised as scratch")
	}
	if IsScratchVolume(map[string]string{}) || IsScratchVolume(map[string]string{"user.sandcastle.scratch": "false"}) {
		t.Fatal("an unmarked volume is recognised as scratch")
	}
}

func TestMachineScratchDevice(t *testing.T) {
	got := (MachineScratch{Machine: "web", Pool: "tank", Size: "1GiB"}).Device()
	want := map[string]string{"type": "disk", "pool": "tank", "source": "web-scratch", "path": "/scratch"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Device = %v, want %v", got, want)
	}
}

func TestParseScratchSize(t *testing.T) {
	for raw, want := range map[string]string{"": DefaultScratchSize, " 50GB ": "50GB", "1TiB": "1TiB", "100GiB": "100GiB"} {
		got, err := ParseScratchSize(raw)
		if err != nil || got != want {
			t.Fatalf("ParseScratchSize(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"lots", "GiB", "0", "0GiB", "10 apples", "-5GiB"} {
		if got, err := ParseScratchSize(raw); err == nil {
			t.Fatalf("ParseScratchSize(%q) = %q, want an error", raw, got)
		}
	}
}
