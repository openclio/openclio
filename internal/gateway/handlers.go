package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openclio/openclio/internal/agent"
	"github.com/openclio/openclio/internal/config"
	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/cost"
	agentcron "github.com/openclio/openclio/internal/cron"
	"github.com/openclio/openclio/internal/plugin"
	"github.com/openclio/openclio/internal/privacy"
	"github.com/openclio/openclio/internal/storage"
	"github.com/openclio/openclio/internal/tools"
)

// Handlers holds dependencies for HTTP handlers.
type Handlers struct {
	agent          *agent.Agent
	sessions       *storage.SessionStore
	messages       *storage.MessageStore
	contextEngine  *agentctx.Engine
	costTracker    *cost.Tracker
	cfg            *config.Config // pointer so UpdateConfig mutations are reflected
	manager        *plugin.Manager
	scheduler      *agentcron.Scheduler
	allowlist      *plugin.Allowlist
	privacyStore   *storage.PrivacyStore
	embeddingErrs  *storage.EmbeddingErrorStore
	knowledgeGraph *storage.KnowledgeGraphStore
	agentProfiles  *storage.AgentProfileStore
	toolRegistry   *tools.Registry
	channelControl tools.ChannelConnector
	channelLife    tools.ChannelLifecycleController
	mcpStatus      MCPRuntimeStatusSource
	mcpServers     []config.MCPServerConfig
	startedAt      time.Time
	setupMu        sync.RWMutex
	setupRequired  bool
	setupReason    string
	dataDir        string
	runMu          sync.Mutex
	runSeq         uint64
	activeRuns     map[string]activeRun
	debugMu        sync.Mutex
	debugSeq       uint64
	debugEvents    []DebugEvent
}

type activeRun struct {
	id        string
	cancel    context.CancelFunc
	startedAt time.Time
	source    string
}

type DebugEvent struct {
	ID        uint64         `json:"id"`
	Action    string         `json:"action"`
	Status    string         `json:"status"`
	Message   string         `json:"message"`
	Timestamp string         `json:"timestamp"`
	Meta      map[string]any `json:"meta,omitempty"`
}

// MCPRuntimeStatus is one MCP server runtime health snapshot.
type MCPRuntimeStatus struct {
	Name                string `json:"name"`
	Status              string `json:"status"`
	Healthy             bool   `json:"healthy"`
	LastHealthCheck     string `json:"last_health_check,omitempty"`
	LastHealthError     string `json:"last_health_error,omitempty"`
	RestartCount        int    `json:"restart_count"`
	ConsecutiveFailures int    `json:"consecutive_failures,omitempty"`
	NextRetryAt         string `json:"next_retry_at,omitempty"`
	RetryBackoffMs      int64  `json:"retry_backoff_ms,omitempty"`
	Disabled            bool   `json:"disabled,omitempty"`
}

// MCPRuntimeStatusSource provides runtime health and restart operations for MCP servers.
type MCPRuntimeStatusSource interface {
	SnapshotMCPStatus() []MCPRuntimeStatus
	RestartMCPServer(name string) error
}

// NewHandlers creates handlers with the given dependencies.
func NewHandlers(a *agent.Agent, sessions *storage.SessionStore, messages *storage.MessageStore, engine *agentctx.Engine, tracker *cost.Tracker, cfg *config.Config) *Handlers {
	setupRequired := a == nil || a.Provider() == nil
	setupReason := ""
	if setupRequired {
		setupReason = "provider not configured"
	}
	dataDir := ""
	if cfg != nil {
		dataDir = strings.TrimSpace(cfg.DataDir)
	}
	if dataDir == "" {
		if homeDir, err := os.UserHomeDir(); err == nil {
			dataDir = filepath.Join(homeDir, ".openclio")
		}
	}

	return &Handlers{
		agent:         a,
		sessions:      sessions,
		messages:      messages,
		contextEngine: engine,
		costTracker:   tracker,
		cfg:           cfg,
		startedAt:     time.Now().UTC(),
		setupRequired: setupRequired,
		setupReason:   setupReason,
		dataDir:       dataDir,
		activeRuns:    make(map[string]activeRun),
		debugEvents:   make([]DebugEvent, 0),
	}
}

// AttachRuntimeSources wires optional runtime components used by dashboard endpoints.
func (h *Handlers) AttachRuntimeSources(manager *plugin.Manager, scheduler *agentcron.Scheduler, allowlist *plugin.Allowlist, mcpServers []config.MCPServerConfig) {
	h.manager = manager
	h.scheduler = scheduler
	h.allowlist = allowlist
	h.mcpServers = append([]config.MCPServerConfig(nil), mcpServers...)
	h.registerRuntimeMessageSendTool()
}

// AttachPrivacyStore wires optional privacy redaction storage.
func (h *Handlers) AttachPrivacyStore(store *storage.PrivacyStore) {
	h.privacyStore = store
}

// AttachEmbeddingErrors wires optional embedding error tracking storage.
func (h *Handlers) AttachEmbeddingErrors(store *storage.EmbeddingErrorStore) {
	h.embeddingErrs = store
}

// AttachKnowledgeGraphStore wires optional knowledge graph storage.
func (h *Handlers) AttachKnowledgeGraphStore(store *storage.KnowledgeGraphStore) {
	h.knowledgeGraph = store
}

// AttachAgentProfiles wires optional agent profile storage used by dashboard endpoints.
func (h *Handlers) AttachAgentProfiles(store *storage.AgentProfileStore) {
	h.agentProfiles = store
}

// AttachToolRegistry wires optional runtime tool registry used for live tool updates.
func (h *Handlers) AttachToolRegistry(registry *tools.Registry) {
	h.toolRegistry = registry
}

// AttachChannelRuntime wires runtime channel connect/disconnect controls.
func (h *Handlers) AttachChannelRuntime(connector tools.ChannelConnector, lifecycle tools.ChannelLifecycleController) {
	h.channelControl = connector
	h.channelLife = lifecycle
	h.registerRuntimeMessageSendTool()
}

func (h *Handlers) registerRuntimeMessageSendTool() {
	// Register runtime message_send tool when manager becomes available.
	if h.toolRegistry == nil || h.manager == nil {
		return
	}
	// Replace existing message_send tool if present.
	h.toolRegistry.Unregister("message_send")
	msgTool := tools.NewMessageSendTool(h.manager)
	if h.channelControl != nil {
		msgTool.SetChannelConnector(h.channelControl)
	}
	h.toolRegistry.Register(msgTool)
}

