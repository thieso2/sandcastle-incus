package incusx

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

var incusStoragePoolRoot = "/var/lib/incus/storage-pools"

func createStorageVolumeFile(inner incus.InstanceServer, projectName string, pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error {
	err := inner.CreateStorageVolumeFile(pool, volumeType, volumeName, filePath, args)
	if err == nil || !api.StatusErrorCheck(err, http.StatusNotFound) {
		return err
	}
	if fallbackErr := writeLocalDirStorageVolumeFile(projectName, pool, volumeType, volumeName, filePath, args); fallbackErr != nil {
		return err
	}
	return nil
}

func getStorageVolumeFile(inner incus.InstanceServer, projectName string, pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	content, response, err := inner.GetStorageVolumeFile(pool, volumeType, volumeName, filePath)
	if err == nil || !api.StatusErrorCheck(err, http.StatusNotFound) {
		return content, response, err
	}
	return readLocalDirStorageVolumeFile(projectName, pool, volumeType, volumeName, filePath)
}

func writeLocalDirStorageVolumeFile(projectName string, pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error {
	target, err := localDirStorageVolumePath(projectName, pool, volumeType, volumeName, filePath)
	if err != nil {
		return err
	}
	if args.Type == "directory" {
		mode := os.FileMode(args.Mode)
		if mode == 0 {
			mode = 0o755
		}
		return os.MkdirAll(target, mode)
	}
	if args.Content == nil {
		return fmt.Errorf("storage volume file %s content is nil", filePath)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	flag := os.O_CREATE | os.O_WRONLY
	if args.WriteMode == "append" {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	mode := os.FileMode(args.Mode)
	if mode == 0 {
		mode = 0o644
	}
	file, err := os.OpenFile(target, flag, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, args.Content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	_ = os.Chown(target, int(args.UID), int(args.GID))
	return os.Chmod(target, mode)
}

func readLocalDirStorageVolumeFile(projectName string, pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	target, err := localDirStorageVolumePath(projectName, pool, volumeType, volumeName, filePath)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return file, &incus.InstanceFileResponse{
		Mode: int(info.Mode().Perm()),
		Type: "file",
	}, nil
}

func localDirStorageVolumePath(projectName string, pool string, volumeType string, volumeName string, filePath string) (string, error) {
	if volumeType != "custom" {
		return "", fmt.Errorf("local storage volume file fallback only supports custom volumes, got %q", volumeType)
	}
	for _, part := range strings.Split(filePath, "/") {
		if part == ".." {
			return "", fmt.Errorf("invalid storage volume file path %q", filePath)
		}
	}
	rel := strings.TrimPrefix(path.Clean("/"+filePath), "/")
	if rel == "." || rel == "" || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("invalid storage volume file path %q", filePath)
	}
	volumeDir := filepath.Join(incusStoragePoolRoot, pool, "custom", projectName+"_"+volumeName)
	if info, err := os.Stat(volumeDir); err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("%s is not a directory", volumeDir)
		}
		return "", err
	}
	return filepath.Join(volumeDir, filepath.FromSlash(rel)), nil
}
