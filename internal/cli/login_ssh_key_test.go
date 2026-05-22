package cli

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestPrepareLoginSSHKeyUsesExplicitPublicKeyPath(t *testing.T) {
	key := validAuthorizedKeyForTest(t)
	path := filepath.Join(t.TempDir(), "chosen.pub")
	if err := os.WriteFile(path, []byte(key+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := prepareLoginSSHKey(loginSSHKeyRequest{PublicKeyPath: path, ExplicitPath: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.PublicKey != key || result.PublicKeyPath != path || result.PrivateKeyPath != strings.TrimSuffix(path, ".pub") {
		t.Fatalf("result = %#v", result)
	}
	if !strings.HasPrefix(result.Fingerprint, "SHA256:") {
		t.Fatalf("fingerprint = %q", result.Fingerprint)
	}
}

func TestPrepareLoginSSHKeyUsesDefaultEd25519PublicKey(t *testing.T) {
	home := useLoginHomeForTest(t)
	key := validAuthorizedKeyForTest(t)
	path := filepath.Join(home, ".ssh", "id_ed25519.pub")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(key+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := prepareLoginSSHKey(loginSSHKeyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result.PublicKey != key || result.PublicKeyPath != path || result.PrivateKeyPath != strings.TrimSuffix(path, ".pub") {
		t.Fatalf("result = %#v", result)
	}
}

func TestPrepareLoginSSHKeyGeneratesSandcastleKey(t *testing.T) {
	home := useLoginHomeForTest(t)

	result, err := prepareLoginSSHKey(loginSSHKeyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	wantPrivate := filepath.Join(home, ".ssh", "sandcastle_ed25519")
	wantPublic := wantPrivate + ".pub"
	if result.PublicKeyPath != wantPublic || result.PrivateKeyPath != wantPrivate {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(wantPrivate); err != nil {
		t.Fatalf("private key not written: %v", err)
	}
	if _, err := os.Stat(wantPublic); err != nil {
		t.Fatalf("public key not written: %v", err)
	}
	if !strings.HasPrefix(result.Fingerprint, "SHA256:") {
		t.Fatalf("fingerprint = %q", result.Fingerprint)
	}
}

func TestPrepareLoginSSHKeyRejectsEmptyExplicitKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.pub")
	if err := os.WriteFile(path, []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := prepareLoginSSHKey(loginSSHKeyRequest{PublicKeyPath: path, ExplicitPath: true})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error = %v", err)
	}
}

func TestPrepareLoginSSHKeyRejectsInvalidExplicitKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.pub")
	if err := os.WriteFile(path, []byte("ssh-ed25519 not-base64\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := prepareLoginSSHKey(loginSSHKeyRequest{PublicKeyPath: path, ExplicitPath: true})
	if err == nil || !strings.Contains(err.Error(), "parse SSH public key") {
		t.Fatalf("error = %v", err)
	}
}

func TestPrepareLoginSSHKeyRejectsEmptyExplicitPath(t *testing.T) {
	_, err := prepareLoginSSHKey(loginSSHKeyRequest{ExplicitPath: true})
	if err == nil || !strings.Contains(err.Error(), "--ssh-public-key requires a path") {
		t.Fatalf("error = %v", err)
	}
}

func useLoginHomeForTest(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func validAuthorizedKeyForTest(t *testing.T) string {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPublicKey, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublicKey)))
}