// AttachMCPStatusSource wires MCP runtime health/restart source for dashboard APIs.
func (h *Handlers) AttachMCPStatusSource(source MCPRuntimeStatusSource) {
	h.mcpStatus = source
}

// --- Health ---

func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	setupRequired, reason := h.setupState()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":         "ok",
		"time":           time.Now().UTC().Format(time.RFC3339),
		"version":        "dev",
		"setup_required": setupRequired,
		"setup_reason":   reason,
	})
}

// --- Metrics ---

func (h *Handlers) Metrics(w http.ResponseWriter, r *http.Request) {
	summary, err := h.costTracker.GetSummary("all")
	if err != nil {
		http.Error(w, "Failed to get metrics: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)

	fmt.Fprintf(w, "# HELP agent_llm_calls_total Total number of LLM API calls made.\n")
	fmt.Fprintf(w, "# TYPE agent_llm_calls_total counter\n")
	fmt.Fprintf(w, "agent_llm_calls_total %d\n\n", summary.CallCount)

	fmt.Fprintf(w, "# HELP agent_input_tokens_total Total input tokens processed across all LLM operations.\n")
	fmt.Fprintf(w, "# TYPE agent_input_tokens_total counter\n")
	fmt.Fprintf(w, "agent_input_tokens_total %d\n\n", summary.InputTokens)

	fmt.Fprintf(w, "# HELP agent_output_tokens_total Total output tokens generated across all LLM operations.\n")
	fmt.Fprintf(w, "# TYPE agent_output_tokens_total counter\n")
	fmt.Fprintf(w, "agent_output_tokens_total %d\n\n", summary.OutputTokens)

	fmt.Fprintf(w, "# HELP agent_estimated_cost_usd Estimated total cost of LLM calls in USD.\n")
	fmt.Fprintf(w, "# TYPE agent_estimated_cost_usd counter\n")
	fmt.Fprintf(w, "agent_estimated_cost_usd %f\n", summary.TotalCost)
}

// ToolsHealth reports availability and basic version info for optional external tools.
func (h *Handlers) ToolsHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	check := func(bin string, args ...string) map[string]any {
		res := map[string]any{"available": false}
		if p, err := exec.LookPath(bin); err == nil {
			res["available"] = true
			res["path"] = p
			// Try to get version string
			out, err := exec.Command(p, args...).CombinedOutput()
			if err == nil {
				s := strings.TrimSpace(string(out))
				if len(s) > 200 {
					s = s[:200] + "..."
				}
				res["version"] = s
			}
		}
		return res
	}

	out := map[string]any{
		"time": time.Now().UTC().Format(time.RFC3339),
		"checks": map[string]any{
			"git":           check("git", "--version"),
			"pdftotext":     check("pdftotext", "-v"),
			"wkhtmltoimage": check("wkhtmltoimage", "--version"),
			"curl":          check("curl", "--version"),
			"chrome": func() map[string]any {
				m := check("google-chrome", "--version")
				if !m["available"].(bool) {
					m = check("chromium", "--version")
				}
				return m
			}(),
		},
	}
	writeJSON(w, http.StatusOK, out)
}

// Privacy reports privacy-related runtime settings and aggregate token/cost usage.
func (h *Handlers) Privacy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	scrubOutput := false
	if h.cfg != nil {
		scrubOutput = h.cfg.Tools.ScrubOutput
	}

	report, err := privacy.BuildReport(h.costTracker, h.privacyStore, scrubOutput, "all")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build privacy report: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// --- Sessions ---

func (h *Handlers) ListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.sessions.List(50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sessions: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sessions": sessions,
		"count":    len(sessions),
	})
}

func (h *Handlers) GetSession(w http.ResponseWriter, r *http.Request) {
	id := extractPathParam(r.URL.Path, "/api/v1/sessions/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}

	session, err := h.sessions.Get(id)
	if err != nil {
		if err == storage.ErrNotFound {
			writeError(w, http.StatusNotFound, "session not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to get session: "+err.Error())
		}
		return
	}
	if session == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	messages, err := h.messages.GetBySession(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get messages: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"session":  session,
		"messages": messages,
	})
}

func (h *Handlers) DeleteSession(w http.ResponseWriter, r *http.Request) {
	id := extractPathParam(r.URL.Path, "/api/v1/sessions/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}

	if err := h.sessions.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete session: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"deleted": id,
	})
}

// GetSessionStats returns summary statistics for a single session.
func (h *Handlers) GetSessionStats(w http.ResponseWriter, r *http.Request) {
	id := extractSessionIDWithSuffix(r.URL.Path, "/api/v1/sessions/", "/stats")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}

	session, err := h.sessions.Get(id)
	if err != nil {
		if err == storage.ErrNotFound {
			writeError(w, http.StatusNotFound, "session not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to get session: "+err.Error())
		}
		return
	}
	messages, err := h.messages.GetBySession(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get messages: "+err.Error())
		return
	}

	totalTokens := 0
	roleCounts := map[string]int{}
	lastMessageAt := ""
	if len(messages) > 0 {
		for _, m := range messages {
			totalTokens += m.Tokens
			roleCounts[m.Role]++
		}
		lastMessageAt = messages[len(messages)-1].CreatedAt.UTC().Format(time.RFC3339)
	}

	overrides := map[string]any{}
	if parsed, ok := extractSessionOverrides(session.Metadata); ok {
		overrides = parsed
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": id,
		"counts": map[string]any{
			"messages": len(messages),
			"tokens":   totalTokens,
			"by_role":  roleCounts,
		},
		"last_message_at": lastMessageAt,
		"created_at":      session.CreatedAt.UTC().Format(time.RFC3339),
		"last_active":     session.LastActive.UTC().Format(time.RFC3339),
		"overrides":       overrides,
	})
}

// GetSessionOverrides returns persisted UI/runtime overrides for one session.
func (h *Handlers) GetSessionOverrides(w http.ResponseWriter, r *http.Request) {
	id := extractSessionIDWithSuffix(r.URL.Path, "/api/v1/sessions/", "/overrides")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}
	session, err := h.sessions.Get(id)
	if err != nil {
		if err == storage.ErrNotFound {
			writeError(w, http.StatusNotFound, "session not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to get session: "+err.Error())
		}
		return
	}
	overrides, _ := extractSessionOverrides(session.Metadata)
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": id,
		"overrides":  overrides,
	})
}

