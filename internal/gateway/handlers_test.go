package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openclio/openclio/internal/agent"
	"github.com/openclio/openclio/internal/config"
	"github.com/openclio/openclio/internal/cost"
	agentcron "github.com/openclio/openclio/internal/cron"
	"github.com/openclio/openclio/internal/kg"
	"github.com/openclio/openclio/internal/plugin"
	"github.com/openclio/openclio/internal/storage"
	"github.com/openclio/openclio/internal/tools"
)

type mockQRAdapter struct {
	state plugin.QRCodeState
}

func (m *mockQRAdapter) Name() string { return "whatsapp" }

func (m *mockQRAdapter) Start(_ context.Context, _ chan<- plugin.InboundMessage, _ <-chan plugin.OutboundMessage) error {
	return nil
}

func (m *mockQRAdapter) Stop() {}

func (m *mockQRAdapter) Health() error { return nil }

func (m *mockQRAdapter) QRCodeState() plugin.QRCodeState { return m.state }

func setupTestHandlers(t *testing.T) *Handlers {
	// In-memory db
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	sessions := storage.NewSessionStore(db)
	messages := storage.NewMessageStore(db)
	kgStore := storage.NewKnowledgeGraphStore(db)
	messages.SetKnowledgeGraphStore(kgStore)

	cfg := config.DefaultConfig()
	tracker := cost.NewTracker(db)
	profiles := storage.NewAgentProfileStore(db)

	h := NewHandlers(nil, sessions, messages, nil, tracker, cfg)
	h.AttachAgentProfiles(profiles)
	h.AttachKnowledgeGraphStore(kgStore)
	return h
}

func TestMetricsEndpoint(t *testing.T) {
	h := setupTestHandlers(t)

	req := httptest.NewRequest("GET", "/api/v1/metrics", nil)
	rec := httptest.NewRecorder()

	h.Metrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "text/plain; version=0.0.4" {
		t.Errorf("wrong content type")
	}
}

func TestPrivacyEndpoint(t *testing.T) {
	h := setupTestHandlers(t)

	req := httptest.NewRequest("GET", "/api/v1/privacy", nil)
	rec := httptest.NewRecorder()

	h.Privacy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if _, ok := payload["privacy"]; !ok {
		t.Fatalf("expected privacy key in payload, got %#v", payload)
	}
	if _, ok := payload["totals"]; !ok {
		t.Fatalf("expected totals key in payload, got %#v", payload)
	}
	if _, ok := payload["providers"]; !ok {
		t.Fatalf("expected providers key in payload, got %#v", payload)
	}
}

func TestOverviewEndpoint(t *testing.T) {
	h := setupTestHandlers(t)
	req := httptest.NewRequest("GET", "/api/v1/overview", nil)
	rec := httptest.NewRecorder()

	h.Overview(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("expected status=ok, got %#v", payload["status"])
	}
	if _, ok := payload["counts"]; !ok {
		t.Fatalf("expected counts in overview payload, got %#v", payload)
	}
	if _, ok := payload["privacy"]; !ok {
		t.Fatalf("expected privacy in overview payload, got %#v", payload)
	}
	if _, ok := payload["embeddings"]; !ok {
		t.Fatalf("expected embeddings in overview payload, got %#v", payload)
	}
}

