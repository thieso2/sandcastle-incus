package e2e

import (
	"context"
	"testing"

	sharedtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

func TestRestrictedUserTokenE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}

	user := safeProjectName("user-" + e2eConfig.DisposableRunID())
	plan, err := usertrust.PlanToken(user)
	if err != nil {
		t.Fatal(err)
	}
	result, err := incusx.NewTrustManager(e2eConfig.Remote).CreateToken(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Token == "" {
		t.Fatal("expected certificate add token")
	}
	decoded, err := sharedtls.CertificateTokenDecode(result.Token)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ClientName != plan.CertificateName {
		t.Fatalf("token client name = %q, want %q", decoded.ClientName, plan.CertificateName)
	}
	if decoded.Secret == "" || decoded.Fingerprint == "" || len(decoded.Addresses) == 0 {
		t.Fatalf("decoded token is incomplete: %#v", decoded)
	}
}
