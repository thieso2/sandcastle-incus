package incusx

import "testing"

func TestParseFrontTarget(t *testing.T) {
	target, err := ParseFrontTarget("infrastructure/sc-edge")
	if err != nil {
		t.Fatal(err)
	}
	if target.Project != "infrastructure" || target.Instance != "sc-edge" {
		t.Errorf("parsed %+v", target)
	}
	if !target.configured() {
		t.Error("a parsed target must be configured")
	}
	// No front is the normal case for an install that owns the host ports.
	empty, err := ParseFrontTarget("  ")
	if err != nil {
		t.Fatalf("an empty front must not be an error: %v", err)
	}
	if empty.configured() {
		t.Error("empty target must be unconfigured")
	}
	// A typo must be loud: silently publishing nowhere would leave every route
	// unreachable with nothing pointing at the cause.
	for _, bad := range []string{"sc-edge", "infrastructure/", "/sc-edge", "a/b/c "} {
		if _, err := ParseFrontTarget(bad); err == nil && bad != "a/b/c " {
			t.Errorf("ParseFrontTarget(%q) must fail", bad)
		}
	}
}

func TestFrontFragmentName(t *testing.T) {
	if got := frontFragmentName("obelix"); got != "obelix.caddy" {
		t.Errorf("frontFragmentName(obelix) = %q", got)
	}
	if got := frontFragmentName(""); got != "sandcastle.caddy" {
		t.Errorf("frontFragmentName(\"\") = %q", got)
	}
}
