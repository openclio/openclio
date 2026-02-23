package gateway

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testResponseWriter struct {
	headers http.Header
	status  int
}

func (w *testResponseWriter) Header() http.Header {
	if w.headers == nil {
		w.headers = make(http.Header)
	}
	return w.headers
}

func (w *testResponseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *testResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}

type hijackableTestWriter struct {
	testResponseWriter
	hijackCalled bool
}

func (w *hijackableTestWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijackCalled = true
	return nil, nil, nil
}

type flushableTestWriter struct {
	testResponseWriter
	flushCalled bool
}

func (w *flushableTestWriter) Flush() {
	w.flushCalled = true
}

func TestResponseRecorderHijackDelegates(t *testing.T) {
	underlying := &hijackableTestWriter{}
	rec := &responseRecorder{
		ResponseWriter: underlying,
		status:         http.StatusOK,
	}

	_, _, err := rec.Hijack()
	if err != nil {
		t.Fatalf("expected no hijack error, got: %v", err)
	}
	if !underlying.hijackCalled {
		t.Fatal("expected underlying Hijack to be called")
	}
}

func TestResponseRecorderHijackReturnsErrorWhenUnsupported(t *testing.T) {
	underlying := &testResponseWriter{}
	rec := &responseRecorder{
		ResponseWriter: underlying,
		status:         http.StatusOK,
	}

	_, _, err := rec.Hijack()
	if err == nil {
		t.Fatal("expected hijack error when underlying writer lacks http.Hijacker")
	}
	if !strings.Contains(err.Error(), "http.Hijacker") {
		t.Fatalf("expected hijacker error message, got: %v", err)
	}
}

func TestResponseRecorderFlushDelegates(t *testing.T) {
	underlying := &flushableTestWriter{}
	rec := &responseRecorder{
		ResponseWriter: underlying,
		status:         http.StatusOK,
	}

	rec.Flush()

	if !underlying.flushCalled {
		t.Fatal("expected underlying Flush to be called")
	}
}

func TestRateLimiterStopIsIdempotent(t *testing.T) {
	rl := NewRateLimiter(5)
	rl.Stop()
	rl.Stop()
}

func TestBodySizeLimitMiddleware(t *testing.T) {
	handler := BodySizeLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	oversized := bytes.Repeat([]byte("x"), int(maxRequestBodyBytes)+1)
	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(oversized))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized payload, got %d", rec.Code)
	}
}
