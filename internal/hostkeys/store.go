package hostkeys

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const knownHostsMode = 0o600

// withLock serializes read-modify-write of known_hosts across concurrent `sc`
// processes. Two connects racing to claim different machines would otherwise
// each write back a copy of the file that lacks the other's change.
func withLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".sc-lock", os.O_CREATE|os.O_RDWR, knownHostsMode)
	if err != nil {
		return fmt.Errorf("open known_hosts lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock known_hosts: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return fn()
}

// readLines returns the file's lines, or nothing at all when it does not exist
// yet — a first connect on a fresh machine is not an error.
func readLines(path string) ([]string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	text := strings.TrimSuffix(string(content), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

// writeLines replaces the file atomically: a crash mid-write leaves the old
// known_hosts intact rather than a truncated one that locks you out of
// everything.
func writeLines(path string, lines []string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	mode := os.FileMode(knownHostsMode)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	temp, err := os.CreateTemp(dir, ".known_hosts-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	if _, err := temp.WriteString(body); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, mode); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

// backupPath is the once-per-day snapshot taken before the first destructive
// write. Removals are the only irreversible thing this package does, and the
// file it edits is the user's, not ours.
func backupPath(path string, now time.Time) string {
	return fmt.Sprintf("%s.sc-backup-%s", path, now.Format("2006-01-02"))
}

// ensureBackup copies the current file aside if today's snapshot is absent.
// Returns the backup path when one was created.
func ensureBackup(path string, now time.Time) (string, error) {
	target := backupPath(path, now)
	if _, err := os.Stat(target); err == nil {
		return "", nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if err := os.WriteFile(target, content, knownHostsMode); err != nil {
		return "", fmt.Errorf("back up known_hosts: %w", err)
	}
	return target, nil
}
