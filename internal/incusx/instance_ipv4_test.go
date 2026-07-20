package incusx

import (
	"testing"

	"github.com/lxc/incus/v6/shared/api"
)

func globalAddr(address string, netmask string) api.InstanceStateNetworkAddress {
	return api.InstanceStateNetworkAddress{
		Family:  "inet",
		Scope:   "global",
		Address: address,
		Netmask: netmask,
	}
}

// A machine running Docker reports both eth0 and the docker0 bridge as global
// IPv4. Selecting by map iteration order returned docker0 — container-local,
// identical on every machine, routing nowhere — on a random subset of calls.
// The loop guards against a fix that merely happens to pass once.
func TestInstanceNICIPv4IgnoresInGuestBridges(t *testing.T) {
	config := map[string]string{"volatile.eth0.hwaddr": "00:16:3e:11:22:33"}
	devices := map[string]map[string]string{
		"eth0": {"type": "nic", "nictype": "bridged", "parent": "obelix-thieso2"},
		"root": {"type": "disk", "path": "/"},
	}
	network := map[string]api.InstanceStateNetwork{
		"lo":      {Type: "loopback", Addresses: []api.InstanceStateNetworkAddress{globalAddr("127.0.0.1", "8")}},
		"docker0": {Type: "broadcast", Hwaddr: "02:42:9a:bb:cc:dd", Addresses: []api.InstanceStateNetworkAddress{globalAddr("172.17.0.1", "16")}},
		"eth0":    {Type: "broadcast", Hwaddr: "00:16:3e:11:22:33", Addresses: []api.InstanceStateNetworkAddress{globalAddr("10.123.0.80", "24")}},
	}

	for i := 0; i < 200; i++ {
		got := instanceNICIPv4(config, devices, network)
		if got.Address != "10.123.0.80" {
			t.Fatalf("iteration %d: Address = %q, want 10.123.0.80 (docker0 must never win)", i, got.Address)
		}
		if got.Netmask != "24" {
			t.Fatalf("iteration %d: Netmask = %q, want 24", i, got.Netmask)
		}
	}
}

// In a VM the guest names its own interfaces, so the device key "eth0" is not a
// key in the state map. Matching on the volatile hwaddr is what keeps VMs
// working; a name-only match would report no address at all.
func TestInstanceNICIPv4MatchesRenamedVMInterfaceByMAC(t *testing.T) {
	config := map[string]string{"volatile.eth0.hwaddr": "00:16:3e:aa:bb:cc"}
	devices := map[string]map[string]string{
		"eth0": {"type": "nic", "nictype": "bridged"},
	}
	network := map[string]api.InstanceStateNetwork{
		"enp5s0":  {Type: "broadcast", Hwaddr: "00:16:3e:aa:bb:cc", Addresses: []api.InstanceStateNetworkAddress{globalAddr("10.123.0.42", "24")}},
		"docker0": {Type: "broadcast", Hwaddr: "02:42:00:00:00:01", Addresses: []api.InstanceStateNetworkAddress{globalAddr("172.17.0.1", "16")}},
	}

	if got := instanceNICIPv4(config, devices, network); got.Address != "10.123.0.42" {
		t.Fatalf("Address = %q, want 10.123.0.42 (renamed VM interface matched by MAC)", got.Address)
	}
}

// Incus records the MAC case-insensitively depending on driver.
func TestInstanceNICIPv4MACMatchIsCaseInsensitive(t *testing.T) {
	config := map[string]string{"volatile.eth0.hwaddr": "00:16:3E:AA:BB:CC"}
	devices := map[string]map[string]string{"eth0": {"type": "nic"}}
	network := map[string]api.InstanceStateNetwork{
		"enp5s0": {Type: "broadcast", Hwaddr: "00:16:3e:aa:bb:cc", Addresses: []api.InstanceStateNetworkAddress{globalAddr("10.123.0.7", "24")}},
	}

	if got := instanceNICIPv4(config, devices, network); got.Address != "10.123.0.7" {
		t.Fatalf("Address = %q, want 10.123.0.7", got.Address)
	}
}

// With no hwaddr recorded, the device key is the guest interface name.
func TestInstanceNICIPv4FallsBackToDeviceName(t *testing.T) {
	devices := map[string]map[string]string{"eth0": {"type": "nic"}}
	network := map[string]api.InstanceStateNetwork{
		"eth0":    {Type: "broadcast", Addresses: []api.InstanceStateNetworkAddress{globalAddr("10.123.0.9", "24")}},
		"docker0": {Type: "broadcast", Addresses: []api.InstanceStateNetworkAddress{globalAddr("172.17.0.1", "16")}},
	}

	if got := instanceNICIPv4(nil, devices, network); got.Address != "10.123.0.9" {
		t.Fatalf("Address = %q, want 10.123.0.9", got.Address)
	}
}

// A nic's "name" property overrides the device key as the guest-side name.
func TestInstanceNICIPv4HonoursNameProperty(t *testing.T) {
	devices := map[string]map[string]string{
		"tenant0": {"type": "nic", "name": "eth1"},
	}
	network := map[string]api.InstanceStateNetwork{
		"eth1": {Type: "broadcast", Addresses: []api.InstanceStateNetworkAddress{globalAddr("10.123.0.11", "24")}},
	}

	if got := instanceNICIPv4(nil, devices, network); got.Address != "10.123.0.11" {
		t.Fatalf("Address = %q, want 10.123.0.11", got.Address)
	}
}

// No NIC device means no Sandcastle-managed address. Reporting "" beats
// reporting whichever in-guest bridge happened to sort first.
func TestInstanceNICIPv4WithoutNICDeviceIsEmpty(t *testing.T) {
	devices := map[string]map[string]string{"root": {"type": "disk", "path": "/"}}
	network := map[string]api.InstanceStateNetwork{
		"docker0": {Type: "broadcast", Addresses: []api.InstanceStateNetworkAddress{globalAddr("172.17.0.1", "16")}},
	}

	if got := instanceNICIPv4(nil, devices, network); got.Address != "" {
		t.Fatalf("Address = %q, want empty", got.Address)
	}
}

// A machine that has not leased yet reports no global address.
func TestInstanceNICIPv4WithoutLeaseIsEmpty(t *testing.T) {
	devices := map[string]map[string]string{"eth0": {"type": "nic"}}
	network := map[string]api.InstanceStateNetwork{
		"eth0": {Type: "broadcast", Addresses: []api.InstanceStateNetworkAddress{
			{Family: "inet6", Scope: "link", Address: "fe80::1"},
		}},
	}

	if got := instanceNICIPv4(nil, devices, network); got.Address != "" {
		t.Fatalf("Address = %q, want empty (no global IPv4 lease)", got.Address)
	}
}

// Multiple NICs resolve in sorted device order, not map order.
func TestInstanceNICIPv4MultipleNICsAreDeterministic(t *testing.T) {
	devices := map[string]map[string]string{
		"eth0": {"type": "nic"},
		"eth1": {"type": "nic"},
	}
	network := map[string]api.InstanceStateNetwork{
		"eth0": {Type: "broadcast", Addresses: []api.InstanceStateNetworkAddress{globalAddr("10.123.0.20", "24")}},
		"eth1": {Type: "broadcast", Addresses: []api.InstanceStateNetworkAddress{globalAddr("10.200.0.20", "24")}},
	}

	for i := 0; i < 100; i++ {
		if got := instanceNICIPv4(nil, devices, network); got.Address != "10.123.0.20" {
			t.Fatalf("iteration %d: Address = %q, want 10.123.0.20 (first NIC by device name)", i, got.Address)
		}
	}
}
