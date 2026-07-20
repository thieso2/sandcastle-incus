package incusx

import (
	"sort"
	"strings"

	"github.com/lxc/incus/v6/shared/api"
)

// Picking a machine's address from live Incus state used to mean "first
// non-loopback global IPv4 in instance.State.Network". That map is a Go map, so
// iteration order is randomized: on a machine running Docker the docker0 bridge
// (172.17.0.1 — container-local, identical on every machine, routes nowhere)
// won the race about as often as the real NIC, and the reported address flapped
// between successive calls.
//
// The fix is to consider only the instance's Incus-managed NIC devices.
// In-guest bridges (docker0, br-*, veth*) never appear in ExpandedDevices, so
// they are excluded structurally rather than by a blocklist that would need
// updating for every new container runtime.
//
// Note this deliberately does not filter by the tenant CIDR the way
// instanceTenantIPv4 (dns_v2.go) does: that CIDR lives on the infra project and
// is unreadable from a tenant certificate, so a CIDR filter would blank the
// address for user-facing CLI callers instead of correcting it.

// nicAddress is one Incus-managed NIC's live global IPv4.
type nicAddress struct {
	Address string
	Netmask string
}

// instanceNICIPv4 returns the global IPv4 of the instance's first Incus-managed
// NIC device, in sorted device order. Returns the zero value when the instance
// has no NIC device, or none has a global IPv4 lease yet.
//
// config carries the instance's expanded config (for volatile.<device>.hwaddr),
// devices its expanded devices, and network the live per-interface state.
func instanceNICIPv4(config map[string]string, devices map[string]map[string]string, network map[string]api.InstanceStateNetwork) nicAddress {
	deviceNames := make([]string, 0, len(devices))
	for name, device := range devices {
		if device["type"] == "nic" {
			deviceNames = append(deviceNames, name)
		}
	}
	sort.Strings(deviceNames)

	for _, device := range deviceNames {
		iface, ok := guestInterface(config, device, devices[device], network)
		if !ok || iface.Type == "loopback" {
			continue
		}
		for _, address := range iface.Addresses {
			if address.Family == "inet" && address.Scope == "global" {
				return nicAddress{Address: address.Address, Netmask: address.Netmask}
			}
		}
	}
	return nicAddress{}
}

// guestInterface resolves an Incus NIC device to its live guest-side interface.
//
// Matching by MAC comes first because it survives interface renaming: in a
// container Incus names the veth to match the device, but in a VM the guest
// names its own interfaces (enp5s0, ens3), so the device key "eth0" will not be
// a key in the state map. Name matching is the fallback for the case where no
// hwaddr is recorded.
func guestInterface(config map[string]string, device string, deviceConfig map[string]string, network map[string]api.InstanceStateNetwork) (api.InstanceStateNetwork, bool) {
	hwaddr := deviceConfig["hwaddr"]
	if hwaddr == "" {
		hwaddr = config["volatile."+device+".hwaddr"]
	}
	if hwaddr != "" {
		names := make([]string, 0, len(network))
		for name := range network {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if strings.EqualFold(network[name].Hwaddr, hwaddr) {
				return network[name], true
			}
		}
	}

	// A NIC's guest-side name is its "name" property; Incus defaults it to the
	// device key when unset.
	name := deviceConfig["name"]
	if name == "" {
		name = device
	}
	iface, ok := network[name]
	return iface, ok
}
