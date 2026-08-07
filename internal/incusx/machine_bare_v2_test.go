package incusx

import (
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

// THE CONTRACT. A `--bare` machine re-states its own identity in instance-level
// cloud-init, and the only place the CLI can read that identity from is the
// project's default profile (a restricted tenant certificate cannot see the
// kind=infra project that holds the CIDR/signer). So the patterns here must
// keep matching whatever V2DefaultProfileUserData renders — round-trip them
// against the real renderer rather than against a hand-copied sample.
func TestV2ProfilePatternsRoundTripDefaultUserData(t *testing.T) {
	userData := tenant.V2DefaultProfileUserData("dev", "ssh-ed25519 AAAA", "backend", "acme.example", "http://10.249.7.3:9443")

	if got := firstSubmatch(v2ProfileFQDNPattern, userData); got != "backend.acme.example" {
		t.Fatalf("FQDN domain: got %q, want %q\n%s", got, "backend.acme.example", userData)
	}
	if got := firstSubmatch(v2ProfileSignerPattern, userData); got != "http://10.249.7.3:9443" {
		t.Fatalf("signer: got %q, want %q\n%s", got, "http://10.249.7.3:9443", userData)
	}
}

// The bare document the round-tripped identity produces must name the SAME
// machine and signer — this is what stops a bare machine from disagreeing with
// its project about the tenant's zone.
func TestV2BareUserDataFollowsTheProfileIdentity(t *testing.T) {
	userData := tenant.V2DefaultProfileUserData("dev", "ssh-ed25519 AAAA", "backend", "acme.example", "http://10.249.7.3:9443")

	bare := tenant.V2BareUserData(
		firstSubmatch(v2ProfileFQDNPattern, userData),
		firstSubmatch(v2ProfileSignerPattern, userData),
	)
	for _, want := range []string{
		"fqdn: {{ v1.local_hostname }}.backend.acme.example",
		"SIGNER=http://10.249.7.3:9443",
	} {
		if !strings.Contains(bare, want) {
			t.Fatalf("bare user-data missing %q:\n%s", want, bare)
		}
	}
}

// A profile rendered WITHOUT identity (no signer: the minimal fallback branch)
// yields no match, which is what makes v2BareInstanceConfig refuse rather than
// launch a machine with no certificate and no way in.
func TestV2ProfilePatternsMissOnTheMinimalProfile(t *testing.T) {
	userData := tenant.V2DefaultProfileUserData("dev", "ssh-ed25519 AAAA", "", "", "")

	if got := firstSubmatch(v2ProfileFQDNPattern, userData); got != "" {
		t.Fatalf("expected no FQDN in the minimal profile, got %q", got)
	}
	if got := firstSubmatch(v2ProfileSignerPattern, userData); got != "" {
		t.Fatalf("expected no signer in the minimal profile, got %q", got)
	}
}

// A bare machine joins none of the project's shared filesystems, but MUST keep
// /.sc/platform: its boot shims source caddy-setup from there, so masking that
// device would leave it with no Caddy — the one thing --bare promises.
func TestV2BareInstanceDevicesMaskSharedDisksButNotThePayload(t *testing.T) {
	devices := v2BareInstanceDevices()

	for _, name := range []string{"home", "workspace"} {
		device, ok := devices[name]
		if !ok {
			t.Fatalf("bare machines must mask the %s device: %v", name, devices)
		}
		if device["type"] != "none" {
			t.Fatalf("%s must be masked with the device-inhibit type, got %v", name, device)
		}
		// `none` takes no other keys — Incus rejects the device otherwise.
		if len(device) != 1 {
			t.Fatalf("a none device carries no other fields, got %v", device)
		}
	}
	for _, name := range []string{"sc-platform", "root", "eth0"} {
		if _, masked := devices[name]; masked {
			t.Fatalf("bare machines must inherit %s from the profile, but it is masked: %v", name, devices)
		}
	}

	// The masks are only meaningful against devices the profile actually has.
	profile := v2AppProfileDevices(devicesTestPlan())
	for name := range devices {
		if name == "home" {
			continue // lives in the homeshare profile since the /home split
		}
		if _, present := profile[name]; !present {
			t.Fatalf("masking %q is dead config: the default profile has no such device: %v", name, profile)
		}
	}
	if _, present := v2HomeShareProfileDevices(devicesTestPlan())["home"]; !present {
		t.Fatalf("masking \"home\" is dead config: the homeshare profile has no such device")
	}
}

// How a bare machine is recognised later, when nothing but its Incus record is
// left to go on: the marker `--bare` writes at create time, and — for machines
// that predate the marker — its own cloud-init creating no users.
func TestInstanceIsBareV2(t *testing.T) {
	bareUserData := tenant.V2BareUserData("default.acme", "http://10.0.0.3:9443")
	normalUserData := tenant.V2DefaultProfileUserData("dev", "ssh-ed25519 AAAA", "default", "acme", "http://10.0.0.3:9443")

	for _, tc := range []struct {
		name   string
		config map[string]string
		want   bool
	}{
		{"marker", map[string]string{meta.KeyV2Bare: "true"}, true},
		{"marker wins without user-data", map[string]string{meta.KeyV2Bare: "true", "cloud-init.user-data": normalUserData}, true},
		// Machines created by the first --bare release carry no marker.
		{"legacy bare, no marker", map[string]string{"cloud-init.user-data": bareUserData}, true},
		{"a normal machine", map[string]string{"cloud-init.user-data": normalUserData}, false},
		// The common case by far: no instance-level cloud-init at all, the
		// project profile supplies it.
		{"profile-supplied cloud-init", map[string]string{}, false},
		{"marker explicitly false", map[string]string{meta.KeyV2Bare: "false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := instanceIsBareV2(tc.config); got != tc.want {
				t.Fatalf("instanceIsBareV2 = %v, want %v", got, tc.want)
			}
		})
	}
}

// The marker has to survive a round trip through the config --bare actually
// writes, or every machine falls back to the cloud-init heuristic.
func TestBareInstanceConfigCarriesTheMarker(t *testing.T) {
	userData := tenant.V2DefaultProfileUserData("dev", "ssh-ed25519 AAAA", "backend", "acme.example", "http://10.249.7.3:9443")
	config := map[string]string{
		"cloud-init.user-data": tenant.V2BareUserData(
			firstSubmatch(v2ProfileFQDNPattern, userData),
			firstSubmatch(v2ProfileSignerPattern, userData),
		),
		meta.KeyV2Bare: "true",
	}
	if !instanceIsBareV2(config) {
		t.Fatalf("a machine created with --bare must read back as bare: %v", config)
	}
}

func TestShortProjectName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"sc2-acme-backend", "backend"},
		{"sc2-acme-default", "default"},
		{"nodashes", "nodashes"},
	} {
		if got := shortProjectName(tc.in); got != tc.want {
			t.Fatalf("shortProjectName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
