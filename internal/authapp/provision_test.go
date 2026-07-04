package authapp

import "testing"

func TestProfileUnixUser(t *testing.T) {
	tests := []struct {
		name          string
		localUnixUser string
		defaultUser   string
		want          string
	}{
		{"client user wins", "thies", "ops", "thies"},
		{"falls back to deployment default", "", "ops", "ops"},
		{"falls back to dev", "", "", "dev"},
		{"root is refused", "root", "", "dev"},
		{"root falls through to default", "root", "ops", "ops"},
		{"invalid name falls through", "Bad Name!", "ops", "ops"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Provisioner{DefaultUnixUser: tt.defaultUser}
			got := p.profileUnixUser(User{LocalUnixUser: tt.localUnixUser})
			if got != tt.want {
				t.Fatalf("profileUnixUser = %q, want %q", got, tt.want)
			}
		})
	}
}
