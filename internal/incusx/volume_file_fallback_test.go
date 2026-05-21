package incusx

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
)

func TestLocalDirStorageVolumeFileFallbackWritesAndReads(t *testing.T) {
	root := t.TempDir()
	oldRoot := incusStoragePoolRoot
	incusStoragePoolRoot = root
	t.Cleanup(func() { incusStoragePoolRoot = oldRoot })

	volumeDir := filepath.Join(root, "sc-acme", "custom", "sc-acme_sc-ca")
	if err := os.MkdirAll(volumeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	err := writeLocalDirStorageVolumeFile("sc-acme", "sc-acme", "custom", "sc-ca", "/ca.crt", incus.InstanceFileArgs{
		Content:   strings.NewReader("certificate"),
		Mode:      0o600,
		Type:      "file",
		WriteMode: "overwrite",
	})
	if err != nil {
		t.Fatal(err)
	}

	content, response, err := readLocalDirStorageVolumeFile("sc-acme", "sc-acme", "custom", "sc-ca", "/ca.crt")
	if err != nil {
		t.Fatal(err)
	}
	defer content.Close()
	data, err := io.ReadAll(content)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "certificate" {
		t.Fatalf("content = %q", data)
	}
	if response.Type != "file" || response.Mode != 0o600 {
		t.Fatalf("response = %#v", response)
	}
}

func TestLocalDirStorageVolumeFileFallbackRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	oldRoot := incusStoragePoolRoot
	incusStoragePoolRoot = root
	t.Cleanup(func() { incusStoragePoolRoot = oldRoot })

	volumeDir := filepath.Join(root, "sc-acme", "custom", "sc-acme_sc-ca")
	if err := os.MkdirAll(volumeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	err := writeLocalDirStorageVolumeFile("sc-acme", "sc-acme", "custom", "sc-ca", "/../escape", incus.InstanceFileArgs{
		Content: strings.NewReader("nope"),
		Type:    "file",
	})
	if err == nil {
		t.Fatal("expected traversal error")
	}
}
