package incusx

import (
	"strings"
	"testing"
)

func TestAuthAppCaddyfileACME(t *testing.T) {
	out := authAppCaddyfile(IngressACME, "burg.example.dev", "ops@example.dev")
	for _, want := range []string{"email ops@example.dev", "burg.example.dev {", "reverse_proxy 127.0.0.1:9444"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestAuthAppCaddyfileCloudflare(t *testing.T) {
	out := authAppCaddyfile(IngressCloudflare, "burg.example.dev", "")
	for _, want := range []string{"auto_https off", "http://burg.example.dev:8080 {", "reverse_proxy 127.0.0.1:9444"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestCaddyDownloadURLIncludesCloudflareDNSModule(t *testing.T) {
	got := caddyDownloadURL("amd64", RouteDNSProviderCloudflare)
	for _, want := range []string{
		"os=linux",
		"arch=amd64",
		"p=github.com%2Fcaddy-dns%2Fcloudflare",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("download URL %q missing %q", got, want)
		}
	}
}

func TestAuthAppCaddyUnitLoadsOnlyRouteDNSSecretEnvironment(t *testing.T) {
	out := authAppCaddyUnit()
	if !strings.Contains(out, "EnvironmentFile=-"+AuthAppRouteDNSEnvPath) {
		t.Fatalf("caddy unit does not load route DNS secret environment:\n%s", out)
	}
	if strings.Contains(out, "EnvironmentFile="+AuthAppEnvPath) {
		t.Fatalf("caddy unit must not receive the auth-app's unrelated secrets:\n%s", out)
	}
}
