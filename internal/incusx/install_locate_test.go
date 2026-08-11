package incusx

import (
	"reflect"
	"testing"

	"github.com/lxc/incus/v6/shared/cliconfig"
)

func TestOrderedRemoteNamesPutsDefaultFirst(t *testing.T) {
	got := orderedRemoteNames("big", []string{"home", "local", "big"})
	want := []string{"big", "home", "local"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered = %v, want %v", got, want)
	}
}

func TestOrderedRemoteNamesWithoutUsableDefault(t *testing.T) {
	// No default configured, and a default naming a remote that isn't in the
	// list, both fall back to plain alphabetical order — never to an empty or
	// reordered-but-missing list.
	for _, defaultRemote := range []string{"", "gone"} {
		got := orderedRemoteNames(defaultRemote, []string{"home", "big"})
		want := []string{"big", "home"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("default %q: ordered = %v, want %v", defaultRemote, got, want)
		}
	}
}

func TestRemoteNamesSkipsImageServers(t *testing.T) {
	// Image servers host no projects, so dialing them to ask which installs
	// they carry is pure latency.
	loaded := &cliconfig.Config{Remotes: map[string]cliconfig.Remote{
		"big":    {Addr: "https://big.example:8443"},
		"local":  {Addr: "unix://"},
		"images": {Addr: "https://images.example", Public: true, Protocol: "simplestreams"},
	}}
	got := orderedRemoteNames("", remoteNames(loaded))
	want := []string{"big", "local"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

func TestFindRemoteHostingInstallIgnoresEmptyPrefix(t *testing.T) {
	// An empty install name must never be turned into a search for "-infra";
	// it means "the user CLI is not on any install", which is not an error.
	if got := FindRemoteHostingInstall("  ", nil); got != "" {
		t.Fatalf("FindRemoteHostingInstall(empty) = %q, want empty", got)
	}
}
