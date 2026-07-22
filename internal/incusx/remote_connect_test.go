package incusx

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/lxc/incus/v6/shared/cliconfig"
)

func TestRemoteDialAddress(t *testing.T) {
	for _, testCase := range []struct {
		name string
		addr string
		want string
	}{
		{name: "https with port", addr: "https://100.97.217.39:8443", want: "100.97.217.39:8443"},
		{name: "https without port defaults to 8443", addr: "https://obelix.example.dev", want: "obelix.example.dev:8443"},
		{name: "unix socket is not dialable", addr: "unix://", want: ""},
		{name: "empty", addr: "", want: ""},
		// LoadCLIConfig has already collapsed multi-address remotes by the time
		// the probe runs; a comma-joined addr here would be a bug, not a host.
		{name: "unparseable host", addr: "https://", want: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := remoteDialAddress(testCase.addr); got != testCase.want {
				t.Fatalf("remoteDialAddress(%q) = %q, want %q", testCase.addr, got, testCase.want)
			}
		})
	}
}

// A remote whose TCP endpoint answers passes the probe.
func TestProbeRemoteReachableAcceptsListeningRemote(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	loaded := &cliconfig.Config{Remotes: map[string]cliconfig.Remote{
		"obelix": {Addr: "https://" + listener.Addr().String()},
	}}
	if err := probeRemoteReachable(loaded, "obelix"); err != nil {
		t.Fatalf("probe of a listening remote failed: %v", err)
	}
}

// The whole point of the probe: an unreachable remote fails FAST and names
// itself, instead of the Incus client blocking ~20s and the caller reporting
// the miss as a missing tenant.
func TestProbeRemoteReachableFailsFastAndNamesTheRemote(t *testing.T) {
	// Port 1 on the loopback: nothing listens, and the connection is refused
	// immediately rather than filtered, so the test never waits out the budget.
	loaded := &cliconfig.Config{Remotes: map[string]cliconfig.Remote{
		"obelix": {Addr: "https://127.0.0.1:1"},
	}}

	start := time.Now()
	err := probeRemoteReachable(loaded, "obelix")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("probe of an unreachable remote should fail")
	}
	if !strings.Contains(err.Error(), `"obelix"`) || !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Fatalf("error should name the remote and its address, got: %v", err)
	}
	if elapsed > defaultRemoteDialTimeout {
		t.Fatalf("probe took %s, want under the %s budget", elapsed, defaultRemoteDialTimeout)
	}
}

// Anything the probe cannot meaningfully dial is passed through to the Incus
// client rather than turned into a connect failure of our own.
func TestProbeRemoteReachableSkipsWhatItCannotDial(t *testing.T) {
	loaded := &cliconfig.Config{Remotes: map[string]cliconfig.Remote{
		"local": {Addr: "unix://"},
	}}
	if err := probeRemoteReachable(loaded, "local"); err != nil {
		t.Fatalf("unix remote should be passed through, got: %v", err)
	}
	if err := probeRemoteReachable(loaded, "unknown-remote"); err != nil {
		t.Fatalf("unknown remote should be passed through, got: %v", err)
	}
}

// The escape hatch must never be the thing that breaks a connect: a bad value
// disables the probe instead of failing the command.
func TestRemoteDialTimeoutOverride(t *testing.T) {
	for _, testCase := range []struct {
		name string
		env  string
		want time.Duration
	}{
		{name: "unset", env: "", want: defaultRemoteDialTimeout},
		{name: "override", env: "30s", want: 30 * time.Second},
		{name: "garbage disables", env: "soon", want: 0},
		{name: "zero disables", env: "0s", want: 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("SANDCASTLE_CONNECT_TIMEOUT", testCase.env)
			if got := remoteDialTimeout(); got != testCase.want {
				t.Fatalf("remoteDialTimeout() = %s, want %s", got, testCase.want)
			}
		})
	}
}