func TestChannelsEndpoint_DefaultAllowlistMode(t *testing.T) {
	h := setupTestHandlers(t)
	req := httptest.NewRequest("GET", "/api/v1/channels", nil)
	rec := httptest.NewRecorder()

	h.Channels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Allowlist struct {
			AllowAll bool `json:"allow_all"`
		} `json:"allowlist"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if payload.Allowlist.AllowAll != h.cfg.Channels.AllowAll {
		t.Fatalf("expected allow_all=%v, got %v", h.cfg.Channels.AllowAll, payload.Allowlist.AllowAll)
	}
}

func TestChannelWhatsAppQREndpoint(t *testing.T) {
	h := setupTestHandlers(t)
	mgr := plugin.NewManager(nil)
	mgr.Register(&mockQRAdapter{
		state: plugin.QRCodeState{
			Event:     "code",
			Code:      "openclio-qr-code",
			UpdatedAt: time.Now().UTC(),
		},
	})
	h.AttachRuntimeSources(mgr, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/whatsapp/qr", nil)
	rec := httptest.NewRecorder()
	h.ChannelWhatsAppQR(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"brand":"openclio"`) {
		t.Fatalf("expected openclio brand in response, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"available":true`) {
		t.Fatalf("expected available=true in response, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"openclio-qr-code"`) {
		t.Fatalf("expected qr code in response, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"qr_image":"data:image/png;base64,`) {
		t.Fatalf("expected qr image data url in response, got %s", rec.Body.String())
	}
}

func TestMemoryEndpoints(t *testing.T) {
	h := setupTestHandlers(t)
	if h.knowledgeGraph == nil {
		t.Fatal("knowledge graph store should be attached")
	}

	if err := h.knowledgeGraph.IngestExtracted(101, []kg.Entity{
		{Type: "company", Name: "Acme", Confidence: 0.9},
		{Type: "person", Name: "Sarah", Confidence: 0.8},
	}, []kg.Relation{
		{From: "Sarah", Relation: "works_at", To: "Acme"},
	}); err != nil {
		t.Fatalf("ingest knowledge: %v", err)
	}

	nodesReq := httptest.NewRequest("GET", "/api/v1/memory/nodes?limit=20", nil)
	nodesRec := httptest.NewRecorder()
	h.MemoryNodes(nodesRec, nodesReq)
	if nodesRec.Code != http.StatusOK {
		t.Fatalf("expected 200 memory nodes, got %d body=%s", nodesRec.Code, nodesRec.Body.String())
	}
	var nodesPayload struct {
		Nodes []storage.KGNode `json:"nodes"`
		Count int              `json:"count"`
	}
	if err := json.Unmarshal(nodesRec.Body.Bytes(), &nodesPayload); err != nil {
		t.Fatalf("invalid nodes payload: %v", err)
	}
	if nodesPayload.Count < 2 {
		t.Fatalf("expected at least 2 nodes, got %d", nodesPayload.Count)
	}

	searchReq := httptest.NewRequest("GET", "/api/v1/memory/search?q=acme", nil)
	searchRec := httptest.NewRecorder()
	h.MemorySearch(searchRec, searchReq)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("expected 200 memory search, got %d body=%s", searchRec.Code, searchRec.Body.String())
	}
	var searchPayload struct {
		Nodes []storage.KGNode `json:"nodes"`
		Count int              `json:"count"`
	}
	if err := json.Unmarshal(searchRec.Body.Bytes(), &searchPayload); err != nil {
		t.Fatalf("invalid search payload: %v", err)
	}
	if searchPayload.Count == 0 {
		t.Fatalf("expected non-empty search result")
	}

	edgesReq := httptest.NewRequest("GET", "/api/v1/memory/edges", nil)
	edgesRec := httptest.NewRecorder()
	h.MemoryEdges(edgesRec, edgesReq)
	if edgesRec.Code != http.StatusOK {
		t.Fatalf("expected 200 memory edges, got %d body=%s", edgesRec.Code, edgesRec.Body.String())
	}
	var edgesPayload struct {
		Edges []storage.KGEdge `json:"edges"`
		Count int              `json:"count"`
	}
	if err := json.Unmarshal(edgesRec.Body.Bytes(), &edgesPayload); err != nil {
		t.Fatalf("invalid edges payload: %v", err)
	}
	if edgesPayload.Count == 0 {
		t.Fatalf("expected non-empty edges")
	}

	targetID := searchPayload.Nodes[0].ID
	delReq := httptest.NewRequest("DELETE", "/api/v1/memory/nodes/"+strconv.FormatInt(targetID, 10), nil)
	delRec := httptest.NewRecorder()
	h.MemoryNodeDelete(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("expected 200 memory delete, got %d body=%s", delRec.Code, delRec.Body.String())
	}
}

func TestSessionsEndpoints(t *testing.T) {
	h := setupTestHandlers(t)

	// Create a session directly in DB
	s, _ := h.sessions.Create("tester", "u1")

	// Test ListSessions
	req := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	rec := httptest.NewRecorder()
	h.ListSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 ListSessions, got %d", rec.Code)
	}

	var resp struct {
		Sessions []storage.Session `json:"sessions"`
		Count    int               `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Count != 1 || resp.Sessions[0].ID != s.ID {
		t.Errorf("expected 1 session with ID %s", s.ID)
	}

	// Test GetSession
	req = httptest.NewRequest("GET", "/api/v1/sessions/"+s.ID, nil)
	rec = httptest.NewRecorder()
	h.GetSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 GetSession, got %d", rec.Code)
	}
}

func TestGetSession_NotFound(t *testing.T) {
	h := setupTestHandlers(t)

	req := httptest.NewRequest("GET", "/api/v1/sessions/invalid-id", nil)
	rec := httptest.NewRecorder()
	h.GetSession(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestConfigEndpoints(t *testing.T) {
	h := setupTestHandlers(t)

	// GET config
	getReq := httptest.NewRequest("GET", "/api/v1/config", nil)
	getRec := httptest.NewRecorder()
	h.GetConfig(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from GetConfig, got %d", getRec.Code)
	}

	// PUT config
	payload := `{
		"model": {"provider":"openai","model":"gpt-4o-mini"},
		"agent": {"max_tool_iterations": 12},
		"context": {
			"max_tokens_per_call": 9000,
			"proactive_compaction": 0.7,
			"compaction_keep_recent": 7
		}
	}`
	putReq := httptest.NewRequest("PUT", "/api/v1/config", bytes.NewBufferString(payload))
	putRec := httptest.NewRecorder()
	h.UpdateConfig(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from UpdateConfig, got %d body=%s", putRec.Code, putRec.Body.String())
	}

	if h.cfg.Model.Provider != "openai" {
		t.Fatalf("expected provider openai, got %q", h.cfg.Model.Provider)
	}
	if h.cfg.Model.Model != "gpt-4o-mini" {
		t.Fatalf("expected model gpt-4o-mini, got %q", h.cfg.Model.Model)
	}
	if h.cfg.Agent.MaxToolIterations != 12 {
		t.Fatalf("expected max_tool_iterations 12, got %d", h.cfg.Agent.MaxToolIterations)
	}
	if h.cfg.Context.MaxTokensPerCall != 9000 {
		t.Fatalf("expected max_tokens_per_call 9000, got %d", h.cfg.Context.MaxTokensPerCall)
	}
	if h.cfg.Context.ProactiveCompaction != 0.7 {
		t.Fatalf("expected proactive_compaction 0.7, got %f", h.cfg.Context.ProactiveCompaction)
	}
	if h.cfg.Context.CompactionKeepRecent != 7 {
		t.Fatalf("expected compaction_keep_recent 7, got %d", h.cfg.Context.CompactionKeepRecent)
	}
}

func TestUpdateConfigRejectsInvalidContext(t *testing.T) {
	h := setupTestHandlers(t)

	payload := `{"context":{"proactive_compaction":1.5}}`
	req := httptest.NewRequest("PUT", "/api/v1/config", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.UpdateConfig(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestUpdateConfigSyncsBrowserToolAtRuntime(t *testing.T) {
	h := setupTestHandlers(t)
	h.cfg.Tools.Browser.Enabled = false
	registry := tools.NewRegistry(h.cfg.Tools, t.TempDir(), "")
	h.AttachToolRegistry(registry)

	if registry.HasTool("browser") {
		t.Fatal("expected browser tool to start disabled in registry")
	}

	enableReq := httptest.NewRequest("PUT", "/api/v1/config", strings.NewReader(`{"tools":{"browser":{"enabled":true}}}`))
	enableRec := httptest.NewRecorder()
	h.UpdateConfig(enableRec, enableReq)
	if enableRec.Code != http.StatusOK {
		t.Fatalf("expected 200 enabling browser, got %d body=%s", enableRec.Code, enableRec.Body.String())
	}
	if !registry.HasTool("browser") {
		t.Fatal("expected browser tool to be registered after runtime enable")
	}
	if !strings.Contains(enableRec.Body.String(), "runtime.tools.browser") {
		t.Fatalf("expected runtime browser update marker in response, got %s", enableRec.Body.String())
	}

	disableReq := httptest.NewRequest("PUT", "/api/v1/config", strings.NewReader(`{"tools":{"browser":{"enabled":false}}}`))
	disableRec := httptest.NewRecorder()
	h.UpdateConfig(disableRec, disableReq)
	if disableRec.Code != http.StatusOK {
		t.Fatalf("expected 200 disabling browser, got %d body=%s", disableRec.Code, disableRec.Body.String())
	}
	if registry.HasTool("browser") {
		t.Fatal("expected browser tool to be removed after runtime disable")
	}
}

func TestSetupEndpoint_ConfiguresProviderAndEnv(t *testing.T) {
	h := setupTestHandlers(t)
	h.agent = agent.NewAgent(nil, nil, nil, config.AgentConfig{}, "")
	h.setupRequired = true
	h.setupReason = "provider not configured"
	h.dataDir = t.TempDir()

	_ = os.Unsetenv("OPENAI_API_KEY")

	payload := `{"provider":"openai","model":"gpt-4o-mini","api_key":"sk-test"}`
	req := httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.Setup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	if required, _ := h.setupState(); required {
		t.Fatal("expected setupRequired=false after successful setup")
	}
	if h.cfg.Model.Provider != "openai" {
		t.Fatalf("expected provider openai, got %s", h.cfg.Model.Provider)
	}
	if h.agent.Provider() == nil {
		t.Fatal("expected hot-reloaded agent provider to be set")
	}

	envPath := filepath.Join(h.dataDir, ".env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("expected .env file to exist, read failed: %v", err)
	}
	if !strings.Contains(string(data), "OPENAI_API_KEY=") {
		t.Fatalf("expected OPENAI_API_KEY entry in .env, got: %s", string(data))
	}

	cfgPath := filepath.Join(h.dataDir, "config.yaml")
	savedCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("expected saved config to be loadable, got error: %v", err)
	}
	if savedCfg.Model.Provider != "openai" {
		t.Fatalf("expected persisted provider openai, got %q", savedCfg.Model.Provider)
	}
	if savedCfg.Model.Model != "gpt-4o-mini" {
		t.Fatalf("expected persisted model gpt-4o-mini, got %q", savedCfg.Model.Model)
	}
	if savedCfg.Model.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("expected persisted api_key_env OPENAI_API_KEY, got %q", savedCfg.Model.APIKeyEnv)
	}
}

func TestSetupEndpoint_RequiresAPIKeyForHostedProviders(t *testing.T) {
	h := setupTestHandlers(t)
	h.agent = agent.NewAgent(nil, nil, nil, config.AgentConfig{}, "")
	h.setupRequired = true
	h.dataDir = t.TempDir()

	payload := `{"provider":"anthropic","model":"claude-sonnet-4-20250514","api_key":""}`
	req := httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.Setup(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestSetupEndpoint_OpenAICompatUsesDefaultAPIKeyEnv(t *testing.T) {
	h := setupTestHandlers(t)
	h.agent = agent.NewAgent(nil, nil, nil, config.AgentConfig{}, "")
	h.setupRequired = true
	h.dataDir = t.TempDir()

	_ = os.Unsetenv("OPENAI_API_KEY")

	payload := `{"provider":"openai-compat","model":"gpt-4o-mini","base_url":"https://api.example.com/v1","api_key":"sk-compat"}`
	req := httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.Setup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if h.cfg.Model.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("expected API key env OPENAI_API_KEY, got %q", h.cfg.Model.APIKeyEnv)
	}
	if h.cfg.Model.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("expected base_url to be set, got %q", h.cfg.Model.BaseURL)
	}
	if got := os.Getenv("OPENAI_API_KEY"); got != "sk-compat" {
		t.Fatalf("expected OPENAI_API_KEY to be set in process env, got %q", got)
	}

	data, err := os.ReadFile(filepath.Join(h.dataDir, ".env"))
	if err != nil {
		t.Fatalf("expected .env file, got read error: %v", err)
	}
	if !strings.Contains(string(data), "OPENAI_API_KEY=") {
		t.Fatalf("expected OPENAI_API_KEY entry in .env, got: %s", string(data))
	}
}

func TestSetupEndpoint_OpenAICompatRequiresModel(t *testing.T) {
	h := setupTestHandlers(t)
	h.agent = agent.NewAgent(nil, nil, nil, config.AgentConfig{}, "")
	h.setupRequired = true
	h.dataDir = t.TempDir()

	payload := `{"provider":"openai-compat","model":"","base_url":"https://api.example.com/v1","api_key":"sk-compat"}`
	req := httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.Setup(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "model is required") {
		t.Fatalf("expected model validation error, got %s", rec.Body.String())
	}
}

func TestChatInject_InsertsMessage(t *testing.T) {
	h := setupTestHandlers(t)
	sess, err := h.sessions.Create("ws", "tester")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/chat/inject",
		strings.NewReader(`{"session_id":"`+sess.ID+`","role":"system","content":"manual note"}`),
	)
	rec := httptest.NewRecorder()
	h.ChatInject(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	msgs, err := h.messages.GetBySession(sess.ID)
	if err != nil {
		t.Fatalf("failed to load messages: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatalf("expected injected message to exist")
	}
	last := msgs[len(msgs)-1]
	if last.Role != "system" || last.Content != "manual note" {
		t.Fatalf("unexpected injected message: role=%q content=%q", last.Role, last.Content)
	}
}

func TestChatAbort_CancelsActiveRun(t *testing.T) {
	h := setupTestHandlers(t)
	sess, err := h.sessions.Create("ws", "tester")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	cancelled := make(chan struct{}, 1)
	cancel := func() { cancelled <- struct{}{} }
	runID := h.registerActiveRun(sess.ID, cancel, "test")
	if runID == "" {
		t.Fatalf("expected non-empty run id")
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/chat/abort",
		strings.NewReader(`{"session_id":"`+sess.ID+`"}`),
	)
	rec := httptest.NewRecorder()
	h.ChatAbort(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case <-cancelled:
	default:
		t.Fatalf("expected cancel function to be called")
	}
	if h.abortActiveRun(sess.ID) {
		t.Fatalf("expected run to be removed after abort")
	}
}

func TestChannelAllowlistEndpoints(t *testing.T) {
	h := setupTestHandlers(t)
	h.allowlist = plugin.NewAllowlist(t.TempDir(), true)

	// Approve one sender.
	approveReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/channels/allowlist/approve",
		strings.NewReader(`{"adapter":"telegram","user_id":"12345"}`),
	)
	approveRec := httptest.NewRecorder()
	h.ChannelAllowlistApprove(approveRec, approveReq)
	if approveRec.Code != http.StatusOK {
		t.Fatalf("approve expected 200, got %d body=%s", approveRec.Code, approveRec.Body.String())
	}

	// Set strict mode.
	modeReq := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/channels/allowlist",
		strings.NewReader(`{"allow_all":false}`),
	)
	modeRec := httptest.NewRecorder()
	h.ChannelAllowlistMode(modeRec, modeReq)
	if modeRec.Code != http.StatusOK {
		t.Fatalf("mode expected 200, got %d body=%s", modeRec.Code, modeRec.Body.String())
	}
	if h.allowlist.AllowAll() {
		t.Fatalf("expected allow_all=false after mode update")
	}

	// List should include entry.
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/channels/allowlist", nil)
	listRec := httptest.NewRecorder()
	h.ChannelAllowlist(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list expected 200, got %d body=%s", listRec.Code, listRec.Body.String())
	}

	// Revoke sender.
	revokeReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/channels/allowlist/revoke",
		strings.NewReader(`{"adapter":"telegram","user_id":"12345"}`),
	)
	revokeRec := httptest.NewRecorder()
	h.ChannelAllowlistRevoke(revokeRec, revokeReq)
	if revokeRec.Code != http.StatusOK {
		t.Fatalf("revoke expected 200, got %d body=%s", revokeRec.Code, revokeRec.Body.String())
	}
}

func TestSessionStatsAndOverridesEndpoints(t *testing.T) {
	h := setupTestHandlers(t)
	sess, err := h.sessions.Create("ws", "tester")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	if _, err := h.messages.Insert(sess.ID, "user", "hello", 10); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := h.messages.Insert(sess.ID, "assistant", "hi", 20); err != nil {
		t.Fatalf("insert assistant: %v", err)
	}

	statsReq := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sess.ID+"/stats", nil)
	statsRec := httptest.NewRecorder()
	h.GetSessionStats(statsRec, statsReq)
	if statsRec.Code != http.StatusOK {
		t.Fatalf("stats expected 200, got %d body=%s", statsRec.Code, statsRec.Body.String())
	}
	var statsResp struct {
		Counts struct {
			Messages int `json:"messages"`
			Tokens   int `json:"tokens"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(statsRec.Body.Bytes(), &statsResp); err != nil {
		t.Fatalf("stats json: %v", err)
	}
	if statsResp.Counts.Messages != 2 || statsResp.Counts.Tokens != 30 {
		t.Fatalf("unexpected stats: %+v", statsResp.Counts)
	}

	putReq := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/sessions/"+sess.ID+"/overrides",
		strings.NewReader(`{"overrides":{"max_tool_iterations":3,"debug":true}}`),
	)
	putRec := httptest.NewRecorder()
	h.UpdateSessionOverrides(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("overrides put expected 200, got %d body=%s", putRec.Code, putRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sess.ID+"/overrides", nil)
	getRec := httptest.NewRecorder()
	h.GetSessionOverrides(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("overrides get expected 200, got %d body=%s", getRec.Code, getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), "max_tool_iterations") {
		t.Fatalf("expected overrides payload in response, got %s", getRec.Body.String())
	}
}

func TestInstanceActionPing(t *testing.T) {
	h := setupTestHandlers(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/instances/action", strings.NewReader(`{"action":"ping"}`))
	rec := httptest.NewRecorder()
	h.InstanceAction(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "pong") {
		t.Fatalf("expected pong response, got %s", rec.Body.String())
	}
}

func TestCronJobCRUDAndEnableEndpoints(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cron-handler-test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	cfg := config.DefaultConfig()
	h := NewHandlers(nil, storage.NewSessionStore(db), storage.NewMessageStore(db), nil, cost.NewTracker(db), cfg)
	h.scheduler = agentcron.NewScheduler(nil, nil, nil, nil, nil, db, nil)

	createReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/cron/jobs",
		strings.NewReader(`{"name":"db-job","schedule":"*/5 * * * *","prompt":"hello","channel":"webchat","session_mode":"shared"}`),
	)
	createRec := httptest.NewRecorder()
	h.CronJobCreate(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create expected 200, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/cron/jobs", nil)
	listRec := httptest.NewRecorder()
	h.CronJobs(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list expected 200, got %d body=%s", listRec.Code, listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), `"name":"db-job"`) {
		t.Fatalf("expected db-job in list, got %s", listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), `"source":"db"`) {
		t.Fatalf("expected source=db in list, got %s", listRec.Body.String())
	}

	disableReq := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/cron/jobs/db-job/enabled",
		strings.NewReader(`{"enabled":false}`),
	)
	disableRec := httptest.NewRecorder()
	h.CronJobSetEnabled(disableRec, disableReq)
	if disableRec.Code != http.StatusOK {
		t.Fatalf("disable expected 200, got %d body=%s", disableRec.Code, disableRec.Body.String())
	}
	if !strings.Contains(disableRec.Body.String(), `"Enabled":false`) &&
		!strings.Contains(disableRec.Body.String(), `"enabled":false`) {
		t.Fatalf("expected enabled=false response, got %s", disableRec.Body.String())
	}

	updateReq := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/cron/jobs/db-job",
		strings.NewReader(`{"schedule":"0 * * * *","prompt":"updated prompt","channel":"telegram","session_mode":"isolated"}`),
	)
	updateRec := httptest.NewRecorder()
	h.CronJobUpdate(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update expected 200, got %d body=%s", updateRec.Code, updateRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/cron/jobs/db-job", nil)
	deleteRec := httptest.NewRecorder()
	h.CronJobDelete(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete expected 200, got %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestCronJobDeleteRejectsConfigJob(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cron-handler-config-test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	cfg := config.DefaultConfig()
	h := NewHandlers(nil, storage.NewSessionStore(db), storage.NewMessageStore(db), nil, cost.NewTracker(db), cfg)
	h.scheduler = agentcron.NewScheduler(nil, nil, nil, nil, nil, db, nil)
	if err := h.scheduler.Add(agentcron.Job{
		Name:        "cfg-job",
		Schedule:    "*/10 * * * *",
		Prompt:      "from config",
		SessionMode: "isolated",
	}); err != nil {
		t.Fatalf("add config job: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/cron/jobs/cfg-job", nil)
	rec := httptest.NewRecorder()
	h.CronJobDelete(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for config job delete, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAgentProfilesCRUDAndActivateEndpoints(t *testing.T) {
	h := setupTestHandlers(t)

	createReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents",
		strings.NewReader(`{"name":"researcher","description":"research profile","provider":"anthropic","model":"claude-sonnet-4-20250514"}`),
	)
	createRec := httptest.NewRecorder()
	h.Agents(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create expected 200, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	var createResp struct {
		Profile storage.AgentProfile `json:"profile"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("create json: %v", err)
	}
	if createResp.Profile.ID == "" {
		t.Fatalf("expected created profile id")
	}

	activateReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents/"+createResp.Profile.ID+"/activate",
		nil,
	)
	activateRec := httptest.NewRecorder()
	h.AgentProfileActivate(activateRec, activateReq)
	if activateRec.Code != http.StatusOK {
		t.Fatalf("activate expected 200, got %d body=%s", activateRec.Code, activateRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	listRec := httptest.NewRecorder()
	h.Agents(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list expected 200, got %d body=%s", listRec.Code, listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), `"active_id":"`+createResp.Profile.ID+`"`) {
		t.Fatalf("expected active_id in list response, got %s", listRec.Body.String())
	}
}

func TestSessionAgentBindingEndpoints(t *testing.T) {
	h := setupTestHandlers(t)
	profile, err := h.agentProfiles.Create(storage.AgentProfile{
		Name:         "default-profile",
		Provider:     "openai",
		Model:        "gpt-4o-mini",
		SystemPrompt: "You are helpful.",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	session, err := h.sessions.Create("ws", "tester")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	putReq := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/sessions/"+session.ID+"/agent",
		strings.NewReader(`{"agent_profile_id":"`+profile.ID+`"}`),
	)
	putRec := httptest.NewRecorder()
	h.UpdateSessionAgentProfile(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("bind expected 200, got %d body=%s", putRec.Code, putRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+session.ID+"/agent", nil)
	getRec := httptest.NewRecorder()
	h.GetSessionAgentProfile(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get expected 200, got %d body=%s", getRec.Code, getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), `"agent_profile_id":"`+profile.ID+`"`) {
		t.Fatalf("expected bound profile id in get response, got %s", getRec.Body.String())
	}

	clearReq := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/sessions/"+session.ID+"/agent",
		strings.NewReader(`{"agent_profile_id":""}`),
	)
	clearRec := httptest.NewRecorder()
	h.UpdateSessionAgentProfile(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear expected 200, got %d body=%s", clearRec.Code, clearRec.Body.String())
	}
}

func TestBindSessionToActiveProfileHelper(t *testing.T) {
	h := setupTestHandlers(t)
	active, err := h.agentProfiles.Create(storage.AgentProfile{
		Name:         "active-profile",
		Provider:     "openai",
		Model:        "gpt-4o-mini",
		SystemPrompt: "You are active.",
		IsActive:     true,
	})
	if err != nil {
		t.Fatalf("create active profile: %v", err)
	}

	session, err := h.sessions.Create("api", "user-1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	h.bindSessionToActiveProfile(session.ID)

	reloaded, err := h.sessions.Get(session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if reloaded.AgentProfileID != active.ID {
		t.Fatalf("expected session profile %s, got %s", active.ID, reloaded.AgentProfileID)
	}
}

func TestSkillsMutationEndpoints(t *testing.T) {
	h := setupTestHandlers(t)
	h.dataDir = t.TempDir()

	createReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/skills",
		strings.NewReader(`{"name":"ops","content":"# Ops\nRunbooks","enabled":true}`),
	)
	createRec := httptest.NewRecorder()
	h.Skills(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create skill expected 200, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	disableReq := httptest.NewRequest(http.MethodPut, "/api/v1/skills/ops/disable", nil)
	disableRec := httptest.NewRecorder()
	h.SkillSetEnabled(disableRec, disableReq, false)
	if disableRec.Code != http.StatusOK {
		t.Fatalf("disable skill expected 200, got %d body=%s", disableRec.Code, disableRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)
	listRec := httptest.NewRecorder()
	h.Skills(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list skills expected 200, got %d body=%s", listRec.Code, listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), `"name":"ops"`) {
		t.Fatalf("expected ops in entries, got %s", listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), `"skills":["ops"]`) {
		t.Fatalf("ops should be disabled and absent from enabled skills list, got %s", listRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/skills/ops", nil)
	deleteRec := httptest.NewRecorder()
	h.SkillDelete(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete skill expected 200, got %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestNodeActionCapabilitiesAndTest(t *testing.T) {
	h := setupTestHandlers(t)
	h.mcpServers = []config.MCPServerConfig{
		{Name: "localfs", Command: "sh", Args: []string{"-c", "echo ok"}},
	}

	capReq := httptest.NewRequest(http.MethodPost, "/api/v1/nodes/action",
		strings.NewReader(`{"name":"localfs","action":"capabilities"}`))
	capRec := httptest.NewRecorder()
	h.NodeAction(capRec, capReq)
	if capRec.Code != http.StatusOK {
		t.Fatalf("capabilities expected 200, got %d body=%s", capRec.Code, capRec.Body.String())
	}

	testReq := httptest.NewRequest(http.MethodPost, "/api/v1/nodes/action",
		strings.NewReader(`{"name":"localfs","action":"test"}`))
	testRec := httptest.NewRecorder()
	h.NodeAction(testRec, testReq)
	if testRec.Code != http.StatusOK {
		t.Fatalf("node test expected 200, got %d body=%s", testRec.Code, testRec.Body.String())
	}
	if !strings.Contains(testRec.Body.String(), `"healthy":true`) {
		t.Fatalf("expected healthy=true from node test, got %s", testRec.Body.String())
	}
}

type mockMCPStatusSource struct {
	statuses      []MCPRuntimeStatus
	restartErr    error
	restartedName string
}

func (m *mockMCPStatusSource) SnapshotMCPStatus() []MCPRuntimeStatus {
	out := make([]MCPRuntimeStatus, len(m.statuses))
	copy(out, m.statuses)
	return out
}

func (m *mockMCPStatusSource) RestartMCPServer(name string) error {
	m.restartedName = name
	return m.restartErr
}

func TestNodesIncludesMCPRuntimeStatus(t *testing.T) {
	h := setupTestHandlers(t)
	h.mcpServers = []config.MCPServerConfig{
		{Name: "localfs", Command: "sh"},
	}
	h.AttachMCPStatusSource(&mockMCPStatusSource{
		statuses: []MCPRuntimeStatus{
			{
				Name:            "localfs",
				Status:          "retrying",
				Healthy:         false,
				LastHealthError: "failed ping",
				RestartCount:    2,
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	rec := httptest.NewRecorder()
	h.Nodes(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("nodes expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"retrying"`) {
		t.Fatalf("expected runtime status in nodes response, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"restart_count":2`) {
		t.Fatalf("expected restart_count in nodes response, got %s", rec.Body.String())
	}
}

func TestNodeActionRestartUsesMCPRuntimeSource(t *testing.T) {
	h := setupTestHandlers(t)
	h.mcpServers = []config.MCPServerConfig{
		{Name: "localfs", Command: "sh"},
	}
	src := &mockMCPStatusSource{}
	h.AttachMCPStatusSource(src)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/nodes/action",
		strings.NewReader(`{"name":"localfs","action":"restart"}`))
	rec := httptest.NewRecorder()
	h.NodeAction(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("node restart expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if src.restartedName != "localfs" {
		t.Fatalf("expected restart request for localfs, got %q", src.restartedName)
	}
}

func TestDebugActionAndEventsEndpoints(t *testing.T) {
	h := setupTestHandlers(t)

	actionReq := httptest.NewRequest(http.MethodPost, "/api/v1/debug/action", strings.NewReader(`{"action":"gc"}`))
	actionRec := httptest.NewRecorder()
	h.DebugAction(actionRec, actionReq)
	if actionRec.Code != http.StatusOK {
		t.Fatalf("debug action expected 200, got %d body=%s", actionRec.Code, actionRec.Body.String())
	}

	eventsReq := httptest.NewRequest(http.MethodGet, "/api/v1/debug/events?limit=10", nil)
	eventsRec := httptest.NewRecorder()
	h.DebugEvents(eventsRec, eventsReq)
	if eventsRec.Code != http.StatusOK {
		t.Fatalf("debug events expected 200, got %d body=%s", eventsRec.Code, eventsRec.Body.String())
	}
	if !strings.Contains(eventsRec.Body.String(), `"debug_gc"`) {
		t.Fatalf("expected debug_gc event, got %s", eventsRec.Body.String())
	}
}

func TestLogsFilterAndExportEndpoints(t *testing.T) {
	h := setupTestHandlers(t)
	logDir := t.TempDir()
	logPath := filepath.Join(logDir, "app.log")
	if err := os.WriteFile(logPath, []byte(strings.Join([]string{
		`{"level":"info","msg":"startup complete"}`,
		`{"level":"error","msg":"db failed"}`,
		`{"level":"info","msg":"request done"}`,
	}, "\n")+"\n"), 0600); err != nil {
		t.Fatalf("write test log: %v", err)
	}
	h.cfg.Logging.Output = logPath

	filterReq := httptest.NewRequest(http.MethodGet, "/api/v1/logs?lines=200&level=error", nil)
	filterRec := httptest.NewRecorder()
	h.Logs(filterRec, filterReq)
	if filterRec.Code != http.StatusOK {
		t.Fatalf("logs filter expected 200, got %d body=%s", filterRec.Code, filterRec.Body.String())
	}
	if !strings.Contains(filterRec.Body.String(), "db failed") || strings.Contains(filterRec.Body.String(), "startup complete") {
		t.Fatalf("unexpected filtered logs response: %s", filterRec.Body.String())
	}

	exportReq := httptest.NewRequest(http.MethodGet, "/api/v1/logs/export?format=text&contains=request", nil)
	exportRec := httptest.NewRecorder()
	h.LogsExport(exportRec, exportReq)
	if exportRec.Code != http.StatusOK {
		t.Fatalf("logs export expected 200, got %d body=%s", exportRec.Code, exportRec.Body.String())
	}
	if ct := exportRec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("expected text/plain export content type, got %q", ct)
	}
	if !strings.Contains(exportRec.Body.String(), "request done") {
		t.Fatalf("expected exported log content, got %s", exportRec.Body.String())
	}
}
