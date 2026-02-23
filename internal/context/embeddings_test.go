package context

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaEmbedderEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2,0.3]]}`))
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "nomic-embed-text")
	vec, err := e.Embed("hello")
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("expected 3-dim embedding, got %d", len(vec))
	}
	if e.Dimensions() != 3 {
		t.Fatalf("expected dimensions to update to 3, got %d", e.Dimensions())
	}
}
