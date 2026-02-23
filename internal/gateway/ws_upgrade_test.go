package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestWebSocketUpgradeThroughMiddlewareChain(t *testing.T) {
	const token = "test-token"

	mux := http.NewServeMux()
	h := &Handlers{}
	mux.HandleFunc("/ws", h.HandleWebSocket)

	// Match production middleware order from gateway.NewServer.
	var handler http.Handler = mux
	rl := NewRateLimiter(100)
	defer rl.Stop()
	handler = rl.Middleware(handler)
	handler = BodySizeLimitMiddleware(handler)
	handler = CORSMiddleware(handler)
	handler = SecurityHeadersMiddleware(handler)
	handler = AuthMiddleware(token)(handler)
	handler = RequestIDMiddleware(handler)
	handler = RequestLoggerMiddleware(handler)

	srv := newTestServerOrSkip(t, handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=" + token
	header := http.Header{}
	header.Set("Origin", "http://127.0.0.1")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("websocket upgrade failed: err=%v status=%d", err, status)
	}
	defer conn.Close()
}

func newTestServerOrSkip(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	var srv *httptest.Server
	func() {
		defer func() {
			if r := recover(); r != nil {
				srv = nil
			}
		}()
		srv = httptest.NewServer(handler)
	}()

	if srv == nil {
		t.Skip("skipping websocket upgrade test: local listener is unavailable in this environment")
	}
	if srv.URL == "" {
		t.Skip("skipping websocket upgrade test: test server did not start")
	}
	if !strings.HasPrefix(srv.URL, "http://") && !strings.HasPrefix(srv.URL, "https://") {
		t.Skipf("skipping websocket upgrade test: unexpected test server URL %q", srv.URL)
	}
	return srv
}
