package projectbroker

// TrustPrincipal is the identity behind a client certificate: the restricted
// Incus user it maps to, and the Incus projects that user is granted. Distinct
// from Principal, which is this broker's own authenticated caller.
//
// It lived in internal/routebroker — the v1 public-route broker — and the v2
// project broker is now its only consumer, so it moved here when v1 was
// removed (#52).
type TrustPrincipal struct {
	Fingerprint string   `json:"fingerprint"`
	User        string   `json:"user"`
	Projects    []string `json:"projects,omitempty"`
}
