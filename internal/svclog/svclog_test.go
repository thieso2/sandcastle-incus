package svclog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureSink records entries for assertions.
type captureSink struct {
	mu      sync.Mutex
	entries []Entry
}

func (c *captureSink) Save(_ context.Context, e Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, e)
}

func (c *captureSink) all() []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Entry, len(c.entries))
	copy(out, c.entries)
	return out
}

func TestHTTPRecordsRequestStatusAndDuration(t *testing.T) {
	sink := &captureSink{}
	var out strings.Builder
	logger := New("test", &out, sink)

	handler := logger.HTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetUser(r.Context(), "alice")
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest(http.MethodGet, "/machines", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	entries := sink.all()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Kind != KindRequest {
		t.Errorf("kind = %q, want %q", e.Kind, KindRequest)
	}
	if e.Status != http.StatusTeapot {
		t.Errorf("status = %d, want %d", e.Status, http.StatusTeapot)
	}
	if e.Method != http.MethodGet || e.Path != "/machines" {
		t.Errorf("method/path = %s %s", e.Method, e.Path)
	}
	if e.UserKey != "alice" {
		t.Errorf("user = %q, want alice", e.UserKey)
	}
	if e.DurationMS < 1 {
		t.Errorf("duration = %dms, want >= 1", e.DurationMS)
	}
	if !strings.Contains(out.String(), "GET /machines") {
		t.Errorf("stderr line missing request: %q", out.String())
	}
}

func TestSpanInheritsRequestUserAndTimesWork(t *testing.T) {
	sink := &captureSink{}
	logger := New("test", nil, sink)

	handler := logger.HTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetUser(r.Context(), "bob")
		_ = Span(r.Context(), "provision.machine", func() error {
			time.Sleep(3 * time.Millisecond)
			return nil
		})
		_ = Span(r.Context(), "incus.create", func() error {
			return errors.New("boom")
		})
		w.WriteHeader(http.StatusOK)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/x", nil))

	entries := sink.all()
	var spans []Entry
	var request *Entry
	for i := range entries {
		switch entries[i].Kind {
		case KindSpan:
			spans = append(spans, entries[i])
		case KindRequest:
			request = &entries[i]
		}
	}
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}
	if request == nil {
		t.Fatal("expected a request entry")
	}
	for _, s := range spans {
		if s.UserKey != "bob" {
			t.Errorf("span %q user = %q, want bob", s.Event, s.UserKey)
		}
		if s.RequestID == "" || s.RequestID != request.RequestID {
			t.Errorf("span %q request id = %q, want %q", s.Event, s.RequestID, request.RequestID)
		}
	}
	// The provision span slept; the incus span errored.
	byEvent := map[string]Entry{}
	for _, s := range spans {
		byEvent[s.Event] = s
	}
	if byEvent["provision.machine"].DurationMS < 1 {
		t.Errorf("provision span duration = %d, want >= 1", byEvent["provision.machine"].DurationMS)
	}
	if byEvent["incus.create"].Level != "ERROR" || byEvent["incus.create"].Detail != "boom" {
		t.Errorf("incus.create span = %+v, want ERROR/boom", byEvent["incus.create"])
	}
}

func TestSpanReturnsErrorAndRunsWithoutRecord(t *testing.T) {
	want := errors.New("still runs")
	ran := false
	got := Span(context.Background(), "orphan", func() error {
		ran = true
		return want
	})
	if !ran {
		t.Fatal("fn did not run outside an instrumented request")
	}
	if !errors.Is(got, want) {
		t.Fatalf("Span returned %v, want %v", got, want)
	}
}

func TestFormatLineIsSingleLineAndGreppable(t *testing.T) {
	line := formatLine(Entry{
		Time:       time.Unix(0, 0),
		Level:      "INFO",
		Service:    "auth-app",
		Kind:       KindSpan,
		Event:      "issue.workload_token",
		UserKey:    "carol",
		RequestID:  "abcd1234",
		DurationMS: 1500,
		Detail:     "multi\nline detail",
	})
	if strings.Count(line, "\n") != 1 || !strings.HasSuffix(line, "\n") {
		t.Fatalf("line is not exactly one physical line: %q", line)
	}
	for _, want := range []string{"auth-app", "issue.workload_token", "user=carol", "req=abcd1234", "dur=1.50s"} {
		if !strings.Contains(line, want) {
			t.Errorf("line missing %q: %s", want, line)
		}
	}
}

func ExampleLogger_Message() {
	logger := New("demo", nil, nil)
	logger.Message(context.Background(), "WARN", "simulated mode enabled: %s", "danger")
	// Output:
}
