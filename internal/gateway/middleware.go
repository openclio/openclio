package gateway

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openclio/openclio/internal/logger"
)

// RateLimiter tracks request counts per IP.
type RateLimiter struct {
	mu        sync.Mutex
	counts    map[string]*ipCounter
	maxPerMin int
	done      chan struct{} // closed to stop the cleanup goroutine
	stopped   chan struct{} // closed when cleanup goroutine exits
	stopOnce  sync.Once
}

const maxRequestBodyBytes int64 = 10 << 20 // 10MB

type ipCounter struct {
	count    int
	windowAt time.Time
}

// RateLimitMiddleware returns a middleware that limits requests per IP.
// maxPerMin: maximum requests per minute per IP address.
// For server lifecycle control, prefer NewRateLimiter + rl.Middleware + rl.Stop.
func RateLimitMiddleware(maxPerMin int) func(http.Handler) http.Handler {
	return NewRateLimiter(maxPerMin).Middleware
}

// NewRateLimiter constructs a limiter and starts its cleanup goroutine.
func NewRateLimiter(maxPerMin int) *RateLimiter {
	rl := &RateLimiter{
		counts:    make(map[string]*ipCounter),
		maxPerMin: maxPerMin,
		done:      make(chan struct{}),
		stopped:   make(chan struct{}),
	}
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		defer ticker.Stop()
		defer close(rl.stopped)
		for {
			select {
			case <-rl.done:
				return
			case <-ticker.C:
				rl.mu.Lock()
				now := time.Now()
				for ip, c := range rl.counts {
					if now.Sub(c.windowAt) > time.Minute {
						delete(rl.counts, ip)
					}
				}
				rl.mu.Unlock()
			}
		}
	}()

	return rl
}

// Middleware applies per-IP request throttling.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always allow health checks
		if r.URL.Path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}

		ip := realIP(r)

		rl.mu.Lock()
		c, ok := rl.counts[ip]
		if !ok || time.Since(c.windowAt) > time.Minute {
			rl.counts[ip] = &ipCounter{count: 1, windowAt: time.Now()}
			rl.mu.Unlock()
			next.ServeHTTP(w, r)
			return
		}
		c.count++
		over := c.count > rl.maxPerMin
		rl.mu.Unlock()

		if over {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded — try again in 60s")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Stop terminates the cleanup goroutine. Safe to call multiple times.
func (rl *RateLimiter) Stop() {
	if rl == nil {
		return
	}
	rl.stopOnce.Do(func() {
		close(rl.done)
		<-rl.stopped
	})
}

// BodySizeLimitMiddleware caps HTTP request bodies to 10MB.
func BodySizeLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// CORSMiddleware adds CORS headers (strict: same-origin + localhost only).
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		// Allow localhost and 127.0.0.1 origins only
		if origin == "" || isLocalOrigin(origin) {
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SecurityHeadersMiddleware adds standard security headers.
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://cdnjs.cloudflare.com; "+
				"style-src 'self' 'unsafe-inline' https://cdnjs.cloudflare.com; "+
				"img-src 'self' data:; connect-src 'self' ws: wss:")
		next.ServeHTTP(w, r)
	})
}

// RequestIDMiddleware generates a trace ID and attaches it to the request context and headers.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = uuid.New().String()
		}

		// Set on response
		w.Header().Set("X-Request-ID", reqID)

		// Set in logger context
		ctx := logger.WithTraceID(r.Context(), reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// responseRecorder captures the status code for logging.
type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the underlying writer for stdlib ResponseController helpers.
func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// Hijack forwards WebSocket/HTTP upgrade hijacking to the underlying writer.
func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
	}
	return h.Hijack()
}

// Flush forwards streaming flushes (SSE/chunked responses) to the underlying writer.
func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// RequestLoggerMiddleware logs HTTP requests with their duration and trace ID.
func RequestLoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		rec := &responseRecorder{
			ResponseWriter: w,
			status:         http.StatusOK, // default if WriteHeader is not called
		}

		next.ServeHTTP(rec, r)

		duration := time.Since(start)

		logger.Global.InfoContext(r.Context(), "http access",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("ip", realIP(r)),
			slog.Int("status", rec.status),
			slog.Duration("duration", duration),
		)
	})
}

// realIP extracts the real client IP, respecting X-Forwarded-For if set.
func realIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return fwd
	}
	if fwd := r.Header.Get("X-Real-IP"); fwd != "" {
		return fwd
	}
	// Strip port from RemoteAddr
	host := r.RemoteAddr
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
}

func isLocalOrigin(origin string) bool {
	u, err := url.Parse(strings.TrimSpace(origin))
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
