package tenant

// The guest half of the Machine Scratch Volume. A stable unit baked by
// cloud-init runs the platform payload's scratch/setup.sh at boot, which
// bind-mounts /scratch/tmp onto /tmp and /scratch/var-tmp onto /var/tmp. The
// shell rc points XDG_CACHE_HOME at /scratch/cache/<user>. Every piece is a
// no-op on a machine without the volume.

// SCPayloadScratchSetupPath is the payload script the unit runs.
const SCPayloadScratchSetupPath = "scratch/setup.sh"

// SCScratchUnitName is the systemd unit every machine carries.
const SCScratchUnitName = "sc-scratch.service"

const scScratchUnitPath = "/etc/systemd/system/" + SCScratchUnitName

// scScratchUnit runs the setup after the Incus agent (a VM's virtiofs shares
// are mounted before it reports ready) and before user sessions, so no login
// sees /tmp switch underneath it. ConditionPathExists skips the unit cleanly,
// never as a failure, while the project's payload predates the script.
var scScratchUnit = `[Unit]
Description=Sandcastle scratch: /tmp and /var/tmp on the machine's /scratch volume
After=local-fs.target incus-agent.service
Before=systemd-user-sessions.service multi-user.target
ConditionPathExists=` + SCPlatformPath + "/" + SCPayloadScratchSetupPath + `

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh ` + SCPlatformPath + "/" + SCPayloadScratchSetupPath + `
TimeoutStartSec=120

[Install]
WantedBy=multi-user.target
`

// scratchSetupScript is /.sc/platform/scratch/setup.sh. The SC_SCRATCH_*
// overrides exist only so the test suite can run it against a temp dir.
const scratchSetupScript = `#!/bin/sh
# Sandcastle: put /tmp and /var/tmp on this machine's scratch volume
# (/scratch, never snapshotted or backed up). Run at boot by sc-scratch.service.
# Idempotent; a machine without the volume is left as it is.
set -u
SCRATCH="${SC_SCRATCH_ROOT:-/scratch}"
AGENT_MOUNTS="${SC_SCRATCH_AGENT_MOUNTS:-/run/incus_agent/agent-mounts.json}"
i=0
until mountpoint -q "$SCRATCH"; do
  # A container's disks are mounted before init starts, and a VM's agent
  # lists every share it mounts: either way a missing /scratch is final.
  if systemd-detect-virt --container --quiet 2>/dev/null; then exit 0; fi
  if [ -r "$AGENT_MOUNTS" ] && ! grep -q "\"$SCRATCH\"" "$AGENT_MOUNTS"; then exit 0; fi
  [ "$i" -ge 60 ] && exit 0
  i=$((i + 1))
  sleep 1
done
mkdir -p "$SCRATCH/tmp" "$SCRATCH/var-tmp" "$SCRATCH/cache" || exit 1
# cache is sticky and world-writable like /tmp: each user creates their own
# 0700 directory in it (shell/rc.sh), which a root-owned 0755 dir would refuse.
chmod 1777 "$SCRATCH/tmp" "$SCRATCH/var-tmp" "$SCRATCH/cache" || exit 1
status=0
bound=0
bind_scratch() {
  src="$SCRATCH/$1"
  dst="$2"
  if [ "$(stat -L -c %d:%i "$src")" = "$(stat -L -c %d:%i "$dst" 2>/dev/null)" ]; then
    return 0
  fi
  mkdir -p "$dst" && mount --bind "$src" "$dst" || return 1
  echo "sc-scratch: $dst is now $src"
  bound=1
}
bind_scratch tmp "${SC_SCRATCH_TMP:-/tmp}" || status=1
bind_scratch var-tmp "${SC_SCRATCH_VAR_TMP:-/var/tmp}" || status=1
# systemd-tmpfiles populated the old /tmp at sysinit; recreate its entries.
if [ "$bound" = 1 ] && command -v systemd-tmpfiles >/dev/null 2>&1; then
  systemd-tmpfiles --create --prefix=/tmp --prefix=/var/tmp >/dev/null 2>&1 || true
fi
exit "$status"
`

// scratchCacheSnippet is appended to shell/rc.sh. A cache directory someone
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

// scScratchWriteFiles / scScratchRuncmd are the cloud-init fragments (entries
// only) that bake and enable the unit on a fresh machine.
var (
	scScratchWriteFiles = "  - path: " + scScratchUnitPath + "\n    permissions: '0644'\n    content: |\n" + indentBlock(scScratchUnit, 6)
	scScratchRuncmd     = "  - [systemctl, enable, --now, " + SCScratchUnitName + "]\n"
)

// ScratchBackfillScript is the `sc fix` counterpart of the cloud-init
// fragments. It enables the unit without starting it: binding /tmp under a
// running machine would hide the sockets of live tmux or herdr sessions.
func ScratchBackfillScript() string {
	return `set -eu
cat > ` + scScratchUnitPath + ` <<'SANDCASTLE_SCRATCH_UNIT_EOF'
` + scScratchUnit + `SANDCASTLE_SCRATCH_UNIT_EOF
chmod 0644 ` + scScratchUnitPath + `
systemctl daemon-reload
systemctl enable --quiet ` + SCScratchUnitName + `
if mountpoint -q ` + ScratchMountPath + `; then
  echo "scratch: unit enabled; /tmp and /var/tmp move onto ` + ScratchMountPath + ` at the next restart (now: sudo systemctl start ` + SCScratchUnitName + `)"
else
  echo "scratch: unit enabled; no ` + ScratchMountPath + ` volume attached, so it stays a no-op"
fi
`
}

// ScratchCheckScript reports whether the unit is installed current and enabled.
func ScratchCheckScript() string {
	return `set -u
if cmp -s ` + scScratchUnitPath + ` - <<'SANDCASTLE_SCRATCH_UNIT_EOF' && systemctl is-enabled --quiet ` + SCScratchUnitName + ` 2>/dev/null; then
` + scScratchUnit + `SANDCASTLE_SCRATCH_UNIT_EOF
  echo "scratch: OK"
else
  echo "scratch: NEEDS FIX"
fi
`
}
