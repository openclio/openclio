// Package webchat provides both the embedded web chat UI handler and a
// plugin.Adapter that bridges the gateway WebSocket to the agent message bus.
package webchat

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/openclio/openclio/internal/plugin"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			return false
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		switch strings.ToLower(u.Hostname()) {
		case "localhost", "127.0.0.1", "::1":
			return true
		default:
			return false
		}
	},
}

// Adapter bridges the web chat WebSocket to the agent plugin message bus.
// It satisfies the plugin.Adapter interface so that Agent responses can be
// delivered back to connected browser clients.
type Adapter struct {
	mu       sync.RWMutex
	clients  map[string]*websocket.Conn // chatID → WS connection
	inbound  chan<- plugin.InboundMessage
	outbound <-chan plugin.OutboundMessage
	done     chan struct{}
	healthy  bool
}

// NewAdapter creates a new Webchat adapter.
func NewAdapter() *Adapter {
	return &Adapter{
		clients: make(map[string]*websocket.Conn),
		done:    make(chan struct{}),
		healthy: true,
	}
}

// Name returns "webchat".
func (a *Adapter) Name() string { return "webchat" }

// Health returns nil when the adapter is ready to accept connections.
func (a *Adapter) Health() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.healthy {
		return fmt.Errorf("webchat adapter has been stopped")
	}
	return nil
}

// Stop signals the adapter to stop accepting new connections.
func (a *Adapter) Stop() {
	a.mu.Lock()
	a.healthy = false
	a.mu.Unlock()

	select {
	case <-a.done:
	default:
		close(a.done)
	}

	// Close all active connections
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, conn := range a.clients {
		conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "server shutting down"),
			time.Now().Add(time.Second),
		)
		conn.Close()
		delete(a.clients, id)
	}
}

// Start begins routing messages between WebSocket clients and the agent.
// inbound: channel to push messages received from browser clients.
// outbound: channel of agent responses to deliver back to clients.
func (a *Adapter) Start(ctx context.Context, inbound chan<- plugin.InboundMessage, outbound <-chan plugin.OutboundMessage) error {
	a.mu.Lock()
	a.inbound = inbound
	a.outbound = outbound
	a.mu.Unlock()

	// Forward agent responses to the correct browser client
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-a.done:
			return nil
		case msg := <-outbound:
			a.mu.RLock()
			conn, ok := a.clients[msg.ChatID]
			a.mu.RUnlock()
			if !ok {
				continue
			}
			if err := conn.WriteJSON(map[string]string{
				"role":    "assistant",
				"content": msg.Text,
			}); err != nil {
				// Client disconnected — clean up
				a.mu.Lock()
				conn.Close()
				delete(a.clients, msg.ChatID)
				a.mu.Unlock()
			}
		}
	}
}

// ServeWS is an HTTP handler that upgrades a connection to WebSocket,
// registers the client, and delivers messages from the browser into the
// plugin.InboundMessage channel for the router to dispatch to the agent.
//
// This handler must be registered on the HTTP mux — typically at /chat.
func (a *Adapter) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "WebSocket upgrade failed", http.StatusBadRequest)
		return
	}

	// Use remote address as stable chat/user ID for this session
	chatID := r.RemoteAddr
	userID := chatID

	a.mu.Lock()
	a.clients[chatID] = conn
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		conn.Close()
		delete(a.clients, chatID)
		a.mu.Unlock()
	}()

	// Read loop
	for {
		var msg struct {
			Content string `json:"content"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			return // client disconnected
		}
		if msg.Content == "" {
			continue
		}

		inbound := a.inbound
		if inbound == nil {
			continue
		}

		select {
		case <-a.done:
			return
		case inbound <- plugin.InboundMessage{
			AdapterName: a.Name(),
			UserID:      userID,
			ChatID:      chatID,
			Text:        msg.Content,
		}:
		}
	}
}
