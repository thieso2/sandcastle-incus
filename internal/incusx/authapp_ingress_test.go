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
