package images

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IncusRunner runs incus CLI commands, optionally streaming stdin. It is the
// seam the RemoteBuilder and tests use instead of shelling out directly.
type IncusRunner interface {
	// Run executes `incus <args...>` and returns combined output.
	Run(ctx context.Context, stdin io.Reader, args ...string) (string, error)
}

// LocalRemoteBuilder drives the Image Builder appliance over the local incus
// CLI. The GHCR push token is read lazily so it never needs to be held in the
// plan or passed on argv.
type LocalRemoteBuilder struct {
	Runner IncusRunner
	// Token returns the GHCR push token (e.g. from $SANDCASTLE_GHCR_TOKEN).
	Token func() (string, error)
	// Stderr receives human-readable progress; defaults to os.Stderr.
	Stderr io.Writer
	// Verbose streams the underlying incus exec output (e.g. podman build/push
	// progress) live to Stderr instead of buffering it until each step finishes.
	Verbose bool
}

// incusCLIRunner shells out to the incus CLI. When stream is non-nil (VERBOSE=1)
// it tees combined output there live so long steps like `podman build` show
// progress in real time, instead of buffering until the step finishes.
type incusCLIRunner struct {
	stream io.Writer
}

func (r incusCLIRunner) Run(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "incus", args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	if r.stream != nil {
		var buf strings.Builder
		w := io.MultiWriter(&buf, r.stream)
		cmd.Stdout = w
		cmd.Stderr = w
		err := cmd.Run()
		if err != nil {
			return buf.String(), fmt.Errorf("incus %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(buf.String()))
		}
		return buf.String(), nil
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("incus %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// setup builds the incus runner (streaming combined output when Verbose) and a
// progress logger. Shared by BuildRemote and ProvisionBuilder.
func (b LocalRemoteBuilder) setup() (IncusRunner, func(string, ...any)) {
	stderr := b.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	runner := b.Runner
	if runner == nil {
		if b.Verbose {
			runner = incusCLIRunner{stream: stderr}
		} else {
			runner = incusCLIRunner{}
		}
	}
	log := func(format string, args ...any) {
		fmt.Fprintf(stderr, "[build-remote] "+format+"\n", args...)
	}
	return runner, log
}

func (b LocalRemoteBuilder) BuildRemote(ctx context.Context, plan RemoteBuildPlan) (RemoteBuildResult, error) {
	runner, log := b.setup()

	if err := b.ensureAppliance(ctx, runner, plan, log); err != nil {
		return RemoteBuildResult{}, err
	}

	log("ship build context %s", plan.ContextDir)
	if err := b.shipContext(ctx, runner, plan); err != nil {
		return RemoteBuildResult{}, err
	}

	result := RemoteBuildResult{RemoteBuildPlan: plan}

	if !plan.NoPush {
		log("write GHCR token to %s (tmpfs)", builderTokenPath)
		if err := b.writeToken(ctx, runner, plan); err != nil {
			return RemoteBuildResult{}, err
		}
	}

	log("build %s (and :latest) for template %s", plan.ImageVersncRef, plan.Template)
	ref := plan.Builder.instanceRef()
	if _, err := runner.Run(ctx, strings.NewReader(plan.BuildScript),
		"exec", "--project", plan.Builder.Project, ref, "--", "bash", "-s"); err != nil {
		return RemoteBuildResult{}, err
	}
	result.Pushed = !plan.NoPush

	if plan.NoImport {
		log("skip import (--no-import); published %s", plan.ImageVersncRef)
		return result, nil
	}

	log("import %s into %s alias %s (on host)", plan.ImageVersncRef, plan.Builder.Remote, plan.Alias)
	if err := b.importOnHost(ctx, runner, plan); err != nil {
		return RemoteBuildResult{}, err
	}
	result.Imported = true
	return result, nil
}

// importOnHost runs the OCI->Incus copy on the Incus host itself, because the
// incus CLI resolves OCI manifests for the client architecture. It ensures the
// ghcr OCI remote there, then copies the published image into the alias.
func (b LocalRemoteBuilder) importOnHost(ctx context.Context, runner IncusRunner, plan RemoteBuildPlan) error {
	script := "set -e\n" +
		"incus remote add " + plan.GHCRRemote + " " + ghcrRegistryURL + " --protocol oci >/dev/null 2>&1 || true\n" +
		strings.Join(plan.ImportCommand, " ") + "\n"

	if plan.Builder.Remote == "local" {
		// The local incus host is this machine; run the script directly.
		_, err := runShell(ctx, nil, "bash", "-c", script)
		return err
	}
	host, err := b.hostForRemote(ctx, runner, plan.Builder.Remote)
	if err != nil {
		return err
	}
	user := os.Getenv("SANDCASTLE_IMAGE_UPLOAD_SSH_USER")
	if strings.TrimSpace(user) == "" {
		user = "root"
	}
	_, err = runShell(ctx, strings.NewReader(script), "ssh", user+"@"+host, "bash", "-s")
	return err
}

// hostForRemote resolves the SSH hostname for an Incus remote from its address.
func (b LocalRemoteBuilder) hostForRemote(ctx context.Context, runner IncusRunner, remote string) (string, error) {
	if override := strings.TrimSpace(os.Getenv("SANDCASTLE_IMAGE_UPLOAD_SSH_HOST")); override != "" {
		return override, nil
	}
	out, err := runner.Run(ctx, nil, "remote", "list", "--format", "json")
	if err != nil {
		return "", err
	}
	var remotes map[string]struct {
		Addrs           []string `json:"Addrs"`
		LastWorkingAddr string   `json:"LastWorkingAddr"`
	}
	if err := json.Unmarshal([]byte(out), &remotes); err != nil {
		return "", fmt.Errorf("parse incus remotes: %w", err)
	}
	addr := remotes[remote].LastWorkingAddr
	if addr == "" && len(remotes[remote].Addrs) > 0 {
		addr = remotes[remote].Addrs[0]
	}
	u, err := url.Parse(addr)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("cannot derive SSH host for remote %q (addr %q); set SANDCASTLE_IMAGE_UPLOAD_SSH_HOST", remote, addr)
	}
	return u.Hostname(), nil
}

// runShell executes a non-incus command (ssh/bash) for the host import step.
func runShell(ctx context.Context, stdin io.Reader, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// ProvisionBuilder creates the appliance if absent (otherwise starts it) and
// runs the provision script, deliberately and idempotently. This is the explicit
// counterpart to BuildRemote, which only auto-provisions on a cold start.
func (b LocalRemoteBuilder) ProvisionBuilder(ctx context.Context, app BuilderAppliance) error {
	runner, log := b.setup()
	if exists, err := b.applianceExists(ctx, runner, app); err != nil {
		return err
	} else if !exists {
		if err := b.createAppliance(ctx, runner, app, log); err != nil {
			return err
		}
	} else {
		log("start %s", app.Instance)
		_, _ = runner.Run(ctx, nil, "start", "--project", app.Project, app.instanceRef())
	}
	if err := b.waitForNetwork(ctx, runner, app, log); err != nil {
		return err
	}
	return b.provisionPodman(ctx, runner, app, builderProvisionScript(), log)
}

// ensureAppliance prepares the appliance for a build. On a cold start (instance
// absent) it creates and provisions it; on a warm start it only ensures the
// instance is running and reachable, skipping the provision script. Re-provision
// deliberately with `image builder provision`.
func (b LocalRemoteBuilder) ensureAppliance(ctx context.Context, runner IncusRunner, plan RemoteBuildPlan, log func(string, ...any)) error {
	app := plan.Builder

	if plan.RebuildBuilder {
		log("--rebuild-builder: deleting existing %s", app.Instance)
		_, _ = runner.Run(ctx, nil, "delete", "--project", app.Project, "--force", app.instanceRef())
	}

	exists, err := b.applianceExists(ctx, runner, app)
	if err != nil {
		return err
	}
	if !exists {
		// Cold start: create and provision (auto-provision only when absent).
		if err := b.createAppliance(ctx, runner, app, log); err != nil {
			return err
		}
		if err := b.waitForNetwork(ctx, runner, app, log); err != nil {
			return err
		}
		return b.provisionPodman(ctx, runner, app, plan.ProvisionScript, log)
	}

	// Warm start: ensure running, skip the provision script (decoupled).
	log("start %s (warm; skip provision — run `image builder provision` to re-provision)", app.Instance)
	_, _ = runner.Run(ctx, nil, "start", "--project", app.Project, app.instanceRef())
	return b.waitForNetwork(ctx, runner, app, log)
}

// applianceExists reports whether the appliance instance is present. Any error
// from `incus info` (including not-found) is treated as absent, matching the
// prior create-on-any-error behavior.
func (b LocalRemoteBuilder) applianceExists(ctx context.Context, runner IncusRunner, app BuilderAppliance) (bool, error) {
	_, err := runner.Run(ctx, nil, "info", "--project", app.Project, app.instanceRef())
	return err == nil, nil
}

// createAppliance creates the project, cache volume, and instance with the cache
// disk attached. It does not provision podman; callers run provisionPodman next.
func (b LocalRemoteBuilder) createAppliance(ctx context.Context, runner IncusRunner, app BuilderAppliance, log func(string, ...any)) error {
	ref := app.instanceRef()

	log("ensure project %s", app.Project)
	_, _ = runner.Run(ctx, nil, "project", "create", app.Remote+":"+app.Project,
		"-c", "features.images=false", "-c", "features.profiles=false")

	log("ensure cache volume %s", app.Volume)
	_, _ = runner.Run(ctx, nil, "storage", "volume", "create",
		app.Remote+":"+app.StoragePool, app.Volume, "--project", app.Project)

	log("launch %s from %s", app.Instance, app.Image)
	// security.nesting also makes LXC expose /dev/fuse inside the container,
	// so fuse-overlayfs works without an explicit unix-char device (adding
	// one fails because the node already exists).
	if _, err := runner.Run(ctx, nil, "launch", app.Image, ref,
		"--project", app.Project, "-c", "security.nesting=true"); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, nil, "config", "device", "add", "--project", app.Project, ref,
		"cache", "disk", "pool="+app.StoragePool, "source="+app.Volume, "path="+app.StorageMount); err != nil {
		return err
	}
	return nil
}

// provisionPodman runs the (idempotent) provision script inside the appliance.
func (b LocalRemoteBuilder) provisionPodman(ctx context.Context, runner IncusRunner, app BuilderAppliance, script string, log func(string, ...any)) error {
	log("provision podman in %s", app.Instance)
	_, err := runner.Run(ctx, strings.NewReader(script),
		"exec", "--project", app.Project, app.instanceRef(), "--", "bash", "-s")
	return err
}

// waitForNetwork blocks until the appliance can resolve and reach the network,
// which a freshly launched container needs before apt/podman can run.
func (b LocalRemoteBuilder) waitForNetwork(ctx context.Context, runner IncusRunner, app BuilderAppliance, log func(string, ...any)) error {
	log("wait for network in %s", app.Instance)
	script := `set -e
for i in $(seq 1 60); do
  if getent hosts ` + ghcrRegistryHost + ` >/dev/null 2>&1; then exit 0; fi
  sleep 1
done
echo "network not ready in appliance" >&2
exit 1
`
	_, err := runner.Run(ctx, strings.NewReader(script),
		"exec", "--project", app.Project, app.instanceRef(), "--", "bash", "-s")
	return err
}

func (b LocalRemoteBuilder) shipContext(ctx context.Context, runner IncusRunner, plan RemoteBuildPlan) error {
	srcDir := plan.ContextDir
	if plan.WorkDir != "" && !filepath.IsAbs(srcDir) {
		srcDir = filepath.Join(plan.WorkDir, srcDir)
	}
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(writeContextTar(pw, srcDir, plan.Template))
	}()
	ref := plan.Builder.instanceRef()
	// Reset and extract under the build root; the leading dir in the archive is
	// the template name, so extraction recreates <buildRoot>/<template>/...
	reset := "rm -rf " + builderBuildRoot + "/" + plan.Template + " && mkdir -p " + builderBuildRoot
	if _, err := runner.Run(ctx, nil, "exec", "--project", plan.Builder.Project, ref, "--", "bash", "-c", reset); err != nil {
		return err
	}
	_, err := runner.Run(ctx, pr, "exec", "--project", plan.Builder.Project, ref, "--",
		"tar", "-xzf", "-", "-C", builderBuildRoot)
	return err
}

func (b LocalRemoteBuilder) writeToken(ctx context.Context, runner IncusRunner, plan RemoteBuildPlan) error {
	if b.Token == nil {
		return fmt.Errorf("GHCR token source is not configured (set SANDCASTLE_GHCR_TOKEN or pass --no-push)")
	}
	token, err := b.Token()
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("GHCR token is empty (set SANDCASTLE_GHCR_TOKEN or pass --no-push)")
	}
	ref := plan.Builder.instanceRef()
	write := "umask 077; cat > " + builderTokenPath
	_, err = runner.Run(ctx, strings.NewReader(token),
		"exec", "--project", plan.Builder.Project, ref, "--", "bash", "-c", write)
	return err
}

// BuilderStatus reports the appliance instance state, or a not-provisioned
// message when it does not exist.
func (b LocalRemoteBuilder) BuilderStatus(ctx context.Context, app BuilderAppliance) (string, error) {
	runner := b.Runner
	if runner == nil {
		runner = incusCLIRunner{}
	}
	out, err := runner.Run(ctx, nil, "list", "--project", app.Project, app.Remote+":"+app.Instance,
		"--format", "csv", "-c", "ns")
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(out)
	if line == "" {
		return fmt.Sprintf("Image Builder %s not provisioned in %s:%s", app.Instance, app.Remote, app.Project), nil
	}
	return fmt.Sprintf("Image Builder %s in %s:%s — %s", app.Instance, app.Remote, app.Project, line), nil
}

// BuilderDestroy tears down the appliance and its project. The cache volume is
// removed unless keepCache is set.
func (b LocalRemoteBuilder) BuilderDestroy(ctx context.Context, app BuilderAppliance, keepCache bool) error {
	runner := b.Runner
	if runner == nil {
		runner = incusCLIRunner{}
	}
	ref := app.Remote + ":" + app.Instance
	if _, err := runner.Run(ctx, nil, "delete", "--project", app.Project, "--force", ref); err != nil {
		// Tolerate a missing instance so destroy is idempotent.
		if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "No such") {
			return err
		}
	}
	if !keepCache {
		_, _ = runner.Run(ctx, nil, "storage", "volume", "delete",
			app.Remote+":"+app.StoragePool, app.Volume, "--project", app.Project)
	}
	if keepCache {
		return nil
	}
	_, err := runner.Run(ctx, nil, "project", "delete", app.Remote+":"+app.Project)
	if err != nil && !strings.Contains(err.Error(), "not found") {
		return err
	}
	return nil
}

// writeContextTar streams a gzip tar of srcDir, with entries rooted under
// prefix/ so the appliance extracts to <buildRoot>/<prefix>/...
func writeContextTar(w io.Writer, srcDir string, prefix string) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		name := prefix
		if rel != "." {
			name = filepath.Join(prefix, rel)
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(name)
		if info.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}
