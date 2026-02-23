package gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthMiddleware(t *testing.T) {
	token := "test-secret-token"
	handler := AuthMiddleware(token)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	// No token — should 401
	req := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", rec.Code)
	}

	// Wrong token — should 401
	req = httptest.NewRequest("GET", "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong token, got %d", rec.Code)
	}

	// Correct token — should 200
	req = httptest.NewRequest("GET", "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with correct token, got %d", rec.Code)
	}

	// Health endpoint — always accessible without token
	req = httptest.NewRequest("GET", "/api/v1/health", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("health should be accessible without token, got %d", rec.Code)
	}

	// Query token should authorize webchat bootstrap path.
	req = httptest.NewRequest("GET", "/?token="+token, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 when using query token on root path, got %d", rec.Code)
	}

	// Query token should NOT authorize regular API endpoints.
	req = httptest.NewRequest("GET", "/api/v1/sessions?token="+token, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 when using query token on non-ws endpoint, got %d", rec.Code)
	}

	// Query token remains valid for websocket handshake path.
	req = httptest.NewRequest("GET", "/ws?token="+token, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for websocket query token auth, got %d", rec.Code)
	}
}

func TestGenerateToken(t *testing.T) {
	t1, err := GenerateToken()
	if err != nil {
		t.Fatalf("unexpected error generating token 1: %v", err)
	}
	t2, err := GenerateToken()
	if err != nil {
		t.Fatalf("unexpected error generating token 2: %v", err)
	}

	if len(t1) != 64 { // 32 bytes hex = 64 chars
		t.Errorf("expected 64 char token, got %d", len(t1))
	}
	if t1 == t2 {
		t.Error("tokens should be unique")
	}
}

func TestGenerateTokenRandomFailure(t *testing.T) {
	prev := randReader
	defer func() { randReader = prev }()
	randReader = io.Reader(failingReader{})

	if _, err := GenerateToken(); err == nil {
		t.Fatal("expected token generation error when random source fails")
	}
}

func TestLoadOrCreateToken(t *testing.T) {
	dir := t.TempDir()

	// First call creates token
	token1, err := LoadOrCreateToken(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token1 == "" {
		t.Fatal("token should not be empty")
	}

	// Second call loads same token
	token2, err := LoadOrCreateToken(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token1 != token2 {
		t.Error("should load the same token on second call")
	}
}

type failingReader struct{}

func (f failingReader) Read(_ []byte) (int, error) {
	return 0, errors.New("random unavailable")
}

func TestHealthEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	handlers := &Handlers{}
	mux.HandleFunc("/api/v1/health", handlers.Health)

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("expected status ok, got %s", rec.Body.String())
	}
}

func TestChatEndpointUnavailableWithoutAgent(t *testing.T) {
	mux := http.NewServeMux()
	handlers := &Handlers{}
	mux.HandleFunc("/api/v1/chat", handlers.Chat)

	req := httptest.NewRequest("POST", "/api/v1/chat", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when agent is unavailable, got %d", rec.Code)
	}
}

func TestChatEndpointWrongMethod(t *testing.T) {
	mux := http.NewServeMux()
	handlers := &Handlers{}
	mux.HandleFunc("/api/v1/chat", handlers.Chat)

	req := httptest.NewRequest("GET", "/api/v1/chat", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET, got %d", rec.Code)
	}
}