type sessionOverridesPayload struct {
	Overrides map[string]any `json:"overrides"`
}

// UpdateSessionOverrides updates session-level overrides in session metadata.
func (h *Handlers) UpdateSessionOverrides(w http.ResponseWriter, r *http.Request) {
	id := extractSessionIDWithSuffix(r.URL.Path, "/api/v1/sessions/", "/overrides")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var payload sessionOverridesPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if payload.Overrides == nil {
		writeError(w, http.StatusBadRequest, "overrides is required")
		return
	}

	session, err := h.sessions.Get(id)
	if err != nil {
		if err == storage.ErrNotFound {
			writeError(w, http.StatusNotFound, "session not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to get session: "+err.Error())
		}
		return
	}

	meta := map[string]any{}
	if strings.TrimSpace(session.Metadata) != "" {
		_ = json.Unmarshal([]byte(session.Metadata), &meta)
	}
	meta["overrides"] = payload.Overrides
	encoded, err := json.Marshal(meta)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode session metadata: "+err.Error())
		return
	}
	if err := h.sessions.UpdateMetadata(id, string(encoded)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist session overrides: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"session_id": id,
		"overrides":  payload.Overrides,
	})
}

// GetSessionAgentProfile returns the agent profile binding for one session.
func (h *Handlers) GetSessionAgentProfile(w http.ResponseWriter, r *http.Request) {
	id := extractSessionIDWithSuffix(r.URL.Path, "/api/v1/sessions/", "/agent")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}
	session, err := h.sessions.Get(id)
	if err != nil {
		if err == storage.ErrNotFound {
			writeError(w, http.StatusNotFound, "session not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to get session: "+err.Error())
		}
		return
	}

	resp := map[string]any{
		"session_id":       id,
		"agent_profile_id": session.AgentProfileID,
	}
	if session.AgentProfileID != "" && h.agentProfiles != nil {
		if profile, err := h.agentProfiles.Get(session.AgentProfileID); err == nil {
			resp["profile"] = profile
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type sessionAgentBindingPayload struct {
	AgentProfileID *string `json:"agent_profile_id"`
}

// UpdateSessionAgentProfile binds (or clears) an agent profile for one session.
func (h *Handlers) UpdateSessionAgentProfile(w http.ResponseWriter, r *http.Request) {
	id := extractSessionIDWithSuffix(r.URL.Path, "/api/v1/sessions/", "/agent")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	var payload sessionAgentBindingPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if payload.AgentProfileID == nil {
		writeError(w, http.StatusBadRequest, "agent_profile_id is required (empty string clears binding)")
		return
	}

	profileID := strings.TrimSpace(*payload.AgentProfileID)
	if profileID != "" {
		if h.agentProfiles == nil {
			writeError(w, http.StatusServiceUnavailable, "agent profile store is not configured")
			return
		}
		if _, err := h.agentProfiles.Get(profileID); err != nil {
			if err == storage.ErrNotFound {
				writeError(w, http.StatusNotFound, "agent profile not found")
			} else {
				writeError(w, http.StatusInternalServerError, "failed to get agent profile: "+err.Error())
			}
			return
		}
	}

	if err := h.sessions.BindAgentProfile(id, profileID); err != nil {
		if err == storage.ErrNotFound {
			writeError(w, http.StatusNotFound, "session not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to bind agent profile: "+err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"session_id":       id,
		"agent_profile_id": profileID,
	})
}

// --- Chat ---

type ChatRequest struct {
	Message   string `json:"message"`
	SessionID string `json:"session_id,omitempty"`
}

func (h *Handlers) Chat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if required, _ := h.setupState(); required {
		writeError(w, http.StatusServiceUnavailable, "setup required: configure provider via /api/v1/setup")
		return
	}
	if h.agent == nil {
		writeError(w, http.StatusServiceUnavailable, "agent is unavailable")
		return
	}

	// Streaming path: POST /api/v1/chat?stream=true
	if r.URL.Query().Get("stream") == "true" {
		h.chatStream(w, r)
		return
	}

	// Limit request body to 10MB to prevent memory exhaustion.
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	// Get or create session
	sessionID := req.SessionID
	if sessionID == "" {
		session, err := h.sessions.Create("api", "api-user")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create session: "+err.Error())
			return
		}
		sessionID = session.ID
		h.bindSessionToActiveProfile(sessionID)
	} else {
		if err := h.sessions.UpdateLastActive(sessionID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update session: "+err.Error())
			return
		}
	}

	// Store user message
	userTokens := agentctx.EstimateTokens(req.Message)
	if _, err := h.messages.Insert(sessionID, "user", req.Message, userTokens); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store message: "+err.Error())
		return
	}

	// Create message provider adapter
	msgProvider := &storageMessageProvider{
		messages:  h.messages,
		sessionID: sessionID,
	}

	// Run the agent
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	runID := h.registerActiveRun(sessionID, cancel, "rest")
	defer func() {
		cancel()
		h.clearActiveRun(sessionID, runID)
	}()

	resp, err := h.agent.Run(ctx, sessionID, req.Message, msgProvider, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "agent error: "+err.Error())
		return
	}

	// Store assistant response
	assistantTokens := agentctx.EstimateTokens(resp.Text)
	if _, err := h.messages.Insert(sessionID, "assistant", resp.Text, assistantTokens); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store response: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response":   resp.Text,
		"session_id": sessionID,
		"usage": map[string]interface{}{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
			"llm_calls":     resp.Usage.LLMCalls,
		},
		"tools_used":  resp.ToolsUsed,
		"iterations":  resp.Iterations,
		"duration_ms": resp.Duration.Milliseconds(),
	})
}

