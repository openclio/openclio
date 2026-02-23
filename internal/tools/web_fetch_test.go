package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockBrowserTool struct {
	out string
	err error
}

func (m mockBrowserTool) Name() string            { return "browser" }
func (m mockBrowserTool) Description() string     { return "mock browser" }
func (m mockBrowserTool) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (m mockBrowserTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	return m.out, m.err
}

func TestWebFetchFlightURLReturnsDynamicHint(t *testing.T) {
	tool := NewWebFetchTool()
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"https://www.google.com/flights?hl=en"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "[web_fetch limitation]") {
		t.Fatalf("expected limitation hint, got: %s", out)
	}
	if !strings.Contains(out, `action="browse"`) {
		t.Fatalf("expected browser hint, got: %s", out)
	}
}

func TestWebFetchDetectsBlockedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("captcha required"))
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "[HTTP 403]") || !strings.Contains(out, "[web_fetch limitation]") {
		t.Fatalf("expected blocked-page hint, got: %s", out)
	}
}

func TestWebFetchDetectsJSRenderedPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head>
			<script src="a.js"></script><script src="b.js"></script><script src="c.js"></script>
			<script src="d.js"></script><script src="e.js"></script><script src="f.js"></script>
			<script src="g.js"></script><script src="h.js"></script><script src="i.js"></script>
			</head><body><div id="root"></div></body></html>`))
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "[web_fetch limitation]") {
		t.Fatalf("expected JS-rendered hint, got: %s", out)
	}
}

func TestWebFetchReturnsStaticContentAndRealisticUA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("User-Agent"), "Mozilla/5.0") {
			t.Fatalf("expected browser-like user agent, got %q", r.Header.Get("User-Agent"))
		}
		_, _ = w.Write([]byte("hello static"))
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "[HTTP 200]") || !strings.Contains(out, "hello static") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestWebFetchUsesBrowserFallbackForDynamicURLs(t *testing.T) {
	tool := NewWebFetchTool()
	tool.SetBrowserFallback(mockBrowserTool{out: "rendered flight results"})

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"https://www.google.com/flights?hl=en"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "[web_fetch dynamic fallback -> browser]") {
		t.Fatalf("expected browser fallback marker, got: %s", out)
	}
	if !strings.Contains(out, "rendered flight results") {
		t.Fatalf("expected browser output, got: %s", out)
	}
}
