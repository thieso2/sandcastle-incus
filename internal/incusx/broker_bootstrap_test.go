package incusx

import "strings"

import "testing"

func TestBrokerUnitRunsBrokerServe(t *testing.T) {
	u := brokerUnit("df67")
	if !strings.Contains(u, "project broker-serve --listen "+BrokerListenInternal) {
		t.Fatalf("unit missing broker-serve: %q", u)
	}
	if !strings.Contains(u, "--sidecar-image df67") {
		t.Fatalf("unit missing sidecar image: %q", u)
	}
	if !strings.Contains(u, "EnvironmentFile="+BrokerEnvPath) {
		t.Fatalf("unit missing env file: %q", u)
	}
}

func TestBrokerEnvUsesLocalSocket(t *testing.T) {
	e := brokerEnv(BootstrapV2Request{StoragePool: "default", CIDRPool: "10.249.0.0/16"})
	if !strings.Contains(e, "SANDCASTLE_REMOTE=local") {
		t.Fatalf("env should use local socket: %q", e)
	}
	if !strings.Contains(e, "SANDCASTLE_CIDR_POOL=10.249.0.0/16") {
		t.Fatalf("env missing cidr pool: %q", e)
	}
}
