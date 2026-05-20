package routebroker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/route"
)

func TestClientAddsRouteThroughBroker(t *testing.T) {
	var captured routeRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/routes" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	err := (Client{BaseURL: server.URL, HTTPClient: server.Client()}).Add(context.Background(), route.AddPlan{
		Hostname:        "app.example.com",
		TargetReference: "alice/myproject/codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.Hostname != "app.example.com" || captured.TargetReference != "alice/myproject/codex" {
		t.Fatalf("captured = %#v", captured)
	}
}

func TestClientRemovesRouteThroughBroker(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/routes/app.example.com" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := (Client{BaseURL: server.URL, HTTPClient: server.Client()}).Remove(context.Background(), route.RemovePlan{
		Hostname: "app.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("expected delete request")
	}
}

func TestClientListsRoutesThroughBroker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/routes" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(route.ListResult{Routes: []route.Route{{
			Hostname:        "app.example.com",
			TargetReference: "alice/myproject/codex",
			RoutePort:       3000,
		}}})
	}))
	defer server.Close()

	result, err := (Client{BaseURL: server.URL, HTTPClient: server.Client()}).List(context.Background(), route.ListPlan{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 1 || result.Routes[0].Hostname != "app.example.com" {
		t.Fatalf("routes = %#v", result.Routes)
	}
}

func TestClientRequiresBrokerURL(t *testing.T) {
	err := (Client{}).Add(context.Background(), route.AddPlan{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClientReportsBrokerJSONError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "route owner mismatch"})
	}))
	defer server.Close()

	err := (Client{BaseURL: server.URL, HTTPClient: server.Client()}).Remove(context.Background(), route.RemovePlan{
		Hostname: "app.example.com",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "route owner mismatch") {
		t.Fatalf("error = %q", err.Error())
	}
	if strings.Contains(err.Error(), "{\"error\"") {
		t.Fatalf("error should use decoded JSON message, got %q", err.Error())
	}
}
