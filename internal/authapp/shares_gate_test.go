package authapp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Tenant Storage Shares are gated off on v2 (#70): every share endpoint answers
// 501, so no forged registry can be turned into a mount through the sanctioned
// path. The handlers and plumbing stay intact behind the gate.
func TestShareEndpointsAreGatedOnV2(t *testing.T) {
	db := authDBForTest(t)
	handler := NewHandler(db, HandlerOptions{AuthHostname: "auth.example.com"})
	for _, path := range []string{
		"/api/shares", "/api/shares/status", "/api/shares/accept", "/api/shares/decline",
		"/api/shares/revoke", "/api/shares/delete", "/api/shares/reconcile",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, httptest.NewRequest(method, path, nil))
			if res.Code != http.StatusNotImplemented {
				t.Fatalf("%s %s -> %d, want 501", method, path, res.Code)
			}
		}
	}
}
