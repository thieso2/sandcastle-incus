package svclog

import (
	"net/http"
	"time"
)

// statusRecorder wraps an http.ResponseWriter to capture the status code and
// response size for the request log line.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		// Default status when the handler writes a body without WriteHeader.
		s.status = http.StatusOK
		s.wrote = true
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Flush forwards to the underlying writer when it supports flushing (e.g. the
// OIDC/SSE paths), so wrapping does not break streaming handlers.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// HTTP wraps next with request logging. It installs a request-scoped record into
// the context (so handlers can SetUser / Span), times the request, and emits one
// request entry on completion carrying method, path, status, duration, and the
// resolved user.
func (l *Logger) HTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rr := &requestRecord{logger: l, requestID: newRequestID()}
		ctx := withRecord(r.Context(), rr)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r.WithContext(ctx))
		l.emit(ctx, Entry{
			Kind:       KindRequest,
			RequestID:  rr.requestID,
			UserKey:    rr.user(),
			Method:     r.Method,
			Path:       r.URL.Path,
			Status:     rec.status,
			DurationMS: time.Since(start).Milliseconds(),
		})
	})
}
