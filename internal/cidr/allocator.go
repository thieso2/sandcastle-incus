package cidr

import (
	"fmt"
	"net/netip"
)

const DefaultTenantPrefixBits = 24

func Allocate(pool string, tenantPrefixBits int, occupied []string) (netip.Prefix, error) {
	poolPrefix, err := netip.ParsePrefix(pool)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("parse CIDR pool: %w", err)
	}
	poolPrefix = poolPrefix.Masked()
	if !poolPrefix.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("only IPv4 tenant pools are supported")
	}
	if tenantPrefixBits == 0 {
		tenantPrefixBits = DefaultTenantPrefixBits
	}
	if tenantPrefixBits < poolPrefix.Bits() {
		return netip.Prefix{}, fmt.Errorf("tenant prefix /%d is larger than pool %s", tenantPrefixBits, poolPrefix)
	}
	if tenantPrefixBits > 30 {
		return netip.Prefix{}, fmt.Errorf("tenant prefix /%d leaves too few host addresses", tenantPrefixBits)
	}

	occupiedPrefixes, err := parseOccupied(occupied)
	if err != nil {
		return netip.Prefix{}, err
	}

	step := uint32(1) << (32 - tenantPrefixBits)
	start := addrToUint32(poolPrefix.Addr())
	end := start + (uint32(1) << (32 - poolPrefix.Bits()))
	for current := start; current < end; current += step {
		candidate := netip.PrefixFrom(uint32ToAddr(current), tenantPrefixBits).Masked()
		if !prefixContainsPrefix(poolPrefix, candidate) {
			continue
		}
		if overlapsAny(candidate, occupiedPrefixes) {
			continue
		}
		return candidate, nil
	}
	return netip.Prefix{}, fmt.Errorf("CIDR pool %s is exhausted for /%d tenants", poolPrefix, tenantPrefixBits)
}

func RoleAddress(tenant netip.Prefix, hostOctet byte) (netip.Addr, error) {
	if !tenant.Addr().Is4() || tenant.Bits() > 24 {
		return netip.Addr{}, fmt.Errorf("role addresses require an IPv4 tenant prefix of /24 or larger")
	}
	base := addrToUint32(tenant.Masked().Addr())
	addr := uint32ToAddr(base + uint32(hostOctet))
	if !tenant.Contains(addr) {
		return netip.Addr{}, fmt.Errorf("host octet %d is outside %s", hostOctet, tenant)
	}
	return addr, nil
}

func parseOccupied(values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("parse occupied CIDR %q: %w", value, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func overlapsAny(candidate netip.Prefix, occupied []netip.Prefix) bool {
	for _, prefix := range occupied {
		if prefixesOverlap(candidate, prefix) {
			return true
		}
	}
	return false
}

func prefixesOverlap(a, b netip.Prefix) bool {
	return a.Contains(b.Addr()) || b.Contains(a.Addr())
}

func prefixContainsPrefix(parent, child netip.Prefix) bool {
	lastChild := uint32ToAddr(addrToUint32(child.Addr()) + (uint32(1) << (32 - child.Bits())) - 1)
	return parent.Contains(child.Addr()) && parent.Contains(lastChild)
}

func addrToUint32(addr netip.Addr) uint32 {
	bytes := addr.As4()
	return uint32(bytes[0])<<24 | uint32(bytes[1])<<16 | uint32(bytes[2])<<8 | uint32(bytes[3])
}

func uint32ToAddr(value uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{
		byte(value >> 24),
		byte(value >> 16),
		byte(value >> 8),
		byte(value),
	})
}
