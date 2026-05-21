package localtrust

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type Store interface {
	InstallCA(context.Context, Plan, []byte) (Result, error)
	UninstallCA(context.Context, Plan) (Result, error)
}

func NewPlatformStore() Store {
	if dir := os.Getenv("SANDCASTLE_TRUST_DIR"); dir != "" {
		return FileStore{Dir: dir}
	}
	return CommandStore{GOOS: runtime.GOOS}
}

type FileStore struct {
	Dir string
}

func (s FileStore) InstallCA(ctx context.Context, plan Plan, certPEM []byte) (Result, error) {
	if len(certPEM) == 0 {
		return Result{}, fmt.Errorf("tenant CA certificate is empty")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return Result{}, err
	}
	target := filepath.Join(s.Dir, CertFilename(plan))
	if err := os.WriteFile(target, certPEM, 0o644); err != nil {
		return Result{}, err
	}
	return Result{Reference: plan.Reference, TrustName: plan.TrustName, Platform: "file", Action: "install", Target: target}, nil
}

func (s FileStore) UninstallCA(ctx context.Context, plan Plan) (Result, error) {
	target := filepath.Join(s.Dir, CertFilename(plan))
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return Result{}, err
	}
	return Result{Reference: plan.Reference, TrustName: plan.TrustName, Platform: "file", Action: "uninstall", Target: target}, nil
}

type CommandStore struct {
	GOOS         string
	LinuxDir     string
	RunCommand   func(context.Context, string, ...string) ([]byte, error)
	EffectiveUID func() int
}

func (s CommandStore) InstallCA(ctx context.Context, plan Plan, certPEM []byte) (Result, error) {
	if len(certPEM) == 0 {
		return Result{}, fmt.Errorf("tenant CA certificate is empty")
	}
	switch s.GOOS {
	case "darwin":
		return s.installDarwin(ctx, plan, certPEM)
	case "linux":
		return s.installLinux(ctx, plan, certPEM)
	default:
		return Result{}, fmt.Errorf("local trust install is not supported on %s", s.GOOS)
	}
}

func (s CommandStore) UninstallCA(ctx context.Context, plan Plan) (Result, error) {
	switch s.GOOS {
	case "darwin":
		name, args := s.securityCommand("delete-certificate", "-c", plan.TrustName, "/Library/Keychains/System.keychain")
		if output, err := s.runCommand(ctx, name, args...); err != nil {
			return Result{}, fmt.Errorf("remove macOS trust certificate: %w: %s", err, string(output))
		}
		return Result{Reference: plan.Reference, TrustName: plan.TrustName, Platform: "darwin", Action: "uninstall", Target: "/Library/Keychains/System.keychain"}, nil
	case "linux":
		target := s.linuxTrustPath(plan)
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return Result{}, err
		}
		if err := s.runUpdateCACertificates(ctx); err != nil {
			return Result{}, err
		}
		return Result{Reference: plan.Reference, TrustName: plan.TrustName, Platform: "linux", Action: "uninstall", Target: target}, nil
	default:
		return Result{}, fmt.Errorf("local trust uninstall is not supported on %s", s.GOOS)
	}
}

func (s CommandStore) installDarwin(ctx context.Context, plan Plan, certPEM []byte) (Result, error) {
	tmp, err := os.CreateTemp("", "sandcastle-ca-*.crt")
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(certPEM); err != nil {
		tmp.Close()
		return Result{}, err
	}
	if err := tmp.Close(); err != nil {
		return Result{}, err
	}
	name, args := s.securityCommand("add-trusted-cert", "-d", "-r", "trustRoot", "-k", "/Library/Keychains/System.keychain", tmp.Name())
	if output, err := s.runCommand(ctx, name, args...); err != nil {
		return Result{}, fmt.Errorf("install macOS trust certificate: %w: %s", err, string(output))
	}
	return Result{Reference: plan.Reference, TrustName: plan.TrustName, Platform: "darwin", Action: "install", Target: "/Library/Keychains/System.keychain"}, nil
}

func (s CommandStore) securityCommand(args ...string) (string, []string) {
	if s.effectiveUID() == 0 {
		return "security", args
	}
	return "sudo", append([]string{"security"}, args...)
}

func (s CommandStore) effectiveUID() int {
	if s.EffectiveUID != nil {
		return s.EffectiveUID()
	}
	return os.Geteuid()
}

func (s CommandStore) installLinux(ctx context.Context, plan Plan, certPEM []byte) (Result, error) {
	target := s.linuxTrustPath(plan)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(target, certPEM, 0o644); err != nil {
		return Result{}, err
	}
	if err := s.runUpdateCACertificates(ctx); err != nil {
		return Result{}, err
	}
	return Result{Reference: plan.Reference, TrustName: plan.TrustName, Platform: "linux", Action: "install", Target: target}, nil
}

func (s CommandStore) linuxTrustPath(plan Plan) string {
	dir := s.LinuxDir
	if dir == "" {
		dir = "/usr/local/share/ca-certificates"
	}
	return filepath.Join(dir, CertFilename(plan))
}

func (s CommandStore) runUpdateCACertificates(ctx context.Context) error {
	output, err := s.runCommand(ctx, "update-ca-certificates")
	if err != nil {
		return fmt.Errorf("update CA certificates: %w: %s", err, string(output))
	}
	return nil
}

func (s CommandStore) runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	if s.RunCommand != nil {
		return s.RunCommand(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}
