package tenant

import "strings"

// The guest half of the Machine Scratch Volume. Three files are baked into
// every machine (cloud-init write_files, or `sc fix --only scratch`):
// sc-scratch-setup bind-mounts /scratch/tmp onto /tmp and /scratch/var-tmp
// onto /var/tmp, sc-scratch.service runs it early at boot, and a profile.d
// file points a login shell's XDG_CACHE_HOME at /scratch/cache/<user>. The
// payload's shell/rc.sh carries the same XDG snippet for interactive shells.
// Every piece is a no-op on a machine without the volume.

// SCScratchUnitName is the systemd unit every machine carries.
const SCScratchUnitName = "sc-scratch.service"

const (
	scScratchSetupPath   = "/usr/local/sbin/sc-scratch-setup"
	scScratchUnitPath    = "/etc/systemd/system/" + SCScratchUnitName
	scScratchProfilePath = "/etc/profile.d/sc-scratch.sh"
)

// scScratchSetupScript is baked, not a payload script: on a VM the payload is
// itself a virtiofs share the Incus agent mounts too late for this to run
// from it. A VM's scratch share is mounted here for the same reason; the
// agent then finds /scratch mounted and leaves it alone.
//
// --restart-private-tmp is the first-boot mode. cloud-init runs after
// sysinit, so services with PrivateTmp=yes already hold a private dir in the
// old /tmp that the bind hides; they are restarted when something was bound.
// The SC_SCRATCH_* overrides exist only for the test suite.
const scScratchSetupScript = `#!/bin/sh
set -u
S="${SC_SCRATCH_ROOT:-/scratch}"
if ! mountpoint -q "$S"; then
  systemd-detect-virt --vm --quiet 2>/dev/null || exit 0
  mkdir -p "$S" && mount -t virtiofs incus_scratch "$S" 2>/dev/null || exit 0
fi
mkdir -p "$S/tmp" "$S/var-tmp" "$S/cache" && chmod 1777 "$S/tmp" "$S/var-tmp" "$S/cache" || exit 1
rc=0
bound=0
for pair in "tmp:${SC_SCRATCH_TMP:-/tmp}" "var-tmp:${SC_SCRATCH_VAR_TMP:-/var/tmp}"; do
  src="$S/${pair%%:*}"; dst="${pair#*:}"
  [ "$(stat -L -c %d:%i "$src")" = "$(stat -L -c %d:%i "$dst" 2>/dev/null)" ] && continue
  if mount --bind "$src" "$dst"; then
    echo "sc-scratch: $dst is now $src"
    bound=1
  else
    rc=1
  fi
done
if [ "$bound" = 1 ] && [ "${1:-}" = --restart-private-tmp ]; then
  systemctl list-units --type=service --state=active --no-legend --plain | while read -r unit _; do
    [ "$(systemctl show -p PrivateTmp --value "$unit")" = yes ] || continue
    echo "sc-scratch: restarting $unit (PrivateTmp)"
    systemctl try-restart "$unit"
  done
fi
exit $rc
`

// scScratchUnit runs before sysinit.target and systemd-tmpfiles-setup, so no
// PrivateTmp= service (hostnamed, logind, resolved) has created its private
// dir in the /tmp the bind would hide. Run later, those services fail with
// 226/NAMESPACE.
const scScratchUnit = `[Unit]
Description=Sandcastle scratch: /tmp and /var/tmp on the machine's /scratch volume
DefaultDependencies=no
After=local-fs.target tmp.mount var-tmp.mount
Before=sysinit.target systemd-tmpfiles-setup.service shutdown.target
Conflicts=shutdown.target
ConditionPathExists=` + scScratchSetupPath + `

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=` + scScratchSetupPath + `
TimeoutStartSec=30

[Install]
WantedBy=sysinit.target
`

