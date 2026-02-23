package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/storage"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			return false
		}
		return isLocalOrigin(origin)
	},
}

// WSMessage is a WebSocket message from client.
type WSMessage struct {
	Type      string `json:"type"` // "chat", "ping", "abort", "inject"
	Message   string `json:"message,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Role      string `json:"role,omitempty"`    // used by "inject"
	Content   string `json:"content,omitempty"` // used by "inject"
}

// WSResponse is a WebSocket message to client.
type WSResponse struct {
	Type      string      `json:"type"` // "response", "token", "tool_use", "error", "pong", "session", "control"
	Content   string      `json:"content,omitempty"`
	Tool      string      `json:"tool,omitempty"`
	SessionID string      `json:"session_id,omitempty"`
	Usage     interface{} `json:"usage,omitempty"`
	Error     string      `json:"error,omitempty"`
}

type safeWSConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *safeWSConn) WriteJSON(v interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(v)
}

// HandleWebSocket upgrades to WebSocket and handles bidirectional chat.
func (h *Handlers) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v (proto=%s writer=%T)", err, r.Proto, w)
		return
	}
	defer conn.Close()
	sconn := &safeWSConn{conn: conn}

	log.Printf("WebSocket client connected from %s", r.RemoteAddr)

	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		var msg WSMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			sendWSError(sconn, "invalid message format")
			continue
		}

		switch msg.Type {
		case "ping":
			sendWSResponse(sconn, WSResponse{Type: "pong"})

		case "chat":
			go h.handleWSChat(sconn, msg)

		case "abort":
			h.handleWSAbort(sconn, msg)

		case "inject":
			h.handleWSInject(sconn, msg)

		default:
			sendWSError(sconn, fmt.Sprintf("unknown message type: %s", msg.Type))
		}
	}

	log.Printf("WebSocket client disconnected")
}

func (h *Handlers) handleWSChat(conn *safeWSConn, msg WSMessage) {
	if msg.Message == "" {
		sendWSError(conn, "message is required")
		return
	}
	if required, _ := h.setupState(); required {
		sendWSError(conn, "setup required: configure provider via /api/v1/setup")
		return
	}
	if h.agent == nil {
		sendWSError(conn, "agent is unavailable")
		return
	}

	// Get or create session
	sessionID := msg.SessionID
	if sessionID == "" {
		session, err := h.sessions.Create("ws", "ws-user")
		if err != nil {
			sendWSError(conn, "failed to create session: "+err.Error())
			return
		}
		sessionID = session.ID
		h.bindSessionToActiveProfile(sessionID)
		sendWSResponse(conn, WSResponse{Type: "session", SessionID: sessionID})
	}

	// Store user message
	userTokens := agentctx.EstimateTokens(msg.Message)
	if _, err := h.messages.Insert(sessionID, "user", msg.Message, userTokens); err != nil {
		sendWSError(conn, "failed to store message: "+err.Error())
		return
	}

	// Create provider
	msgProvider := &storageMessageProvider{
		messages:  h.messages,
		sessionID: sessionID,
	}

	// Run agent
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	runID := h.registerActiveRun(sessionID, cancel, "ws")
	defer func() {
		cancel()
		h.clearActiveRun(sessionID, runID)
	}()

	resp, err := h.agent.RunStream(
		ctx,
		sessionID,
		msg.Message,
		msgProvider,
		nil,
		func(token string) {
			sendWSResponse(conn, WSResponse{
				Type:      "token",
				Content:   token,
				SessionID: sessionID,
			})
		},
		func(toolName, _ string) {
			sendWSResponse(conn, WSResponse{
				Type:      "tool_use",
				Tool:      toolName,
				SessionID: sessionID,
			})
		},
	)
	if err != nil {
		sendWSError(conn, "agent error: "+err.Error())
		return
	}

	// Store response
	assistantTokens := agentctx.EstimateTokens(resp.Text)
	h.messages.Insert(sessionID, "assistant", resp.Text, assistantTokens)

	sendWSResponse(conn, WSResponse{
		Type:      "response",
		Content:   resp.Text,
		SessionID: sessionID,
		Usage: map[string]interface{}{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
			"llm_calls":     resp.Usage.LLMCalls,
		},
	})
}

func (h *Handlers) handleWSAbort(conn *safeWSConn, msg WSMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		sendWSError(conn, "session_id is required for abort")
		return
	}
	if !h.abortActiveRun(sessionID) {
		sendWSError(conn, "no active run found for session")
		return
	}
	sendWSResponse(conn, WSResponse{
		Type:      "control",
		SessionID: sessionID,
		Content:   "aborted",
	})
}

func (h *Handlers) handleWSInject(conn *safeWSConn, msg WSMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		sendWSError(conn, "session_id is required for inject")
		return
	}
	role := strings.TrimSpace(strings.ToLower(msg.Role))
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		sendWSError(conn, "content is required for inject")
		return
	}
	switch role {
	case "user", "assistant", "system", "tool_result":
	default:
		sendWSError(conn, "role must be one of: user|assistant|system|tool_result")
		return
	}
	if _, err := h.sessions.Get(sessionID); err != nil {
		if err == storage.ErrNotFound {
			sendWSError(conn, "session not found")
		} else {
			sendWSError(conn, "failed to load session: "+err.Error())
		}
		return
	}
	tokens := agentctx.EstimateTokens(content)
	if _, err := h.messages.Insert(sessionID, role, content, tokens); err != nil {
		sendWSError(conn, "failed to inject message: "+err.Error())
		return
	}
	_ = h.sessions.UpdateLastActive(sessionID)
	sendWSResponse(conn, WSResponse{
		Type:      "control",
		SessionID: sessionID,
		Content:   "injected",
	})
}

func sendWSResponse(conn *safeWSConn, resp WSResponse) {
	_ = conn.WriteJSON(resp)
}

func sendWSError(conn *safeWSConn, msg string) {
	_ = conn.WriteJSON(WSResponse{Type: "error", Error: msg})
}
