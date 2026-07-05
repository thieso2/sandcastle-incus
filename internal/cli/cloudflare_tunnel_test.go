package cli

import "testing"

func TestZoneCandidates(t *testing.T) {
	got := zoneCandidates("burg.thieso2.dev")
	if len(got) != 1 || got[0] != "thieso2.dev" {
		t.Fatalf("got %v", got)
	}
	got = zoneCandidates("a.b.example.com")
	if len(got) != 2 || got[0] != "b.example.com" || got[1] != "example.com" {
		t.Fatalf("got %v", got)
	}
	if got := zoneCandidates("example.com"); len(got) != 0 {
		t.Fatalf("bare domain should yield no candidates, got %v", got)
	}
}