// chatStream handles POST /api/v1/chat?stream=true
// It sends tokens as Server-Sent Events as they arrive from the LLM.
// Falls back to a full (buffered) response if the provider doesn't support streaming.
func (h *Handlers) chatStream(w http.ResponseWriter, r *http.Request) {
	if required, _ := h.setupState(); required {
		writeError(w, http.StatusServiceUnavailable, "setup required: configure provider via /api/v1/setup")
		return
	}
	if h.agent == nil {
		writeError(w, http.StatusServiceUnavailable, "agent is unavailable")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	// Get or create session
	sessionID := req.SessionID
	if sessionID == "" {
		session, err := h.sessions.Create("api", "api-user")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create session: "+err.Error())
			return
		}
		sessionID = session.ID
		h.bindSessionToActiveProfile(sessionID)
	}

	// Store user message
	userTokens := agentctx.EstimateTokens(req.Message)
	if _, err := h.messages.Insert(sessionID, "user", req.Message, userTokens); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store message: "+err.Error())
		return
	}

	msgProvider := &storageMessageProvider{messages: h.messages, sessionID: sessionID}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	runID := h.registerActiveRun(sessionID, cancel, "rest-sse")
	defer func() {
		cancel()
		h.clearActiveRun(sessionID, runID)
	}()

	// Check if provider supports streaming
	streamer, canStream := h.agent.Provider().(agent.Streamer)
	if !canStream {
		// Fall back to full response
		resp, err := h.agent.Run(ctx, sessionID, req.Message, msgProvider, nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "agent error: "+err.Error())
			return
		}
		assistantTokens := agentctx.EstimateTokens(resp.Text)
		h.messages.Insert(sessionID, "assistant", resp.Text, assistantTokens)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response":   resp.Text,
			"session_id": sessionID,
		})
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Session-ID", sessionID)
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Build a simple chat request to pass to the provider
	chatReq, err := h.agent.BuildStreamRequest(ctx, sessionID, req.Message, msgProvider)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}

	ch, err := streamer.Stream(ctx, *chatReq)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}

	var fullText strings.Builder
	for chunk := range ch {
		if chunk.Error != nil {
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", chunk.Error.Error())
			flusher.Flush()
			return
		}
		if chunk.Text != "" {
			fullText.WriteString(chunk.Text)
			encoded, _ := json.Marshal(map[string]string{"text": chunk.Text})
			fmt.Fprintf(w, "data: %s\n\n", encoded)
			flusher.Flush()
		}
		if chunk.Done {
			break
		}
	}

	// Store assistant response
	fullResponse := fullText.String()
	assistantTokens := agentctx.EstimateTokens(fullResponse)
	h.messages.Insert(sessionID, "assistant", fullResponse, assistantTokens)

	// Send done event with session info
	donePayload, _ := json.Marshal(map[string]string{
		"session_id": sessionID,
		"done":       "true",
	})
	fmt.Fprintf(w, "event: done\ndata: %s\n\n", donePayload)
	flusher.Flush()
}

type chatAbortPayload struct {
	SessionID string `json:"session_id"`
}

// ChatAbort handles POST /api/v1/chat/abort and cancels a running chat for a session.
func (h *Handlers) ChatAbort(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	var payload chatAbortPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required")
		return
	}

	if !h.abortActiveRun(sessionID) {
		writeError(w, http.StatusNotFound, "no active run found for session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"aborted":    true,
		"session_id": sessionID,
	})
}

type chatInjectPayload struct {
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
}

// ChatInject handles POST /api/v1/chat/inject and inserts a message into a session history.
func (h *Handlers) ChatInject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var payload chatInjectPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required")
		return
	}
	role := strings.TrimSpace(strings.ToLower(payload.Role))
	content := strings.TrimSpace(payload.Content)
	if content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}

	switch role {
	case "user", "assistant", "system", "tool_result":
	default:
		writeError(w, http.StatusBadRequest, "role must be one of: user|assistant|system|tool_result")
		return
	}

	if _, err := h.sessions.Get(sessionID); err != nil {
		if err == storage.ErrNotFound {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load session: "+err.Error())
		return
	}

	tokens := agentctx.EstimateTokens(content)
	msg, err := h.messages.Insert(sessionID, role, content, tokens)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to inject message: "+err.Error())
		return
	}
	_ = h.sessions.UpdateLastActive(sessionID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": msg,
	})
}

// --- Config ---

// GetConfig returns a sanitized view of the current configuration.
// API keys and secrets are NEVER included.
func (h *Handlers) GetConfig(w http.ResponseWriter, r *http.Request) {
	if h.cfg == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"note": "config not available",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"model": map[string]interface{}{
			"provider":    h.cfg.Model.Provider,
			"model":       h.cfg.Model.Model,
			"base_url":    h.cfg.Model.BaseURL,
			"name":        h.cfg.Model.Name,
			"api_key_env": h.cfg.Model.APIKeyEnv, // shows env var name, never its value
		},
		"gateway": map[string]interface{}{
			"port": h.cfg.Gateway.Port,
			"bind": h.cfg.Gateway.Bind,
		},
		"agent": map[string]interface{}{
			"max_tool_iterations":    h.cfg.Agent.MaxToolIterations,
			"max_tokens_per_session": h.cfg.Agent.MaxTokensPerSession,
			"max_tokens_per_day":     h.cfg.Agent.MaxTokensPerDay,
		},
		"tools": map[string]interface{}{
			"max_output_size": h.cfg.Tools.MaxOutputSize,
			"scrub_output":    h.cfg.Tools.ScrubOutput,
			"exec": map[string]interface{}{
				"sandbox":              h.cfg.Tools.Exec.Sandbox,
				"docker_image":         h.cfg.Tools.Exec.DockerImage,
				"network_access":       h.cfg.Tools.Exec.NetworkAccess,
				"require_confirmation": h.cfg.Tools.Exec.RequireConfirmation,
				"timeout_secs":         h.cfg.Tools.Exec.Timeout.Seconds(),
			},
			"browser": map[string]interface{}{
				"enabled":      h.cfg.Tools.Browser.Enabled,
				"headless":     h.cfg.Tools.Browser.Headless,
				"chrome_path":  h.cfg.Tools.Browser.ChromePath,
				"timeout_secs": h.cfg.Tools.Browser.Timeout.Seconds(),
			},
		},
		"context": map[string]interface{}{
			"max_tokens_per_call":    h.cfg.Context.MaxTokensPerCall,
			"history_retrieval_k":    h.cfg.Context.HistoryRetrievalK,
			"proactive_compaction":   h.cfg.Context.ProactiveCompaction,
			"compaction_keep_recent": h.cfg.Context.CompactionKeepRecent,
			"compaction_model":       h.cfg.Context.CompactionModel,
			"tool_result_summary":    h.cfg.Context.ToolResultSummary,
		},
		"logging": map[string]interface{}{
			"level":  h.cfg.Logging.Level,
			"output": h.cfg.Logging.Output,
		},
		"retention": map[string]interface{}{
			"sessions_days":        h.cfg.Retention.SessionsDays,
			"messages_per_session": h.cfg.Retention.MessagesPerSession,
		},
	})
}

