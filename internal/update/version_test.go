package update

import "testing"

func TestIsDevBuild(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{"0.0.0-dev", true},              // go build / go test sentinel
		{"0.2.0-SNAPSHOT-abcdef1", true}, // goreleaser --snapshot
		{"", true},
		{"garbage", true},
		{"0.1.0", false},
		{"v0.1.0", false},
		{"1.2.3", false},
	}
	for _, c := range cases {
		if got := IsDevBuild(c.version); got != c.want {
			t.Errorf("IsDevBuild(%q) = %v, want %v", c.version, got, c.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.2.0", "0.1.0", true},
		{"v0.1.0", "0.1.0", false},
		{"v0.1.0", "0.2.0", false},
		{"v0.10.0", "0.9.0", true}, // numeric, not lexical
		{"v1.0.0", "0.9.9", true},
		{"", "0.1.0", false},
		{"v0.2.0", "0.0.0-dev", false}, // dev builds never count as outdated
		{"not-a-tag", "0.1.0", false},
	}
	for _, c := range cases {
		if got := IsNewer(c.latest, c.current); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}
