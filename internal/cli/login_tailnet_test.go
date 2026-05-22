package cli

import (
	"strings"
	"testing"
)

func TestParseLocalTailscaleStatusAcceptsExpectedTailnet(t *testing.T) {
	status, err := parseLocalTailscaleStatus("tailnet.example", []byte(`{
	  "BackendState": "Running",
	  "CurrentTailnet": {"Name": "tailnet.example"},
	  "Self": {"TailscaleIPs": ["100.64.0.10"]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if status.Tailnet != "tailnet.example" || len(status.IPs) != 1 || status.IPs[0] != "100.64.0.10" {
		t.Fatalf("status = %#v", status)
	}
}

func TestParseLocalTailscaleStatusRejectsLoggedOutState(t *testing.T) {
	_, err := parseLocalTailscaleStatus("tailnet.example", []byte(`{
	  "BackendState": "NeedsLogin",
	  "CurrentTailnet": {"Name": "tailnet.example"},
	  "Self": {"TailscaleIPs": ["100.64.0.10"]}
	}`))
	if err == nil || !strings.Contains(err.Error(), "run tailscale up") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseLocalTailscaleStatusRejectsWrongTailnet(t *testing.T) {
	_, err := parseLocalTailscaleStatus("tailnet.example", []byte(`{
	  "BackendState": "Running",
	  "CurrentTailnet": {"Name": "other.example"},
	  "Self": {"TailscaleIPs": ["100.64.0.10"]}
	}`))
	if err == nil || !strings.Contains(err.Error(), "want \"tailnet.example\"") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseLocalTailscaleStatusRejectsMissingIP(t *testing.T) {
	_, err := parseLocalTailscaleStatus("tailnet.example", []byte(`{
	  "BackendState": "Running",
	  "CurrentTailnet": {"Name": "tailnet.example"},
	  "Self": {}
	}`))
	if err == nil || !strings.Contains(err.Error(), "no local Tailscale IP") {
		t.Fatalf("error = %v", err)
	}
}