type setupPayload struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
	BaseURL  string `json:"base_url,omitempty"` // required for openai-compat
	Name     string `json:"name,omitempty"`     // display name for openai-compat
}

// Setup configures the initial provider credentials and hot-reloads the agent.
// This endpoint is intentionally protected by the standard auth middleware.
func (h *Handlers) Setup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if h.cfg == nil {
		writeError(w, http.StatusServiceUnavailable, "config not available")
		return
	}
	if h.agent == nil {
		writeError(w, http.StatusServiceUnavailable, "agent is unavailable")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var payload setupPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	provider := strings.ToLower(strings.TrimSpace(payload.Provider))
	if provider == "" {
		writeError(w, http.StatusBadRequest, "provider is required")
		return
	}
	switch provider {
	case "anthropic", "openai", "gemini", "ollama", "cohere",
		"groq", "deepseek", "mistral", "xai", "cerebras",
		"together", "fireworks", "perplexity", "openrouter",
		"kimi", "sambanova", "lambda", "lmstudio", "openai-compat":
	default:
		writeError(w, http.StatusBadRequest, "unknown provider: "+provider)
		return
	}

	// openai-compat requires a base_url.
	if provider == "openai-compat" && strings.TrimSpace(payload.BaseURL) == "" {
		writeError(w, http.StatusBadRequest, "base_url is required for openai-compat provider")
		return
	}

	model := strings.TrimSpace(payload.Model)
	if model == "" {
		model = defaultModelForProvider(provider)
	}
	if provider == "openai-compat" && model == "" {
		writeError(w, http.StatusBadRequest, "model is required for openai-compat provider")
		return
	}

	// Providers that don't require an API key.
	noKeyProviders := map[string]bool{"ollama": true, "lmstudio": true}
	apiKeyEnv := strings.TrimSpace(payload.APIKey) // temporary variable reuse below

	if !noKeyProviders[provider] {
		apiKey := strings.TrimSpace(payload.APIKey)
		if apiKey == "" {
			writeError(w, http.StatusBadRequest, "api_key is required for this provider")
			return
		}
		apiKeyEnv = defaultAPIKeyEnvForProvider(provider)
		envPath := filepath.Join(h.dataDir, ".env")
		if err := config.UpsertDotEnvKey(envPath, apiKeyEnv, apiKey); err != nil {
			writeError(w, http.StatusInternalServerError, "writing .env failed: "+err.Error())
			return
		}
		_ = os.Setenv(apiKeyEnv, apiKey)
	} else {
		apiKeyEnv = ""
	}

	previousModelCfg := h.cfg.Model

	h.cfg.Model.Provider = provider
	h.cfg.Model.Model = model
	h.cfg.Model.APIKeyEnv = apiKeyEnv
	// Reset optional fields unless explicitly provided by payload.
	h.cfg.Model.BaseURL = ""
	h.cfg.Model.Name = ""
	if baseURL := strings.TrimSpace(payload.BaseURL); baseURL != "" {
		h.cfg.Model.BaseURL = baseURL
	}
	if name := strings.TrimSpace(payload.Name); name != "" {
		h.cfg.Model.Name = name
	}

	providerImpl, err := buildProviderStackFromConfig(h.cfg)
	if err != nil {
		h.cfg.Model = previousModelCfg
		writeError(w, http.StatusBadRequest, "provider initialization failed: "+err.Error())
		return
	}

	configPath := filepath.Join(h.dataDir, "config.yaml")
	if err := config.Save(configPath, h.cfg); err != nil {
		h.cfg.Model = previousModelCfg
		writeError(w, http.StatusInternalServerError, "persisting setup config failed: "+err.Error())
		return
	}

	h.agent.SetProvider(providerImpl, model)
	h.setSetupState(false, "")

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":             true,
		"setup_required": false,
		"provider":       provider,
		"model":          model,
	})
}

// updateConfigPayload is the shape of a PUT /api/v1/config request body.
// Only explicitly mutable fields are accepted.
type updateConfigPayload struct {
	Model *struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		BaseURL  string `json:"base_url"`
		Name     string `json:"name"`
	} `json:"model"`
	Logging *struct {
		Level  string `json:"level"`
		Output string `json:"output"`
	} `json:"logging"`
	Agent *struct {
		MaxToolIterations   int `json:"max_tool_iterations"`
		MaxTokensPerSession int `json:"max_tokens_per_session"`
		MaxTokensPerDay     int `json:"max_tokens_per_day"`
	} `json:"agent"`
	Tools *struct {
		MaxOutputSize int   `json:"max_output_size"`
		ScrubOutput   *bool `json:"scrub_output"`
		Exec          *struct {
			Sandbox             string   `json:"sandbox"`
			DockerImage         string   `json:"docker_image"`
			NetworkAccess       *bool    `json:"network_access"`
			RequireConfirmation *bool    `json:"require_confirmation"`
			TimeoutSecs         *float64 `json:"timeout_secs"`
		} `json:"exec"`
		Browser *struct {
			Enabled     *bool    `json:"enabled"`
			Headless    *bool    `json:"headless"`
			ChromePath  string   `json:"chrome_path"`
			TimeoutSecs *float64 `json:"timeout_secs"`
		} `json:"browser"`
	} `json:"tools"`
	Context *struct {
		MaxTokensPerCall     *int     `json:"max_tokens_per_call"`
		HistoryRetrievalK    *int     `json:"history_retrieval_k"`
		ProactiveCompaction  *float64 `json:"proactive_compaction"`
		CompactionKeepRecent *int     `json:"compaction_keep_recent"`
		CompactionModel      string   `json:"compaction_model"`
		ToolResultSummary    *bool    `json:"tool_result_summary"`
	} `json:"context"`
	Retention *struct {
		SessionsDays       int `json:"sessions_days"`
		MessagesPerSession int `json:"messages_per_session"`
	} `json:"retention"`
}

