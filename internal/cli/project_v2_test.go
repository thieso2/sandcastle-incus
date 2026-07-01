package cli

import "testing"

func TestIncusEndpointFromBrokerExplicit(t *testing.T) {
	got, err := incusEndpointFromBroker("https://big.example:8443", "https://big.example:9443")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://big.example:8443" {
		t.Fatalf("got %q", got)
	}
}

func TestIncusEndpointFromBrokerDerived(t *testing.T) {
	got, err := incusEndpointFromBroker("", "https://65.21.132.31:9443")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://65.21.132.31:8443" {
		t.Fatalf("got %q, want the broker host on :8443", got)
	}
}

func TestIncusEndpointFromBrokerDerivedHostname(t *testing.T) {
	got, err := incusEndpointFromBroker("", "https://big:9443")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://big:8443" {
		t.Fatalf("got %q", got)
	}
}

func TestIncusEndpointFromBrokerNoHost(t *testing.T) {
	if _, err := incusEndpointFromBroker("", "not a url"); err == nil {
		t.Fatal("expected error for URL without host")
	}
}
