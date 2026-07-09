package incusx

import "testing"

// A restricted tenant certificate cannot read the tenant bridge's ipv4.address,
// so the machine's own interface is the only authoritative source of the
// private CIDR. Incus reports the mask as a prefix length for bridged NICs, but
// a dotted mask has to work too.
func TestSubnetCIDR(t *testing.T) {
	cases := []struct {
		address string
		netmask string
		want    string
	}{
		{"10.123.0.51", "24", "10.123.0.0/24"},
		{"10.123.0.51", "255.255.255.0", "10.123.0.0/24"},
		{"10.248.3.9", "16", "10.248.0.0/16"},
		{"192.168.1.5", "255.255.255.192", "192.168.1.0/26"},
		{"10.0.0.1", "32", "10.0.0.1/32"},
		{"10.0.0.1", "", ""},
		{"10.0.0.1", "not-a-mask", ""},
		{"10.0.0.1", "33", ""},
		{"10.0.0.1", "0", ""},
		{"fd00::1", "64", ""},
		{"", "24", ""},
	}
	for _, testCase := range cases {
		if got := subnetCIDR(testCase.address, testCase.netmask); got != testCase.want {
			t.Errorf("subnetCIDR(%q, %q) = %q, want %q", testCase.address, testCase.netmask, got, testCase.want)
		}
	}
}