// scratchCacheSnippet is both /etc/profile.d/sc-scratch.sh (login shells) and
// part of the payload's shell/rc.sh (interactive shells); whichever runs
// second sees XDG_CACHE_HOME set and leaves it. A cache directory someone
// else created is not adopted: -O requires the user to own it.
const scratchCacheSnippet = `# Sandcastle scratch: keep the XDG cache (Go build cache, uv, pip,
# huggingface) on the machine's scratch volume, never snapshotted or backed up.
if [ -z "${XDG_CACHE_HOME:-}" ] && [ -d /scratch/cache ]; then
  __sc_cache="/scratch/cache/${USER:-$(id -un 2>/dev/null)}"
  if mkdir -p -m 700 "$__sc_cache" 2>/dev/null && [ -O "$__sc_cache" ]; then
    export XDG_CACHE_HOME="$__sc_cache"
  fi
  unset __sc_cache
fi
`

// scratchGuestFiles is every file the guest side bakes. cloud-init, the
// `sc fix` backfill and its check all render from this one list.
var scratchGuestFiles = []struct {
	path, mode, content string
}{
	{scScratchSetupPath, "0755", scScratchSetupScript},
	{scScratchUnitPath, "0644", scScratchUnit},
	{scScratchProfilePath, "0644", scratchCacheSnippet},
}

const scratchHeredocEOF = "SANDCASTLE_SCRATCH_EOF"

// scScratchWriteFiles is the cloud-init write_files fragment (entries only).
var scScratchWriteFiles = func() string {
	var b strings.Builder
	for _, f := range scratchGuestFiles {
		b.WriteString("  - path: " + f.path + "\n    permissions: '" + f.mode + "'\n    content: |\n" + indentBlock(f.content, 6))
	}
	return b.String()
}()

// scScratchRuncmd must be the LAST runcmd entries: every service the earlier
// entries start (caddy) is then running and gets restarted onto the new /tmp
// if it uses PrivateTmp. No user session exists yet on first boot.
const scScratchRuncmd = "  - [systemctl, enable, " + SCScratchUnitName + "]\n" +
	"  - [" + scScratchSetupPath + ", --restart-private-tmp]\n"

// ScratchBackfillScript is the `sc fix` counterpart of the cloud-init
// fragments. It enables the unit without starting it: binding /tmp under a
// running machine would hide the sockets of live tmux or herdr sessions.
func ScratchBackfillScript() string {
	var b strings.Builder
	b.WriteString("set -eu\n")
	for _, f := range scratchGuestFiles {
		b.WriteString("cat > " + f.path + " <<'" + scratchHeredocEOF + "'\n" + f.content + scratchHeredocEOF + "\nchmod " + f.mode + " " + f.path + "\n")
	}
	b.WriteString(`systemctl daemon-reload
systemctl enable --quiet ` + SCScratchUnitName + `
if mountpoint -q ` + ScratchMountPath + `; then
  echo "scratch: unit enabled; /tmp and /var/tmp move onto ` + ScratchMountPath + ` at the next restart"
else
  echo "scratch: unit enabled; it applies at the next restart once the machine has a ` + ScratchMountPath + ` volume (sc-adm tenant scratch-migrate)"
fi
`)
	return b.String()
}

// ScratchCheckScript reports whether every file is installed current and the
// unit is enabled.
func ScratchCheckScript() string {
	var b strings.Builder
	b.WriteString("set -u\nok=1\n")
	for _, f := range scratchGuestFiles {
		b.WriteString("cmp -s " + f.path + " - <<'" + scratchHeredocEOF + "' || ok=0\n" + f.content + scratchHeredocEOF + "\n")
		b.WriteString("[ \"$(stat -c %a " + f.path + " 2>/dev/null)\" = " + strings.TrimPrefix(f.mode, "0") + " ] || ok=0\n")
	}
	b.WriteString(`systemctl is-enabled --quiet ` + SCScratchUnitName + ` 2>/dev/null || ok=0
if [ "$ok" = 1 ]; then
  echo "scratch: OK"
else
  echo "scratch: NEEDS FIX"
fi
`)
	return b.String()
}
