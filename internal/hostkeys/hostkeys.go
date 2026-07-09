// Package hostkeys keeps ~/.ssh/known_hosts truthful about Sandcastle machines.
//
// Two facts shape everything here. First, a machine's SSH host key can be read
// authoritatively over the mTLS Incus API, so it never has to be trusted on
// first use. Second, machines are addressed by stable names — the Machine
// Private Hostname and its short alias — while their private IPs are recycled
// DHCP leases. Keying entries by name (via ssh's HostKeyAlias) and writing the
// authoritative key means connect can run StrictHostKeyChecking=yes: a real
// impostor fails, and a rebuilt machine never does.
//
// Every line this package writes carries a `# sandcastle:<remote>/<tenant>`
// tag. That tag is what makes deletion safe: we reclaim names we own and drop
// entries we wrote, and we never touch a line the user put there, with one
// bounded exception — literal IP addresses inside the tenant's own private
// CIDR, which no longer have any legitimate owner once connect stops keying
// entries by IP.
package hostkeys

import (
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// minPurgePrefixBits bounds automatic CIDR purging. A tenant CIDR is a /24 by
// default, but the pool is operator-configurable, and a misconfigured install
// pointing at, say, 192.168.0.0/16 must not silently delete a user's home-LAN
// entries. Anything larger than 1022 addresses is reported, never purged.
const minPurgePrefixBits = 22

// Machine is one machine's desired state: every name it answers at, and every
// host key those names should map to.
//
// All of the machine's keys are recorded, not just the strongest. OpenSSH's
// UpdateHostKeys appends a server's other host keys to known_hosts after a
// successful authentication, so a file holding only the ed25519 key would grow
// an untagged rsa line on the next bare `ssh` — which the next connect would
// reclaim, and ssh would add back. Recording them all makes the file converge.
//
// A Machine with no Keys is live but unreadable — a VM whose incus-agent is
// down, say. Its names stay claimed so Purge does not mistake its lines for
// orphans, but nothing about it is rewritten.
type Machine struct {
	Names []string
	Keys  []Key
	TOFU  bool
}

func (m Machine) known() bool {
	for _, key := range m.Keys {
		if key.valid() {
			return true
		}
	}
	return false
}

type Action string

const (
	ActionAdd    Action = "add"
	ActionUpdate Action = "update"
	ActionRemove Action = "remove"
)

// Change is one line-level edit, phrased for a human reading `sc ssh-key purge`.
type Change struct {
	Action Action
	Host   string
	Detail string
}

func (c Change) String() string {
	return fmt.Sprintf("%-6s %-34s %s", c.Action, c.Host, c.Detail)
}

// Config binds the package to one tenant's view of one known_hosts file.
type Config struct {
	Path   string
	Remote string
	Tenant string
	CIDR   netip.Prefix // tenant PrivateCIDR; invalid disables IP purging
	Stderr io.Writer
	Now    func() time.Time
}

func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c Config) tag(tofu bool) Tag {
	return Tag{Remote: c.Remote, Tenant: c.Tenant, TOFU: tofu}
}

// DefaultPath is the file both `sc` and a bare `ssh` consult.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

// Plan is a computed rewrite of known_hosts: the resulting lines, plus what
// changed and why. It also keeps the inputs, because Apply recomputes under the
// file lock (see Apply).
type Plan struct {
	config             Config
	machines           []Machine
	purgeTaggedOrphans bool
	lines              []string
	Changes            []Change
	Warnings           []string
	PurgeSkipped       string
}

func (p Plan) Empty() bool { return len(p.Changes) == 0 }

func (p Plan) destructive() bool {
	for _, change := range p.Changes {
		if change.Action != ActionAdd {
			return true
		}
	}
	return false
}

