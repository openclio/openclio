// Package logger provides a structured logger for the agent.
// It wraps slog with secret scrubbing, log levels, and trace ID support.
package logger

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
)

// contextKey is used to store trace IDs in context.
type contextKey string

const traceIDKey contextKey = "trace_id"

// sensitivePatterns are regex patterns for values that should be redacted.
var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[a-zA-Z0-9\-_]{20,}`),                 // OpenAI keys
	regexp.MustCompile(`sk-ant-[a-zA-Z0-9\-_]{20,}`),             // Anthropic keys
	regexp.MustCompile(`xai-[a-zA-Z0-9\-_]{20,}`),                // xAI keys
	regexp.MustCompile(`(Bearer\s+)[a-zA-Z0-9\-_.]{20,}`),        // Bearer tokens
	regexp.MustCompile(`(x-api-key:\s*)[a-zA-Z0-9\-_.]{20,}`),    // Header API keys
	regexp.MustCompile(`([Aa][Pp][Ii]_?[Kk][Ee][Yy]=)[^\s&"']+`), // ENV style API_KEY=value
}

// ScrubSecrets redacts sensitive patterns from a string.
func ScrubSecrets(s string) string {
	for _, re := range sensitivePatterns {
		s = re.ReplaceAllStringFunc(s, func(match string) string {
			// Preserve any prefix group (e.g., "Bearer ", "API_KEY=")
			for i := 1; i <= re.NumSubexp(); i++ {
				sub := re.FindStringSubmatch(match)
				if len(sub) > i && sub[i] != "" {
					return sub[i] + "[REDACTED]"
				}
			}
			// Show only first 4 chars + redacted
			if len(match) > 4 {
				return match[:4] + "[REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return s
}

// scrubbingHandler wraps an slog.Handler to auto-scrub attribute values.
type scrubbingHandler struct {
	inner slog.Handler
}

func (h *scrubbingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *scrubbingHandler) Handle(ctx context.Context, r slog.Record) error {
	// Scrub the message
	r.Message = ScrubSecrets(r.Message)

	// Scrub all attributes
	var scrubbed []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		if s, ok := a.Value.Any().(string); ok {
			a = slog.String(a.Key, ScrubSecrets(s))
		}
		scrubbed = append(scrubbed, a)
		return true
	})

	// Inject trace ID from context if present
	if traceID, ok := ctx.Value(traceIDKey).(string); ok && traceID != "" {
		scrubbed = append([]slog.Attr{slog.String("trace_id", traceID)}, scrubbed...)
	}

	// Build new record with scrubbed values
	newRecord := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	for _, a := range scrubbed {
		newRecord.AddAttrs(a)
	}

	return h.inner.Handle(ctx, newRecord)
}

func (h *scrubbingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &scrubbingHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *scrubbingHandler) WithGroup(name string) slog.Handler {
	return &scrubbingHandler{inner: h.inner.WithGroup(name)}
}

// New creates a new structured logger.
// level: "debug", "info", "warn", "error"
// output: path to log file, or "" / "stderr" for stderr
func New(level, output string) *slog.Logger {
	var w io.Writer = os.Stderr

	// Expand ~ to user home dir
	if strings.HasPrefix(output, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			output = strings.Replace(output, "~", home, 1)
		}
	}

	if output != "" && output != "stderr" && output != "stdout" {
		// Ensure output directory exists before opening
		if idx := strings.LastIndex(output, "/"); idx != -1 {
			os.MkdirAll(output[:idx], 0755)
		}

		rw, err := NewRollingWriter(output, 0, 0)
		if err == nil {
			w = rw
		} else {
			// Fallback if rotation init fails
			fmt.Fprintf(os.Stderr, "logger error: failed to initialize rolling writer: %v\n", err)
			f, err := os.OpenFile(output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if err == nil {
				w = f
			}
		}
	} else if output == "stdout" {
		w = os.Stdout
	}

	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}
	inner := slog.NewJSONHandler(w, opts)

	return slog.New(&scrubbingHandler{inner: inner})
}

// WithTraceID returns a context with a trace ID attached.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// TraceID extracts the trace ID from a context.
func TraceID(ctx context.Context) string {
	if id, ok := ctx.Value(traceIDKey).(string); ok {
		return id
	}
	return ""
}

// Global is the default logger (initialized at startup).
var Global *slog.Logger

func init() {
	// If running under 'go test', divert logs to test.log so stdout is clean
	if flag.Lookup("test.v") != nil {
		os.MkdirAll("../../test-data", 0755) // Try creating if we're inside internal/
		os.MkdirAll("test-data", 0755)       // Try creating if we're at root

		path := "test-data/test.log"
		if _, err := os.Stat("../../test-data"); err == nil {
			path = "../../test-data/test.log"
		}

		Global = New("debug", path)
	} else {
		Global = New("info", "stderr")
	}
}

// Logger is a type alias for *slog.Logger for convenience.
type Logger = slog.Logger

// AsLogger converts a *Logger (i.e. *slog.Logger) to *slog.Logger (identity).
func AsLogger(l *slog.Logger) *slog.Logger { return l }
