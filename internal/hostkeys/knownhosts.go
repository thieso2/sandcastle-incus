package hostkeys

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

// A known_hosts line is:
//
//	[marker] hostnames keytype base64-key [comment]
//
// hostnames is either a comma-separated pattern list or a hashed host of the
// form |1|<base64 salt>|<base64 HMAC-SHA1(salt, host)>. Lines carrying an
// @cert-authority or @revoked marker are load-bearing security state we never
// authored, so we pass them through untouched.

const (
	// tagPrefix marks the lines Sandcastle owns. Everything else in the file
	// belongs to the user and is only ever removed under the narrow rules in
	// reconcile — never because it merely looks like ours.
	tagPrefix = "sandcastle:"
	// tofuMarker records that a tagged key was learned by ssh-keyscan rather
	// than read authoritatively from the machine. Any later connect that can
	// reach the Incus API upgrades the line and drops the marker.
	tofuMarker = "tofu"
)

// Key is an SSH host public key: its algorithm and its base64 blob, with the
// trailing comment stripped.
type Key struct {
	Type string
	Key  string
}

func (k Key) valid() bool { return k.Type != "" && k.Key != "" }

// Fingerprint renders the key the way OpenSSH does, so messages we print can
// be compared directly against what ssh reports on a mismatch.
func (k Key) Fingerprint() string {
	blob, err := base64.StdEncoding.DecodeString(k.Key)
	if err != nil {
		return "SHA256:?"
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// Tag identifies the install and tenant a Sandcastle-owned line belongs to, so
// purge can scope itself to one tenant on a machine that talks to several.
type Tag struct {
	Remote string
	Tenant string
	TOFU   bool
}

func (t Tag) sameTenant(other Tag) bool {
	return t.Remote == other.Remote && t.Tenant == other.Tenant
}

func (t Tag) render() string {
	out := "# " + tagPrefix + t.Remote + "/" + t.Tenant
	if t.TOFU {
		out += " " + tofuMarker
	}
	return out
}

type entryKind int

const (
	// kindOpaque covers blank lines, comments, @markers, and anything we fail
	// to parse. We copy these through verbatim and never match against them.
	kindOpaque entryKind = iota
	kindPlain
	kindHashed
)

type entry struct {
	raw      string
	kind     entryKind
	hosts    []string // kindPlain
	salt     []byte   // kindHashed
	hash     []byte   // kindHashed
	keyType  string
	key      string
	trailing string // everything after the key: our tag, or the user's comment
	tag      *Tag
}

func (e entry) key0() Key { return Key{Type: e.keyType, Key: e.key} }

func parseEntry(line string) entry {
	opaque := entry{raw: line}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "@") {
		return opaque
	}
	fields := strings.Fields(trimmed)
	if len(fields) < 3 {
		return opaque
	}
	parsed := entry{
		raw:      line,
		keyType:  fields[1],
		key:      fields[2],
		trailing: strings.Join(fields[3:], " "),
		tag:      parseTag(fields[3:]),
	}
	if strings.HasPrefix(fields[0], "|1|") {
		salt, hash, ok := parseHashedHost(fields[0])
		if !ok {
			return opaque
		}
		parsed.kind, parsed.salt, parsed.hash = kindHashed, salt, hash
		return parsed
	}
	parsed.kind = kindPlain
	parsed.hosts = strings.Split(fields[0], ",")
	return parsed
}

func parseTag(rest []string) *Tag {
	for index, field := range rest {
		if !strings.HasPrefix(field, tagPrefix) {
			continue
		}
		remote, tenant, ok := strings.Cut(strings.TrimPrefix(field, tagPrefix), "/")
		if !ok || remote == "" || tenant == "" {
			return nil
		}
		tag := Tag{Remote: remote, Tenant: tenant}
		for _, trailing := range rest[index+1:] {
			if trailing == tofuMarker {
				tag.TOFU = true
			}
		}
		return &tag
	}
	return nil
}

func parseHashedHost(field string) (salt []byte, hash []byte, ok bool) {
	parts := strings.Split(field, "|")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "1" {
		return nil, nil, false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, nil, false
	}
	hash, err = base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return nil, nil, false
	}
	return salt, hash, true
}

// matchesHost reports whether this line is an entry *for* host. Hashed lines
// can only ever be tested against a host we already know to ask about, which
// is why purge can find hashed IP entries (the tenant CIDR is enumerable) but
// never hashed name entries.
func (e entry) matchesHost(host string) bool {
	switch e.kind {
	case kindPlain:
		for _, candidate := range e.hosts {
			if candidate == host {
				return true
			}
		}
	case kindHashed:
		mac := hmac.New(sha1.New, e.salt)
		mac.Write([]byte(host))
		return hmac.Equal(mac.Sum(nil), e.hash)
	}
	return false
}

// shadows reports whether a wildcard pattern on this line would make OpenSSH
// consult it *before* the line we are about to write. We never delete these —
// they are the user's — but a shadowing wildcard makes StrictHostKeyChecking=yes
// fail in a way that is otherwise impossible to diagnose, so we warn.
func (e entry) shadows(host string) bool {
	if e.kind != kindPlain {
		return false
	}
	for _, pattern := range e.hosts {
		if strings.ContainsAny(pattern, "*?") && matchPattern(pattern, host) {
			return true
		}
	}
	return false
}

// matchPattern implements OpenSSH's glob subset: '*' spans any run of
// characters, '?' exactly one.
func matchPattern(pattern string, host string) bool {
	if pattern == "" {
		return host == ""
	}
	switch pattern[0] {
	case '*':
		for index := 0; index <= len(host); index++ {
			if matchPattern(pattern[1:], host[index:]) {
				return true
			}
		}
		return false
	case '?':
		if host == "" {
			return false
		}
		return matchPattern(pattern[1:], host[1:])
	default:
		if host == "" || host[0] != pattern[0] {
			return false
		}
		return matchPattern(pattern[1:], host[1:])
	}
}

// withoutHosts drops the named hosts from this line. A plain line listing other
// hosts survives with those intact; a line left with no hosts disappears, as
// does any hashed line that matched (a hashed line names exactly one host).
func (e entry) withoutHosts(hosts map[string]bool) (entry, bool) {
	if e.kind == kindHashed {
		for host := range hosts {
			if e.matchesHost(host) {
				return entry{}, false
			}
		}
		return e, true
	}
	var kept []string
	for _, candidate := range e.hosts {
		if !hosts[candidate] {
			kept = append(kept, candidate)
		}
	}
	if len(kept) == 0 {
		return entry{}, false
	}
	if len(kept) == len(e.hosts) {
		return e, true
	}
	rest := ""
	if e.trailing != "" {
		rest = " " + e.trailing
	}
	return parseEntry(strings.Join(kept, ",") + " " + e.keyType + " " + e.key + rest), true
}

// renderLine writes the canonical Sandcastle-owned line: every name the machine
// answers at, one authoritative key, and the tag that makes it ours to reclaim.
func renderLine(names []string, key Key, tag Tag) string {
	return fmt.Sprintf("%s %s %s %s", strings.Join(names, ","), key.Type, key.Key, tag.render())
}