// Apply writes the plan, snapshotting the file first if anything is being
// removed or rewritten.
//
// The plan is recomputed against the file as it stands under the lock, not
// replayed from the snapshot taken when it was displayed. Another `sc` process
// — or a bare `ssh` — may have appended entries in between, and writing back a
// stale line set would silently drop them.
func (p Plan) Apply() error {
	if p.Empty() {
		return nil
	}
	return withLock(p.config.Path, func() error {
		fresh, err := p.config.plan(p.machines, p.purgeTaggedOrphans)
		if err != nil {
			return err
		}
		if fresh.Empty() {
			return nil
		}
		if fresh.destructive() {
			backup, err := ensureBackup(p.config.Path, p.config.now())
			if err != nil {
				return err
			}
			if backup != "" && p.config.Stderr != nil {
				fmt.Fprintf(p.config.Stderr, "backup: %s\n", backup)
			}
		}
		return writeLines(p.config.Path, fresh.lines)
	})
}

// Ensure reconciles a single machine on the connect path: it claims the names
// the machine answers at, purges recycled-IP debris in the tenant CIDR, and
// writes the authoritative line. It is idempotent — a machine already recorded
// correctly produces no changes and no write.
func (c Config) Ensure(machine Machine) (Plan, error) {
	return c.plan([]Machine{machine}, false)
}

// Purge reconciles every live machine in the tenant and additionally removes
// tagged lines whose machine no longer exists. Only Purge can do that: knowing
// a line is orphaned requires the full live-machine list, which connect does
// not have.
func (c Config) Purge(machines []Machine) (Plan, error) {
	return c.plan(machines, true)
}

