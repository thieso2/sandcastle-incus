package cli

import "testing"

func TestResourceCacheEnabled(t *testing.T) {
	cases := map[string]bool{
		"":         true,
		"1":        true,
		"on":       true,
		"anything": true,
		"0":        false,
		"false":    false,
		"False":    false,
		"off":      false,
		"OFF":      false,
		"disable":  false,
		"disabled": false,
		"  off  ":  false,
	}
	for value, want := range cases {
		if got := resourceCacheEnabled(value); got != want {
			t.Errorf("resourceCacheEnabled(%q) = %v, want %v", value, got, want)
		}
	}
}
