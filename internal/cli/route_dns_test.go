package cli

import "testing"

func TestRouteDNSProviderForCloudflare(t *testing.T) {
	tests := []struct {
		name         string
		routeIngress string
		token        string
		wildcards    []string
		want         string
		wantErr      bool
	}{
		{name: "disabled", routeIngress: "acme-proxied"},
		{name: "configured", routeIngress: "acme-proxied", token: "secret", wildcards: []string{"*.jot.moyn.dev"}, want: "cloudflare"},
		{name: "requires route ingress", token: "secret", wildcards: []string{"*.jot.moyn.dev"}, wantErr: true},
		{name: "requires token", routeIngress: "acme-proxied", wildcards: []string{"*.jot.moyn.dev"}, wantErr: true},
		{name: "requires wildcard", routeIngress: "acme-proxied", token: "secret", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := routeDNSProviderForCloudflare(tt.routeIngress, tt.token, tt.wildcards)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("provider = %q, want %q", got, tt.want)
			}
		})
	}
}
