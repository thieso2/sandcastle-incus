package cli

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

type loginSSHKeyRequest struct {
	PublicKeyPath string
	ExplicitPath  bool
}

type loginSSHKeyResult struct {
	PublicKey      string
	PublicKeyPath  string
	PrivateKeyPath string
	Fingerprint    string
}

func prepareLoginSSHKey(request loginSSHKeyRequest) (loginSSHKeyResult, error) {
	if request.ExplicitPath {
		if strings.TrimSpace(request.PublicKeyPath) == "" {
			return loginSSHKeyResult{}, fmt.Errorf("--ssh-public-key requires a path")
		}
		return loginSSHKeyFromFile(request.PublicKeyPath, true)
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return loginSSHKeyResult{}, fmt.Errorf("home directory is required to prepare SSH key")
	}
	defaultPublicKey := filepath.Join(home, ".ssh", "id_ed25519.pub")
	if result, err := loginSSHKeyFromFile(defaultPublicKey, false); err == nil {
		return result, nil
	}
	sandcastlePrivateKey := filepath.Join(home, ".ssh", "sandcastle_ed25519")
	sandcastlePublicKey := sandcastlePrivateKey + ".pub"
	if result, err := loginSSHKeyFromFile(sandcastlePublicKey, false); err == nil {
		return result, nil
	}
	return generateLoginSSHKey(sandcastlePrivateKey, sandcastlePublicKey)
}

func loginSSHKeyFromFile(path string, strict bool) (loginSSHKeyResult, error) {
	publicKey, err := readSSHPublicKeyFile(path)
	if err != nil {
		if strict {
			return loginSSHKeyResult{}, err
		}
		return loginSSHKeyResult{}, fmt.Errorf("read SSH public key %s: %w", path, err)
	}
	result, err := loginSSHKeyResultForPublicKey(publicKey)
	if err != nil {
		return loginSSHKeyResult{}, err
	}
	result.PublicKeyPath = path
	if strings.HasSuffix(path, ".pub") {
		result.PrivateKeyPath = strings.TrimSuffix(path, ".pub")
	}
	return result, nil
}

func generateLoginSSHKey(privateKeyPath string, publicKeyPath string) (loginSSHKeyResult, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return loginSSHKeyResult{}, fmt.Errorf("generate Sandcastle SSH key: %w", err)
	}
	sshPublicKey, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		return loginSSHKeyResult{}, fmt.Errorf("encode Sandcastle SSH public key: %w", err)
	}
	privateBlock, err := ssh.MarshalPrivateKey(privateKey, "sandcastle")
	if err != nil {
		return loginSSHKeyResult{}, fmt.Errorf("encode Sandcastle SSH private key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(privateKeyPath), 0o700); err != nil {
		return loginSSHKeyResult{}, fmt.Errorf("create SSH directory: %w", err)
	}
	privatePEM := pem.EncodeToMemory(privateBlock)
	if err := os.WriteFile(privateKeyPath, privatePEM, 0o600); err != nil {
		return loginSSHKeyResult{}, fmt.Errorf("write Sandcastle SSH private key: %w", err)
	}
	authorizedKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublicKey)))
	if err := os.WriteFile(publicKeyPath, []byte(authorizedKey+"\n"), 0o644); err != nil {
		return loginSSHKeyResult{}, fmt.Errorf("write Sandcastle SSH public key: %w", err)
	}
	result, err := loginSSHKeyResultForPublicKey(authorizedKey)
	if err != nil {
		return loginSSHKeyResult{}, err
	}
	result.PublicKeyPath = publicKeyPath
	result.PrivateKeyPath = privateKeyPath
	return result, nil
}

func loginSSHKeyResultForPublicKey(publicKey string) (loginSSHKeyResult, error) {
	parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(publicKey))
	if err != nil {
		return loginSSHKeyResult{}, fmt.Errorf("parse SSH public key: %w", err)
	}
	return loginSSHKeyResult{
		PublicKey:   strings.TrimSpace(publicKey),
		Fingerprint: ssh.FingerprintSHA256(parsed),
	}, nil
}
