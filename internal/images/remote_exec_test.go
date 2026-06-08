package images

import (
	"context"
	"io"
	"testing"
)

// stubRunner returns a fixed output/err for any Run call, recording the args.
type stubRunner struct {
	out  string
	err  error
	args [][]string
}

func (s *stubRunner) Run(_ context.Context, _ io.Reader, args ...string) (string, error) {
	s.args = append(s.args, args)
	return s.out, s.err
}

// incus remote list --format json returns Addrs/LastWorkingAddr, not Addr.
const remoteListJSON = `{
  "big": {"Addrs": ["https://big.thieso2.dev:8443"], "LastWorkingAddr": "https://big.thieso2.dev:8443"},
  "fresh": {"Addrs": ["https://fresh.example.com:8443"], "LastWorkingAddr": ""},
  "local": {"Addrs": ["unix://"], "LastWorkingAddr": ""}
}`

func TestHostForRemoteParsesAddrs(t *testing.T) {
	t.Setenv("SANDCASTLE_IMAGE_UPLOAD_SSH_HOST", "")
	b := LocalRemoteBuilder{}
	runner := &stubRunner{out: remoteListJSON}

	// Prefers LastWorkingAddr.
	host, err := b.hostForRemote(context.Background(), runner, "big")
	if err != nil {
		t.Fatalf("big: unexpected error: %v", err)
	}
	if host != "big.thieso2.dev" {
		t.Fatalf("big: got %q, want big.thieso2.dev", host)
	}

	// Falls back to Addrs[0] when LastWorkingAddr is empty (never-connected remote).
	host, err = b.hostForRemote(context.Background(), runner, "fresh")
	if err != nil {
		t.Fatalf("fresh: unexpected error: %v", err)
	}
	if host != "fresh.example.com" {
		t.Fatalf("fresh: got %q, want fresh.example.com", host)
	}
}

func TestHostForRemoteEnvOverride(t *testing.T) {
	t.Setenv("SANDCASTLE_IMAGE_UPLOAD_SSH_HOST", "override.example.com")
	b := LocalRemoteBuilder{}
	runner := &stubRunner{err: io.EOF} // must not be consulted

	host, err := b.hostForRemote(context.Background(), runner, "big")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "override.example.com" {
		t.Fatalf("got %q, want override.example.com", host)
	}
	if len(runner.args) != 0 {
		t.Fatalf("runner should not be called when override is set, got %v", runner.args)
	}
}

func TestHostForRemoteUnknownRemoteErrors(t *testing.T) {
	t.Setenv("SANDCASTLE_IMAGE_UPLOAD_SSH_HOST", "")
	b := LocalRemoteBuilder{}
	runner := &stubRunner{out: remoteListJSON}

	if _, err := b.hostForRemote(context.Background(), runner, "missing"); err == nil {
		t.Fatal("expected error for unknown remote, got nil")
	}
}
