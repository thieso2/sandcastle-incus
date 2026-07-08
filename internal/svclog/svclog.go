// Package svclog is the shared structured-logging layer for Sandcastle's
// long-running services (the Auth App and the brokers). It emits verbose,
// timestamped log lines with per-request and per-work-span durations to an
// io.Writer (stderr → journald under systemd) and, optionally, to a Sink for
// persistence (the Auth App's SQLite logs table).
//
// The design keeps identity attribution honest: the HTTP middleware knows the
// method/path/status/duration of a request but not *who* made it — that is
// resolved inside handlers. So the middleware installs a mutable, request-scoped
// record into the request context, and handlers enrich it (SetUser) when they
// resolve identity. Work spans (Span) inherit the request's id and user, so a
// single instrumentation point per identity choke attributes every line for
// free.
package svclog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Kind classifies a log Entry.
const (
	KindRequest = "request"
	KindSpan    = "span"
	KindMessage = "message"
)

// Entry is a single structured log record. It is the unit both the stderr
// formatter and any Sink receive.
type Entry struct {
	Time       time.Time
	Level      string
	Service    string
	Kind       string
	Event      string
	RequestID  string
	UserKey    string
	Method     string
	Path       string
	Status     int
	DurationMS int64
	Detail     string
}

// Sink is an optional persistence target for entries. A nil Sink means
// stderr-only logging. Implementations must be safe for concurrent use and
// should not block (the Auth App's sink buffers writes on a goroutine).
type Sink interface {
	Save(context.Context, Entry)
}

// Logger formats entries to an io.Writer and, if configured, forwards them to a
// Sink. A Logger is safe for concurrent use.
type Logger struct {
	service string
	mu      sync.Mutex
	out     io.Writer
	sink    Sink
}

// New constructs a Logger for the named service. out is where verbose lines are
// written (typically os.Stderr); sink may be nil for stderr-only logging.
func New(service string, out io.Writer, sink Sink) *Logger {
	return &Logger{service: strings.TrimSpace(service), out: out, sink: sink}
}

// emit writes an entry to the configured writer and forwards it to the sink.
func (l *Logger) emit(ctx context.Context, e Entry) {
	if l == nil {
		return
	}
	if e.Service == "" {
		e.Service = l.service
	}
	if e.Level == "" {
		e.Level = "INFO"
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if l.out != nil {
		line := formatLine(e)
		l.mu.Lock()
		_, _ = io.WriteString(l.out, line)
		l.mu.Unlock()
	}
	if l.sink != nil {
		l.sink.Save(ctx, e)
	}
}

// formatLine renders a compact, greppable single line, e.g.:
//
//	2026-07-08T12:00:01Z INFO auth-app request GET /machines status=200 dur=42ms user=alice req=ab12cd34
func formatLine(e Entry) string {
	var b strings.Builder
	b.WriteString(e.Time.UTC().Format(time.RFC3339))
	b.WriteByte(' ')
	b.WriteString(e.Level)
	b.WriteByte(' ')
	b.WriteString(e.Service)
	b.WriteByte(' ')
	b.WriteString(e.Kind)
	if e.Event != "" {
		b.WriteByte(' ')
		b.WriteString(e.Event)
	}
	if e.Method != "" || e.Path != "" {
		b.WriteByte(' ')
		b.WriteString(strings.TrimSpace(e.Method + " " + e.Path))
	}
	if e.Status != 0 {
		fmt.Fprintf(&b, " status=%d", e.Status)
	}
	fmt.Fprintf(&b, " dur=%s", formatDuration(e.DurationMS))
	if e.UserKey != "" {
		b.WriteString(" user=")
		b.WriteString(e.UserKey)
	}
	if e.RequestID != "" {
		b.WriteString(" req=")
		b.WriteString(e.RequestID)
	}
	if e.Detail != "" {
		b.WriteString(" detail=")
		b.WriteString(quoteDetail(e.Detail))
	}
	b.WriteByte('\n')
	return b.String()
}

func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.2fs", float64(ms)/1000)
}

// quoteDetail keeps a detail string on one line for greppability.
func quoteDetail(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if strings.ContainsAny(s, " \t") {
		return "\"" + strings.ReplaceAll(s, "\"", "'") + "\""
	}
	return s
}

// requestRecord is the mutable per-request state carried in the context. The
// middleware creates it; handlers mutate UserKey via SetUser; Span reads it.
type requestRecord struct {
	logger    *Logger
	requestID string
	mu        sync.Mutex
	userKey   string
}

func (rr *requestRecord) setUser(key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	rr.mu.Lock()
	rr.userKey = key
	rr.mu.Unlock()
}

func (rr *requestRecord) user() string {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return rr.userKey
}

type contextKey struct{}

var recordKey contextKey

// withRecord returns a child context carrying the request record.
func withRecord(ctx context.Context, rr *requestRecord) context.Context {
	return context.WithValue(ctx, recordKey, rr)
}

func recordFrom(ctx context.Context) *requestRecord {
	if ctx == nil {
		return nil
	}
	rr, _ := ctx.Value(recordKey).(*requestRecord)
	return rr
}

// SetUser attributes the current request (and any subsequent spans on it) to the
// given user key. It is a no-op outside an instrumented request or for an empty
// key, so it is safe to call unconditionally from identity-resolution helpers.
func SetUser(ctx context.Context, userKey string) {
	if rr := recordFrom(ctx); rr != nil {
		rr.setUser(userKey)
	}
}

// Span times fn and emits a span entry inheriting the request's id and user. The
// error returned by fn is passed through; on error the entry is recorded at
// ERROR level with the message in Detail. Span is a no-op wrapper (still runs fn)
// when called outside an instrumented request.
func Span(ctx context.Context, event string, fn func() error) error {
	rr := recordFrom(ctx)
	start := time.Now()
	err := fn()
	if rr == nil || rr.logger == nil {
		return err
	}
	e := Entry{
		Kind:       KindSpan,
		Event:      strings.TrimSpace(event),
		RequestID:  rr.requestID,
		UserKey:    rr.user(),
		DurationMS: time.Since(start).Milliseconds(),
	}
	if err != nil {
		e.Level = "ERROR"
		e.Detail = err.Error()
	}
	rr.logger.emit(ctx, e)
	return err
}

// Logf emits an ad-hoc message entry tied to the current request. Outside an
// instrumented request it is a no-op.
func Logf(ctx context.Context, format string, args ...any) {
	rr := recordFrom(ctx)
	if rr == nil || rr.logger == nil {
		return
	}
	rr.logger.emit(ctx, Entry{
		Kind:      KindMessage,
		RequestID: rr.requestID,
		UserKey:   rr.user(),
		Detail:    fmt.Sprintf(format, args...),
	})
}

// Message emits a standalone message entry directly through the logger (not tied
// to a request). Used for lifecycle/operational lines that replace bare
// log.Printf calls.
func (l *Logger) Message(ctx context.Context, level, format string, args ...any) {
	l.emit(ctx, Entry{
		Level:  level,
		Kind:   KindMessage,
		Detail: fmt.Sprintf(format, args...),
	})
}

// newRequestID returns a short random hex id for correlating a request with its
// spans.
func newRequestID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req"
	}
	return hex.EncodeToString(b[:])
}