// UpdateConfig handles PUT /api/v1/config.
// It applies only explicitly mutable fields; immutable ones (port, bind, API keys) are rejected.
func (h *Handlers) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var payload updateConfigPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if h.cfg == nil {
		writeError(w, http.StatusServiceUnavailable, "config not available")
		return
	}

	var changed []string
	browserRuntimeChanged := false

	if payload.Model != nil {
		if payload.Model.Provider != "" {
			switch payload.Model.Provider {
			case "anthropic", "openai", "gemini", "ollama", "cohere",
				"groq", "deepseek", "mistral", "xai", "cerebras",
				"together", "fireworks", "perplexity", "openrouter",
				"kimi", "sambanova", "lambda", "lmstudio", "openai-compat":
				h.cfg.Model.Provider = payload.Model.Provider
				changed = append(changed, "model.provider")
			default:
				writeError(w, http.StatusBadRequest, "unknown model.provider: "+payload.Model.Provider)
				return
			}
		}
		if strings.TrimSpace(payload.Model.Model) != "" {
			h.cfg.Model.Model = strings.TrimSpace(payload.Model.Model)
			changed = append(changed, "model.model")
		}
		if strings.TrimSpace(payload.Model.BaseURL) != "" {
			h.cfg.Model.BaseURL = strings.TrimSpace(payload.Model.BaseURL)
			changed = append(changed, "model.base_url")
		}
		if strings.TrimSpace(payload.Model.Name) != "" {
			h.cfg.Model.Name = strings.TrimSpace(payload.Model.Name)
			changed = append(changed, "model.name")
		}
	}

	if payload.Logging != nil {
		if payload.Logging.Level != "" {
			valid := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
			if !valid[payload.Logging.Level] {
				writeError(w, http.StatusBadRequest, "logging.level must be debug|info|warn|error")
				return
			}
			h.cfg.Logging.Level = payload.Logging.Level
			changed = append(changed, "logging.level")
		}
		if payload.Logging.Output != "" {
			h.cfg.Logging.Output = payload.Logging.Output
			changed = append(changed, "logging.output")
		}
	}

	if payload.Agent != nil {
		if payload.Agent.MaxToolIterations > 0 {
			if payload.Agent.MaxToolIterations > 100 {
				writeError(w, http.StatusBadRequest, "agent.max_tool_iterations must be ≤ 100")
				return
			}
			h.cfg.Agent.MaxToolIterations = payload.Agent.MaxToolIterations
			changed = append(changed, "agent.max_tool_iterations")
		}
		if payload.Agent.MaxTokensPerSession >= 0 {
			h.cfg.Agent.MaxTokensPerSession = payload.Agent.MaxTokensPerSession
			changed = append(changed, "agent.max_tokens_per_session")
		}
		if payload.Agent.MaxTokensPerDay >= 0 {
			h.cfg.Agent.MaxTokensPerDay = payload.Agent.MaxTokensPerDay
			changed = append(changed, "agent.max_tokens_per_day")
		}
	}

	if payload.Tools != nil {
		if payload.Tools.MaxOutputSize > 0 {
			h.cfg.Tools.MaxOutputSize = payload.Tools.MaxOutputSize
			changed = append(changed, "tools.max_output_size")
		}
		if payload.Tools.ScrubOutput != nil {
			h.cfg.Tools.ScrubOutput = *payload.Tools.ScrubOutput
			changed = append(changed, "tools.scrub_output")
		}
		if payload.Tools.Exec != nil {
			exec := payload.Tools.Exec
			if exec.Sandbox != "" {
				switch exec.Sandbox {
				case "none", "namespace", "docker":
					h.cfg.Tools.Exec.Sandbox = exec.Sandbox
					changed = append(changed, "tools.exec.sandbox")
				default:
					writeError(w, http.StatusBadRequest, "tools.exec.sandbox must be none|namespace|docker")
					return
				}
			}
			if exec.DockerImage != "" {
				h.cfg.Tools.Exec.DockerImage = exec.DockerImage
				changed = append(changed, "tools.exec.docker_image")
			}
			if exec.NetworkAccess != nil {
				h.cfg.Tools.Exec.NetworkAccess = *exec.NetworkAccess
				changed = append(changed, "tools.exec.network_access")
			}
			if exec.RequireConfirmation != nil {
				h.cfg.Tools.Exec.RequireConfirmation = *exec.RequireConfirmation
				changed = append(changed, "tools.exec.require_confirmation")
			}
			if exec.TimeoutSecs != nil && *exec.TimeoutSecs >= 0 {
				h.cfg.Tools.Exec.Timeout = time.Duration(*exec.TimeoutSecs * float64(time.Second))
				changed = append(changed, "tools.exec.timeout")
			}
		}
		if payload.Tools.Browser != nil {
			browser := payload.Tools.Browser
			if browser.Enabled != nil {
				h.cfg.Tools.Browser.Enabled = *browser.Enabled
				changed = append(changed, "tools.browser.enabled")
				browserRuntimeChanged = true
			}
			if browser.Headless != nil {
				h.cfg.Tools.Browser.Headless = *browser.Headless
				changed = append(changed, "tools.browser.headless")
				browserRuntimeChanged = true
			}
			if browser.ChromePath != "" {
				h.cfg.Tools.Browser.ChromePath = browser.ChromePath
				changed = append(changed, "tools.browser.chrome_path")
				browserRuntimeChanged = true
			}
			if browser.TimeoutSecs != nil && *browser.TimeoutSecs >= 0 {
				h.cfg.Tools.Browser.Timeout = time.Duration(*browser.TimeoutSecs * float64(time.Second))
				changed = append(changed, "tools.browser.timeout")
				browserRuntimeChanged = true
			}
		}
	}

	if payload.Context != nil {
		if payload.Context.MaxTokensPerCall != nil && *payload.Context.MaxTokensPerCall > 0 {
			h.cfg.Context.MaxTokensPerCall = *payload.Context.MaxTokensPerCall
			changed = append(changed, "context.max_tokens_per_call")
		}
		if payload.Context.HistoryRetrievalK != nil && *payload.Context.HistoryRetrievalK > 0 {
			h.cfg.Context.HistoryRetrievalK = *payload.Context.HistoryRetrievalK
			changed = append(changed, "context.history_retrieval_k")
		}
		if payload.Context.ProactiveCompaction != nil {
			if *payload.Context.ProactiveCompaction < 0 || *payload.Context.ProactiveCompaction > 1 {
				writeError(w, http.StatusBadRequest, "context.proactive_compaction must be between 0 and 1")
				return
			}
			h.cfg.Context.ProactiveCompaction = *payload.Context.ProactiveCompaction
			changed = append(changed, "context.proactive_compaction")
		}
		if payload.Context.CompactionKeepRecent != nil && *payload.Context.CompactionKeepRecent >= 0 {
			h.cfg.Context.CompactionKeepRecent = *payload.Context.CompactionKeepRecent
			changed = append(changed, "context.compaction_keep_recent")
		}
		if payload.Context.CompactionModel != "" {
			h.cfg.Context.CompactionModel = payload.Context.CompactionModel
			changed = append(changed, "context.compaction_model")
		}
		if payload.Context.ToolResultSummary != nil {
			h.cfg.Context.ToolResultSummary = *payload.Context.ToolResultSummary
			changed = append(changed, "context.tool_result_summary")
		}
	}

	if payload.Retention != nil {
		if payload.Retention.SessionsDays >= 0 {
			h.cfg.Retention.SessionsDays = payload.Retention.SessionsDays
			changed = append(changed, "retention.sessions_days")
		}
		if payload.Retention.MessagesPerSession >= 0 {
			h.cfg.Retention.MessagesPerSession = payload.Retention.MessagesPerSession
			changed = append(changed, "retention.messages_per_session")
		}
	}
	if browserRuntimeChanged {
		h.syncRuntimeBrowserTool()
		changed = append(changed, "runtime.tools.browser")
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"updated": changed,
		"note":    "changes are in-memory only and will reset on restart",
	})
}

