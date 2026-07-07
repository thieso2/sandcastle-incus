package incusx

import "testing"

func TestNormalizeZoneSerialToOne(t *testing.T) {
	// A zone written with a monotonic unix serial must compare equal to the
	// freshly rendered zone (serial 1) after normalization — otherwise the
	// reconciler would rewrite (and reload CoreDNS) on every pass.
	written := "$ORIGIN e2edns.\n$TTL 60\n@ IN SOA ns.e2edns. hostmaster.e2edns. 1783411808 3600 600 604800 60\n@ IN NS ns.e2edns.\nidbox.default.e2edns. IN A 10.251.1.128\n"
	rendered := "$ORIGIN e2edns.\n$TTL 60\n@ IN SOA ns.e2edns. hostmaster.e2edns. 1 3600 600 604800 60\n@ IN NS ns.e2edns.\nidbox.default.e2edns. IN A 10.251.1.128\n"
	if got := normalizeZoneSerialToOne(written, "e2edns"); got != rendered {
		t.Fatalf("normalized zone mismatch:\n got: %q\nwant: %q", got, rendered)
	}
	// Idempotent on an already-serial-1 zone.
	if got := normalizeZoneSerialToOne(rendered, "e2edns"); got != rendered {
		t.Fatalf("normalize should be idempotent, got %q", got)
	}
	// A different record set must NOT normalize to equal (so a real change still
	// triggers a rewrite — the self-heal path).
	changed := "$ORIGIN e2edns.\n$TTL 60\n@ IN SOA ns.e2edns. hostmaster.e2edns. 1783411999 3600 600 604800 60\n@ IN NS ns.e2edns.\n"
	if normalizeZoneSerialToOne(changed, "e2edns") == rendered {
		t.Fatal("zones with different records must not compare equal after normalization")
	}
	// No SOA marker → returned unchanged.
	if got := normalizeZoneSerialToOne("not a zone", "e2edns"); got != "not a zone" {
		t.Fatalf("expected passthrough, got %q", got)
	}
}
