package main

import "testing"

func TestCommandName(t *testing.T) {
	tests := []struct {
		arg0 string
		want string
	}{
		{arg0: "/usr/local/bin/sandcastle", want: "sandcastle"},
		{arg0: "/usr/local/bin/sc", want: "sc"},
		{arg0: "", want: "sandcastle"},
	}

	for _, test := range tests {
		if got := commandName(test.arg0); got != test.want {
			t.Fatalf("commandName(%q) = %q, want %q", test.arg0, got, test.want)
		}
	}
}