func (h *Handlers) syncRuntimeBrowserTool() {
	if h == nil || h.toolRegistry == nil || h.cfg == nil {
		return
	}

	h.toolRegistry.Unregister("browser")
	if h.cfg.Tools.Browser.Enabled {
		browserTool := tools.NewBrowserTool(h.cfg.Tools.Browser)
		h.toolRegistry.Register(browserTool)
		if webFetchRaw, ok := h.toolRegistry.Get("web_fetch"); ok {
			if webFetchTool, ok := webFetchRaw.(*tools.WebFetchTool); ok {
				webFetchTool.SetBrowserFallback(browserTool)
			}
		}
		return
	}

	if webFetchRaw, ok := h.toolRegistry.Get("web_fetch"); ok {
		if webFetchTool, ok := webFetchRaw.(*tools.WebFetchTool); ok {
			webFetchTool.SetBrowserFallback(nil)
		}
	}
}

// --- Helpers ---

func (h *Handlers) registerActiveRun(sessionID string, cancel context.CancelFunc, source string) string {
	if sessionID == "" || cancel == nil {
		return ""
	}
	runID := fmt.Sprintf("run-%d", atomic.AddUint64(&h.runSeq, 1))
	h.runMu.Lock()
	if prev, ok := h.activeRuns[sessionID]; ok && prev.cancel != nil {
		prev.cancel()
	}
	h.activeRuns[sessionID] = activeRun{
		id:        runID,
		cancel:    cancel,
		startedAt: time.Now().UTC(),
		source:    source,
	}
	h.runMu.Unlock()
	return runID
}

func (h *Handlers) clearActiveRun(sessionID, runID string) {
	if sessionID == "" || runID == "" {
		return
	}
	h.runMu.Lock()
	defer h.runMu.Unlock()
	run, ok := h.activeRuns[sessionID]
	if !ok || run.id != runID {
		return
	}
	delete(h.activeRuns, sessionID)
}

func (h *Handlers) abortActiveRun(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	h.runMu.Lock()
	run, ok := h.activeRuns[sessionID]
	if ok {
		delete(h.activeRuns, sessionID)
	}
	h.runMu.Unlock()
	if !ok || run.cancel == nil {
		return false
	}
	run.cancel()
	return true
}

func (h *Handlers) bindSessionToActiveProfile(sessionID string) {
	if sessionID == "" || h.sessions == nil || h.agentProfiles == nil {
		return
	}
	active, err := h.agentProfiles.GetActive()
	if err != nil {
		return
	}
	_ = h.sessions.BindAgentProfile(sessionID, active.ID)
}

func (h *Handlers) addDebugEvent(action, status, message string, meta map[string]any) DebugEvent {
	id := atomic.AddUint64(&h.debugSeq, 1)
	event := DebugEvent{
		ID:        id,
		Action:    action,
		Status:    status,
		Message:   message,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Meta:      meta,
	}
	h.debugMu.Lock()
	h.debugEvents = append(h.debugEvents, event)
	if len(h.debugEvents) > 500 {
		h.debugEvents = h.debugEvents[len(h.debugEvents)-500:]
	}
	h.debugMu.Unlock()
	return event
}

func (h *Handlers) listDebugEvents(limit int) []DebugEvent {
	if limit <= 0 {
		limit = 100
	}
	h.debugMu.Lock()
	defer h.debugMu.Unlock()
	n := len(h.debugEvents)
	if n == 0 {
		return nil
	}
	if limit > n {
		limit = n
	}
	out := make([]DebugEvent, limit)
	copy(out, h.debugEvents[n-limit:])
	return out
}

// storageMessageProvider adapts storage.MessageStore to the context engine's MessageProvider interface.
type storageMessageProvider struct {
	messages  *storage.MessageStore
	sessionID string
}