func (c Config) plan(machines []Machine, purgeTaggedOrphans bool) (Plan, error) {
	plan := Plan{config: c, machines: machines, purgeTaggedOrphans: purgeTaggedOrphans}
	existing, err := readLines(c.Path)
	if err != nil {
		return Plan{}, fmt.Errorf("read %s: %w", c.Path, err)
	}

	own := c.tag(false)
	desired := make([][]string, len(machines)) // one rendered line per key
	claimed := map[string]int{}                // host -> machine index
	for index, machine := range machines {
		for _, key := range machine.Keys {
			if key.valid() {
				desired[index] = append(desired[index], renderLine(machine.Names, key, c.tag(machine.TOFU)))
			}
		}
		for _, name := range machine.Names {
			claimed[name] = index
		}
	}

	purgeAddrs, skipped := c.purgeAddrs()
	plan.PurgeSkipped = skipped

	satisfied := make([]map[string]bool, len(machines))
	for index := range satisfied {
		satisfied[index] = map[string]bool{}
	}
	replaced := make([]bool, len(machines))
	warned := map[string]bool{}

	for _, line := range existing {
		parsed := parseEntry(line)
		if parsed.kind == kindOpaque {
			plan.lines = append(plan.lines, line)
			continue
		}
		for _, machine := range machines {
			for _, name := range machine.Names {
				if parsed.shadows(name) && !warned[name] {
					warned[name] = true
					plan.Warnings = append(plan.Warnings, fmt.Sprintf(
						"%s matches a wildcard entry in %s; ssh will prefer that key over ours",
						name, c.Path))
				}
			}
		}

		// A name we own. Either this is exactly the line we want (keep it and
		// write nothing), or it is stale and we reclaim the name.
		if hosts, index, ok := matchedClaims(parsed, claimed); ok {
			// Live but unreadable: leave whatever is recorded alone rather
			// than delete a line we cannot replace.
			if !machines[index].known() {
				plan.lines = append(plan.lines, line)
				continue
			}
			trimmed := strings.TrimSpace(line)
			if parsed.tag != nil && parsed.tag.sameTenant(own) && contains(desired[index], trimmed) && !satisfied[index][trimmed] {
				satisfied[index][trimmed] = true
				plan.lines = append(plan.lines, line)
				continue
			}
			replaced[index] = true
			kept, alive := parsed.withoutHosts(hosts)
			for host := range hosts {
				plan.Changes = append(plan.Changes, Change{
					Action: ActionUpdate, Host: host,
					Detail: "was " + parsed.keyType + " " + parsed.key0().Fingerprint(),
				})
			}
			if alive {
				plan.lines = append(plan.lines, kept.raw)
			}
			continue
		}

		// A line we wrote for a machine that no longer exists.
		if parsed.tag != nil && parsed.tag.sameTenant(own) {
			if purgeTaggedOrphans {
				plan.Changes = append(plan.Changes, Change{
					Action: ActionRemove, Host: hostLabel(parsed),
					Detail: "tagged, machine gone",
				})
				continue
			}
			plan.lines = append(plan.lines, line)
			continue
		}

		// Recycled-IP debris: a literal address inside the tenant's private
		// CIDR. Nothing writes IP-keyed entries any more, so these are stale
		// by construction.
		if len(purgeAddrs) > 0 && (parsed.tag == nil || !parsed.tag.sameTenant(own)) {
			if hosts := matchedAddrs(parsed, purgeAddrs); len(hosts) > 0 {
				kept, alive := parsed.withoutHosts(hosts)
				for host := range hosts {
					plan.Changes = append(plan.Changes, Change{
						Action: ActionRemove, Host: host,
						Detail: "untagged, inside " + c.CIDR.String(),
					})
				}
				if alive {
					plan.lines = append(plan.lines, kept.raw)
				}
				continue
			}
		}

		plan.lines = append(plan.lines, line)
	}

	for index, machine := range machines {
		if !machine.known() {
			continue
		}
		for lineIndex, line := range desired[index] {
			if satisfied[index][line] {
				continue
			}
			plan.lines = append(plan.lines, line)
			if !replaced[index] {
				key := machine.Keys[lineIndex]
				plan.Changes = append(plan.Changes, Change{
					Action: ActionAdd, Host: machine.Names[0],
					Detail: key.Type + " " + key.Fingerprint() + tofuSuffix(machine.TOFU),
				})
			}
		}
	}
	sortChanges(plan.Changes)
	return plan, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func tofuSuffix(tofu bool) string {
	if tofu {
		return " (trust on first use)"
	}
	return ""
}

// purgeAddrs enumerates the tenant CIDR so both plain and hashed entries can be
// tested against it. An absent or implausibly large CIDR disables purging and
// explains itself rather than silently doing nothing.
func (c Config) purgeAddrs() ([]string, string) {
	if !c.CIDR.IsValid() || !c.CIDR.Addr().Is4() {
		return nil, ""
	}
	if c.CIDR.Bits() < minPurgePrefixBits {
		return nil, fmt.Sprintf(
			"tenant CIDR %s is larger than /%d; skipping IP purge (run `sc ssh-key purge` to review)",
			c.CIDR, minPurgePrefixBits)
	}
	var addrs []string
	for addr := c.CIDR.Masked().Addr(); c.CIDR.Contains(addr); addr = addr.Next() {
		addrs = append(addrs, addr.String())
	}
	return addrs, ""
}

// matchedClaims returns the owned hosts this entry claims, and which machine
// they belong to.
func matchedClaims(parsed entry, claimed map[string]int) (map[string]bool, int, bool) {
	hosts := map[string]bool{}
	index := -1
	for host, machineIndex := range claimed {
		if parsed.matchesHost(host) {
			hosts[host] = true
			if index < 0 || machineIndex < index {
				index = machineIndex
			}
		}
	}
	if index < 0 {
		return nil, 0, false
	}
	return hosts, index, true
}

func matchedAddrs(parsed entry, addrs []string) map[string]bool {
	hosts := map[string]bool{}
	for _, addr := range addrs {
		if parsed.matchesHost(addr) {
			hosts[addr] = true
		}
	}
	return hosts
}

// hostLabel names an entry for reporting. Hashed entries cannot be reversed, so
// they are described rather than named.
func hostLabel(parsed entry) string {
	if parsed.kind == kindHashed {
		return "(hashed entry)"
	}
	return strings.Join(parsed.hosts, ",")
}

func sortChanges(changes []Change) {
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].Action != changes[j].Action {
			return changes[i].Action < changes[j].Action
		}
		return changes[i].Host < changes[j].Host
	})
}
