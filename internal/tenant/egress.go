package tenant

// Tailnet egress (ADR-0026): machines are not tailnet nodes, so without help a
// packet from a machine to a tailnet CGNAT address follows the default route to
// the host bridge gateway and dies on the public uplink. Egress steers that
// traffic at the sidecar (a DHCP classless static route on the tenant bridge)
// and masquerades it onto the sidecar's tailscale0, so tailnet peers see a
// normal connection from the sidecar's tailnet IP and need neither
// --accept-routes nor knowledge of the tenant CIDR. The tailnet ACL still
// governs what the sidecar's tag may reach — egress delivers packets, the ACL
// decides their fate.

// TailnetCGNAT is the Tailscale CGNAT range every tailnet address lives in.
const TailnetCGNAT = "100.64.0.0/10"

// BridgeDHCPOptions renders the tenant bridge's raw.dnsmasq value: always the
// CoreDNS resolver option (ADR-0018), plus — with egress on — a classless
// static route (option 121) steering the tailnet CGNAT range at the sidecar.
// RFC 3442 requires a client that receives option 121 to IGNORE the router
// option, so the default route must ride along in the same option or clients
// would lose their gateway.
func BridgeDHCPOptions(dnsAddress, gatewayAddress string, egress bool) string {
	options := "dhcp-option=6," + dnsAddress
	if egress {
		options += "\ndhcp-option=121," + TailnetCGNAT + "," + dnsAddress + ",0.0.0.0/0," + gatewayAddress
	}
	return options
}

// SidecarEgressNftRuleset renders /etc/sandcastle-egress.nft. The
// declare/delete/declare idiom makes `nft -f` an atomic, idempotent replace of
// our own table. This NAT is independent of the bridge's ipv4.nat=true (the
// host masquerades machine→internet); it covers only the tailscale0 path.
func SidecarEgressNftRuleset(privateCIDR string) string {
	return `table ip sandcastle-egress
delete table ip sandcastle-egress
table ip sandcastle-egress {
	chain postrouting {
		type nat hook postrouting priority srcnat; policy accept;
		oifname "tailscale0" ip saddr ` + privateCIDR + ` ip daddr ` + TailnetCGNAT + ` masquerade
	}
}
`
}

// SidecarEgressScript renders /usr/local/sbin/sandcastle-sidecar-egress, run by
// the oneshot unit at boot and on converge.
func SidecarEgressScript() string {
	return `#!/bin/sh
set -eu
sysctl -qw net.ipv4.ip_forward=1
exec nft -f /etc/sandcastle-egress.nft
`
}

// SidecarEgressUnit renders the systemd oneshot applying the egress rules.
func SidecarEgressUnit() string {
	return `[Unit]
Description=Sandcastle sidecar tailnet egress (NAT machines onto tailscale0)
After=sandcastle-sidecar-network.service

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/sandcastle-sidecar-egress
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
`
}
