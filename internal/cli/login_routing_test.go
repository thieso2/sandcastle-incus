package cli

import "testing"

func TestParseTailscaleClientState(t *testing.T) {
	statusJSON := []byte(`{
		"BackendState": "Running",
		"Self": {"TailscaleIPs": ["100.115.58.83", "fd7a::1"]},
		"Peer": {
			"key1": {
				"HostName": "sc2-thieso2",
				"TailscaleIPs": ["100.90.124.119"],
				"Online": true,
				"PrimaryRoutes": ["10.250.0.0/24"],
				"AllowedIPs": ["100.90.124.119/32", "10.250.0.0/24"]
			}
		}
	}`)
	state := parseTailscaleClientState(statusJSON)
	if state.BackendState != "Running" {
		t.Fatalf("BackendState = %q, want Running", state.BackendState)
	}
	if len(state.SelfIPs) != 2 || state.SelfIPs[0] != "100.115.58.83" {
		t.Fatalf("SelfIPs = %v", state.SelfIPs)
	}
	if len(state.Peers) != 1 || state.Peers[0].HostName != "sc2-thieso2" || !state.Peers[0].Online {
		t.Fatalf("Peers = %+v", state.Peers)
	}
}

func TestParseTailscaleClientStateBadJSON(t *testing.T) {
	state := parseTailscaleClientState([]byte("not json"))
	if state.BackendState != "" || state.Peers != nil {
		t.Fatalf("expected zero state, got %+v", state)
	}
}

func TestTenantRouteOwner(t *testing.T) {
	cidr := "10.250.0.0/24"
	primaryPeer := tailscalePeerState{HostName: "live", Online: true, PrimaryRoutes: []string{cidr}, AllowedIPs: []string{cidr}}
	offeredPeer := tailscalePeerState{HostName: "stale", Online: false, AllowedIPs: []string{"100.1.2.3/32", cidr}}
	otherPeer := tailscalePeerState{HostName: "other", AllowedIPs: []string{"100.9.9.9/32"}}

	tests := []struct {
		name        string
		peers       []tailscalePeerState
		wantOffered bool
		wantPrimary bool
		wantRouter  string
	}{
		{"no peers", nil, false, false, ""},
		{"route not offered", []tailscalePeerState{otherPeer}, false, false, ""},
		{"offered but no primary (stale-device blackhole)", []tailscalePeerState{offeredPeer, otherPeer}, true, false, "stale"},
		{"primary elected", []tailscalePeerState{offeredPeer, primaryPeer}, true, true, "live"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offered, primary, router := tenantRouteOwner(tailscaleClientState{Peers: tt.peers}, cidr)
			if offered != tt.wantOffered || primary != tt.wantPrimary {
				t.Fatalf("offered=%v primary=%v, want %v/%v", offered, primary, tt.wantOffered, tt.wantPrimary)
			}
			if tt.wantRouter != "" && router.HostName != tt.wantRouter {
				t.Fatalf("router = %q, want %q", router.HostName, tt.wantRouter)
			}
		})
	}
}

func TestDescribeTailscaleBackendState(t *testing.T) {
	if got := describeTailscaleBackendState("NeedsLogin"); got != "logged out" {
		t.Fatalf("NeedsLogin -> %q", got)
	}
	if got := describeTailscaleBackendState("Starting"); got != "in state Starting" {
		t.Fatalf("Starting -> %q", got)
	}
}
