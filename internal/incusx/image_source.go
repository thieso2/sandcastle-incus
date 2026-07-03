package incusx

import (
	"strings"

	"github.com/lxc/incus/v6/shared/api"
)

// publicImageRemotes maps Incus's well-known public image remotes to their
// simplestreams servers. This lets a base-image reference like
// "images:debian/13" be pulled directly from the public remote — no bespoke
// sandcastle/base image and no manual `incus image copy` pre-caching needed.
var publicImageRemotes = map[string]string{
	"images":         "https://images.linuxcontainers.org",
	"ubuntu":         "https://cloud-images.ubuntu.com/releases",
	"ubuntu-daily":   "https://cloud-images.ubuntu.com/daily",
	"ubuntu-minimal": "https://cloud-images.ubuntu.com/minimal/releases",
}

// imageInstanceSource builds an InstanceSource for an appliance/sidecar base
// image reference, accepting three forms:
//
//   - "<remote>:<alias>" for a known public remote (e.g. "images:debian/13") —
//     pulled from that remote's simplestreams server;
//   - a hex fingerprint — matched against the local image store;
//   - a bare alias — matched against the local image store.
//
// The remote-pull form is what makes stock images work out of the box: the
// three system-container launch sites (auth-app, broker, tenant sidecar) all run
// standard Debian with the fat sandcastle binary pushed in, so no custom image
// needs to be built or cached first.
func imageInstanceSource(ref string) api.InstanceSource {
	ref = strings.TrimSpace(ref)
	if remote, alias, ok := strings.Cut(ref, ":"); ok {
		if server, known := publicImageRemotes[remote]; known && strings.TrimSpace(alias) != "" {
			return api.InstanceSource{
				Type:     "image",
				Mode:     "pull",
				Server:   server,
				Protocol: "simplestreams",
				Alias:    alias,
			}
		}
	}
	if looksLikeFingerprint(ref) {
		return api.InstanceSource{Type: "image", Fingerprint: ref}
	}
	return api.InstanceSource{Type: "image", Alias: ref}
}
