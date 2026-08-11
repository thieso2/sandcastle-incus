package incusx

import (
	"sort"
	"strings"

	"github.com/lxc/incus/v6/shared/cliconfig"
)

// FindRemoteHostingInstall returns the name of the enrolled admin Incus remote
// that hosts the Sandcastle install named by prefix — the remote whose project
// list contains "<prefix>-infra", the project every install puts its auth-app
// appliance in (see admin_install.go).
//
// It exists because the user plane and the admin plane identify a deployment
// by different things and, since v2, cannot be matched by inspecting the
// connection: a user's Incus remote points at their own tenant SIDECAR on the
// tailnet (ADR-0017), not at the Incus host, so neither the server certificate
// nor the resolved address of "obelix" has anything in common with the admin
// remote ("big") that actually runs it. The install name is the one identifier
// both planes share, so ask each remote which installs it hosts instead of
// trying to correlate transport details.
//
// Remotes are tried default-first, then alphabetically, so the answer is
// deterministic; unreachable or unauthorized remotes are skipped rather than
// failing the search (connectConfiguredRemote already bounds the dial with
// remoteDialTimeout). Returns "" when no enrolled remote hosts the install.
func FindRemoteHostingInstall(prefix string, log func(string)) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return ""
	}
	loaded, err := LoadCLIConfig("")
	if err != nil {
		logLine(log, "install lookup: cannot load global incus config: "+err.Error())
		return ""
	}
	infraProject := prefix + "-infra"
	for _, remote := range orderedRemoteNames(loaded.DefaultRemote, remoteNames(loaded)) {
		server, err := connectConfiguredRemote(log, "", remote)
		if err != nil {
			logLine(log, "install lookup: skipping remote "+remote+": "+err.Error())
			continue
		}
		names, err := server.GetProjectNames()
		if err != nil {
			logLine(log, "install lookup: skipping remote "+remote+": "+err.Error())
			continue
		}
		for _, name := range names {
			if name == infraProject {
				return remote
			}
		}
	}
	return ""
}

// remoteNames lists the remotes worth asking about an install: image servers
// (public, or any non-incus protocol such as simplestreams) host no projects,
// so they are dropped rather than dialed.
func remoteNames(loaded *cliconfig.Config) []string {
	names := make([]string, 0, len(loaded.Remotes))
	for name, remote := range loaded.Remotes {
		if remote.Public || (remote.Protocol != "" && remote.Protocol != "incus") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// logLine emits one verbose line in the same shape logIncusAPICall uses — the
// sink writes messages verbatim, so the prefix and newline belong here.
func logLine(log func(string), message string) {
	if log != nil {
		log("[verbose] " + message + "\n")
	}
}

// orderedRemoteNames puts the default remote first — the common case is that
// the operator is already pointed at the right host, and finding it first
// avoids dialing anything else — then the rest alphabetically for a stable,
// reproducible search order.
func orderedRemoteNames(defaultRemote string, names []string) []string {
	sort.Strings(names)
	if defaultRemote == "" {
		return names
	}
	ordered := make([]string, 0, len(names))
	found := false
	for _, name := range names {
		if name == defaultRemote {
			found = true
			continue
		}
		ordered = append(ordered, name)
	}
	if !found {
		return names
	}
	return append([]string{defaultRemote}, ordered...)
}
