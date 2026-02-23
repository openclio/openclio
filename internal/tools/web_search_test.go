package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSearchTool_Name(t *testing.T) {
	tool := NewWebSearchTool("brave", "test-key")
	if tool.Name() != "web_search" {
		t.Errorf("expected 'web_search', got %q", tool.Name())
	}
}

func TestWebSearchTool_Schema(t *testing.T) {
	tool := NewWebSearchTool("brave", "key")
	schema := tool.Schema()
	if len(schema) == 0 {
		t.Fatal("schema should not be empty")
	}
	var m map[string]interface{}
	if err := json.Unmarshal(schema, &m); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
}

func TestWebSearchTool_BraveSearch_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assert correct auth header
		if got := r.Header.Get("X-Subscription-Token"); got != "test-brave-key" {
			t.Errorf("expected X-Subscription-Token: test-brave-key, got %q", got)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"web": {
				"results": [
					{"title": "Go Language", "url": "https://golang.org", "description": "The Go programming language"},
					{"title": "Go Tour", "url": "https://tour.golang.org", "description": "A tour of Go"}
				]
			}
		}`))
	}))
	defer srv.Close()

	tool := &WebSearchTool{
		provider:   "brave",
		apiKey:     "test-brave-key",
		maxResults: 5,
		client:     srv.Client(),
		braveURL:   srv.URL,
	}

	params, _ := json.Marshal(map[string]interface{}{"query": "golang"})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Title:") {
		t.Errorf("expected result to contain 'Title:', got: %s", result)
	}
	if !strings.Contains(result, "URL:") {
		t.Errorf("expected result to contain 'URL:', got: %s", result)
	}
	if !strings.Contains(result, "Snippet:") {
		t.Errorf("expected result to contain 'Snippet:', got: %s", result)
	}
	if !strings.Contains(result, "Go Language") {
		t.Errorf("expected result to contain 'Go Language', got: %s", result)
	}
}

func TestWebSearchTool_BraveSearch_EmptyAPIKey(t *testing.T) {
	tool := &WebSearchTool{
		provider:   "brave",
		apiKey:     "",
		maxResults: 5,
		client:     http.DefaultClient,
	}

	params, _ := json.Marshal(map[string]interface{}{"query": "golang"})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "API key missing or invalid") {
		t.Errorf("expected graceful error message, got: %s", result)
	}
}

func TestWebSearchTool_BraveSearch_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	tool := &WebSearchTool{
		provider:   "brave",
		apiKey:     "bad-key",
		maxResults: 5,
		client:     srv.Client(),
		braveURL:   srv.URL,
	}

	params, _ := json.Marshal(map[string]interface{}{"query": "test"})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "API key missing or invalid") {
		t.Errorf("expected API key error, got: %s", result)
	}
}

func TestWebSearchTool_TavilySearch_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"results": [
				{"title": "Rust Language", "url": "https://rust-lang.org", "content": "Rust is fast and safe"}
			]
		}`))
	}))
	defer srv.Close()

	tool := &WebSearchTool{
		provider:   "tavily",
		apiKey:     "test-tavily-key",
		maxResults: 5,
		client:     srv.Client(),
		tavilyURL:  srv.URL,
	}

	params, _ := json.Marshal(map[string]interface{}{"query": "rust lang", "num_results": 3})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Title:") {
		t.Errorf("expected 'Title:', got: %s", result)
	}
	if !strings.Contains(result, "Rust Language") {
		t.Errorf("expected 'Rust Language', got: %s", result)
	}
}

func TestWebSearchTool_MissingQuery(t *testing.T) {
	tool := NewWebSearchTool("brave", "key")
	params, _ := json.Marshal(map[string]interface{}{})
	_, err := tool.Execute(context.Background(), params)
	if err == nil {
		t.Error("expected error for missing query")
	}
}

func TestWebSearchTool_UnknownProvider(t *testing.T) {
	tool := NewWebSearchTool("unknown-provider", "key")
	params, _ := json.Marshal(map[string]interface{}{"query": "test"})
	_, err := tool.Execute(context.Background(), params)
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}
