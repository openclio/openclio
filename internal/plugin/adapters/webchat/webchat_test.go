package webchat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openclio/openclio/internal/plugin"
)

func TestHandler_NoCacheHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html content type, got %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("expected no-store cache control, got %q", cc)
	}
	if rec.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("expected pragma no-cache, got %q", rec.Header().Get("Pragma"))
	}
}

func TestWebChatAdapter_Name(t *testing.T) {
	a := NewAdapter()
	if a.Name() != "webchat" {
		t.Errorf("expected 'webchat', got %q", a.Name())
	}
}

func TestWebChatAdapter_Health_AfterNew(t *testing.T) {
	a := NewAdapter()
	// Adapter starts healthy=true after NewAdapter()
	if err := a.Health(); err != nil {
		t.Errorf("expected healthy after NewAdapter(), got error: %v", err)
	}
}

func TestWebChatAdapter_Health_AfterStop(t *testing.T) {
	a := NewAdapter()
	a.Stop()
	err := a.Health()
	if err == nil {
		t.Error("expected error after Stop()")
	}
}

func TestWebChatAdapter_Stop_Idempotent(t *testing.T) {
	a := NewAdapter()
	// Stop twice should not panic
	a.Stop()
	a.Stop()
	err := a.Health()
	if err == nil {
		t.Error("expected error after double Stop()")
	}
}

func TestWebChatAdapter_StartAndStop(t *testing.T) {
	a := NewAdapter()
	inbound := make(chan plugin.InboundMessage, 4)
	outbound := make(chan plugin.OutboundMessage, 4)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- a.Start(ctx, inbound, outbound)
	}()

	// Let it run briefly
	time.Sleep(10 * time.Millisecond)

	// Stop via context
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start() returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("Start() did not return after context cancellation")
	}
}

func TestWebChatAdapter_OutboundRouting(t *testing.T) {
	a := NewAdapter()
	inbound := make(chan plugin.InboundMessage, 4)
	outbound := make(chan plugin.OutboundMessage, 4)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the adapter
	go a.Start(ctx, inbound, outbound)
	time.Sleep(10 * time.Millisecond)

	// Create a test HTTP server and WebSocket client
	srv := httptest.NewServer(http.HandlerFunc(a.ServeWS))
	defer srv.Close()

	// Connect a WebSocket client
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	header := http.Header{}
	header.Set("Origin", "http://127.0.0.1")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("failed to dial WebSocket: %v", err)
	}
	defer conn.Close()

	// Give the server time to register the client
	time.Sleep(50 * time.Millisecond)

	// Send an outbound message to the client's chatID (which is the remote addr)
	// Since we don't know the exact remote addr, we can read the client map
	// Instead, send a message from the client side and capture the chatID
	msg := struct {
		Content string `json:"content"`
	}{Content: "hello from client"}

	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("failed to send WebSocket message: %v", err)
	}

	// Wait for inbound message
	select {
	case m := <-inbound:
		if m.Text != "hello from client" {
			t.Errorf("expected 'hello from client', got %q", m.Text)
		}
		if m.AdapterName != "webchat" {
			t.Errorf("expected AdapterName 'webchat', got %q", m.AdapterName)
		}
		// Now send a response back
		outbound <- plugin.OutboundMessage{
			ChatID: m.ChatID,
			Text:   "reply from server",
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for inbound message")
	}

	// Read the reply from the WebSocket client
	var reply struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&reply); err != nil {
		t.Fatalf("failed to read WebSocket reply: %v", err)
	}
	if reply.Content != "reply from server" {
		t.Errorf("expected 'reply from server', got %q", reply.Content)
	}
	if reply.Role != "assistant" {
		t.Errorf("expected role 'assistant', got %q", reply.Role)
	}
}
