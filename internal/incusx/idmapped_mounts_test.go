package incusx

import "testing"

// Incus 7.x stopped populating environment.kernel_features (always {}), and
// keying on == "true" made every 7.x host silently omit the shared /home.
// Absent entry → supported (the 7.x kernel floor includes idmapped mounts);
// only an explicit "false" from an older daemon disables it.
func TestKernelFeaturesSupportIdmappedMounts(t *testing.T) {
	tests := []struct {
		name     string
		features map[string]string
		want     bool
	}{
		{"incus 7.x empty map", map[string]string{}, true},
		{"nil map", nil, true},
		{"explicitly supported", map[string]string{"idmapped_mounts": "true"}, true},
		{"explicitly unsupported", map[string]string{"idmapped_mounts": "false"}, false},
		{"other features reported, idmapped absent", map[string]string{"seccomp_notify": "true"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := kernelFeaturesSupportIdmappedMounts(tt.features); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
