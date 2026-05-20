package routebroker

import (
	"strings"
	"testing"
)

func TestPlanServeDefaultsAddress(t *testing.T) {
	plan, err := PlanServe(ServeRequest{
		CertFile: "/tmp/broker.crt",
		KeyFile:  "/tmp/broker.key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Address != ":9443" {
		t.Fatalf("Address = %q", plan.Address)
	}
}

func TestPlanServeRequiresTLSCertificate(t *testing.T) {
	_, err := PlanServe(ServeRequest{KeyFile: "/tmp/broker.key"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("error = %q", err)
	}
}

func TestPlanServeRequiresTLSKey(t *testing.T) {
	_, err := PlanServe(ServeRequest{CertFile: "/tmp/broker.crt"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "key") {
		t.Fatalf("error = %q", err)
	}
}