func (p *storageMessageProvider) GetRecentMessages(sessionID string, limit int) ([]agentctx.ContextMessage, error) {
	msgs, err := p.messages.GetRecent(sessionID, limit)
	if err != nil {
		return nil, err
	}
	var result []agentctx.ContextMessage
	for _, m := range msgs {
		result = append(result, agentctx.ContextMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	return result, nil
}

func (p *storageMessageProvider) GetStoredEmbeddings(sessionID string) ([]agentctx.StoredEmbedding, error) {
	msgs, err := p.messages.GetEmbeddings(sessionID)
	if err != nil {
		return nil, err
	}
	var result []agentctx.StoredEmbedding
	for _, m := range msgs {
		result = append(result, agentctx.StoredEmbedding{
			MessageID: m.ID,
			SessionID: m.SessionID,
			Role:      m.Role,
			Content:   m.Content,
			Summary:   m.Summary,
			Tokens:    m.Tokens,
			Embedding: m.Embedding,
		})
	}
	return result, nil
}

func (p *storageMessageProvider) SearchKnowledge(query, nodeType string, limit int) ([]agentctx.KnowledgeNode, error) {
	nodes, err := p.messages.SearchKnowledge(query, nodeType, limit)
	if err != nil {
		return nil, err
	}
	out := make([]agentctx.KnowledgeNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, agentctx.KnowledgeNode{
			ID:         n.ID,
			Type:       n.Type,
			Name:       n.Name,
			Confidence: n.Confidence,
		})
	}
	return out, nil
}

func (p *storageMessageProvider) GetOldMessages(sessionID string, keepRecentTurns int) ([]agent.CompactionMessage, error) {
	msgs, err := p.messages.GetOldMessages(sessionID, keepRecentTurns)
	if err != nil {
		return nil, err
	}
	result := make([]agent.CompactionMessage, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, agent.CompactionMessage{
			ID:      m.ID,
			Role:    m.Role,
			Content: m.Content,
		})
	}
	return result, nil
}

func (p *storageMessageProvider) ArchiveMessages(sessionID string, olderThanID int64) (int64, error) {
	return p.messages.ArchiveMessages(sessionID, olderThanID)
}

func (p *storageMessageProvider) InsertCompactionSummary(sessionID, content string, tokens int) error {
	_, err := p.messages.Insert(sessionID, "system", content, tokens)
	return err
}

func (h *Handlers) setupState() (required bool, reason string) {
	h.setupMu.RLock()
	defer h.setupMu.RUnlock()
	return h.setupRequired, h.setupReason
}

func (h *Handlers) setSetupState(required bool, reason string) {
	h.setupMu.Lock()
	defer h.setupMu.Unlock()
	h.setupRequired = required
	h.setupReason = reason
}

func buildProviderStackFromConfig(cfg *config.Config) (agent.Provider, error) {
	primaryCfg := cfg.Model
	if strings.TrimSpace(primaryCfg.Model) == "" {
		primaryCfg.Model = defaultModelForProvider(primaryCfg.Provider)
	}
	primary, err := agent.NewProvider(primaryCfg)
	if err != nil {
		return nil, err
	}
	primaryWrapped := agent.WithModel(primary, primaryCfg.Model)

	var fallbacks []agent.Provider
	for _, raw := range cfg.Model.FallbackProviders {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" || name == cfg.Model.Provider {
			continue
		}
		model := strings.TrimSpace(cfg.Model.FallbackModels[name])
		if model == "" {
			model = defaultModelForProvider(name)
		}
		keyEnv := strings.TrimSpace(cfg.Model.FallbackAPIKeyEnv[name])
		if keyEnv == "" {
			keyEnv = defaultAPIKeyEnvForProvider(name)
		}
		fp, err := agent.NewProvider(config.ModelConfig{
			Provider:  name,
			Model:     model,
			APIKeyEnv: keyEnv,
		})
		if err != nil {
			continue
		}
		fallbacks = append(fallbacks, agent.WithModel(fp, model))
	}
	if len(fallbacks) > 0 {
		return agent.NewFailoverProvider(primaryWrapped, fallbacks, nil), nil
	}
	return primaryWrapped, nil
}

func defaultAPIKeyEnvForProvider(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "gemini":
		return "GEMINI_API_KEY"
	case "cohere":
		return "COHERE_API_KEY"
	case "groq":
		return "GROQ_API_KEY"
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	case "mistral":
		return "MISTRAL_API_KEY"
	case "xai":
		return "XAI_API_KEY"
	case "cerebras":
		return "CEREBRAS_API_KEY"
	case "together":
		return "TOGETHER_API_KEY"
	case "fireworks":
		return "FIREWORKS_API_KEY"
	case "perplexity":
		return "PERPLEXITY_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "kimi":
		return "MOONSHOT_API_KEY"
	case "sambanova":
		return "SAMBANOVA_API_KEY"
	case "lambda":
		return "LAMBDA_API_KEY"
	case "openai-compat":
		return "OPENAI_API_KEY"
	case "ollama", "lmstudio":
		return "" // no key required
	default:
		return ""
	}
}

func defaultModelForProvider(provider string) string {
	switch provider {
	case "anthropic":
		return "claude-sonnet-4-20250514"
	case "openai":
		return "gpt-4o-mini"
	case "gemini":
		return "gemini-2.0-flash"
	case "ollama":
		return "llama3.1"
	case "cohere":
		return "command-r-plus-08-2024"
	case "groq":
		return "llama-3.3-70b-versatile"
	case "deepseek":
		return "deepseek-chat"
	case "mistral":
		return "mistral-large-latest"
	case "xai":
		return "grok-2-latest"
	case "cerebras":
		return "llama3.1-70b"
	case "together":
		return "meta-llama/Llama-3.3-70B-Instruct-Turbo"
	case "fireworks":
		return "accounts/fireworks/models/llama-v3p3-70b-instruct"
	case "perplexity":
		return "sonar-pro"
	case "openrouter":
		return "anthropic/claude-sonnet-4-6"
	case "kimi":
		return "moonshot-v1-8k"
	case "sambanova":
		return "Meta-Llama-3.1-70B-Instruct"
	case "lambda":
		return "llama3.1-70b-instruct-fp8"
	case "lmstudio":
		return ""
	default:
		return ""
	}
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func extractPathParam(path, prefix string) string {
	s := strings.TrimPrefix(path, prefix)
	s = strings.TrimSuffix(s, "/")
	return s
}

func extractSessionIDWithSuffix(path, prefix, suffix string) string {
	id := extractPathParam(path, prefix)
	if id == "" || !strings.HasSuffix(id, suffix) {
		return ""
	}
	id = strings.TrimSuffix(id, suffix)
	id = strings.TrimSuffix(id, "/")
	return strings.TrimSpace(id)
}

func extractSessionOverrides(metadata string) (map[string]any, bool) {
	if strings.TrimSpace(metadata) == "" {
		return map[string]any{}, false
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(metadata), &meta); err != nil {
		return map[string]any{}, false
	}
	raw, ok := meta["overrides"]
	if !ok {
		return map[string]any{}, false
	}
	overrides, ok := raw.(map[string]any)
	if !ok {
		return map[string]any{}, false
	}
	return overrides, true
}
