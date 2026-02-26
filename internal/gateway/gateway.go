// Package gateway provides the HTTP and WebSocket server for the agent.
// It handles routing, authentication, rate limiting, and security headers.
package gateway

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/openclio/openclio/internal/agent"
	"github.com/openclio/openclio/internal/config"
	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/cost"
	agentcron "github.com/openclio/openclio/internal/cron"
	"github.com/openclio/openclio/internal/plugin"
	"github.com/openclio/openclio/internal/plugin/adapters/webchat"
	"github.com/openclio/openclio/internal/storage"
	"github.com/openclio/openclio/internal/tools"
)

// Server is the HTTP/WebSocket gateway server.
type Server struct {
	httpServer  *http.Server
	handlers    *Handlers
	authToken   string
	cfg         config.GatewayConfig
	rateLimiter *RateLimiter
}

// NewServer creates a new gateway server.
func NewServer(
	cfg config.GatewayConfig,
	fullCfg *config.Config,
	agentInstance *agent.Agent,
	db *storage.DB,
	contextEngine *agentctx.Engine,
	tracker *cost.Tracker,
	authToken string,
	embedders ...storage.Embedder,
) *Server {
	sessions := storage.NewSessionStore(db)
	messages := storage.NewMessageStore(db, embedders...)
	embeddingErrors := storage.NewEmbeddingErrorStore(db)
	knowledgeGraph := storage.NewKnowledgeGraphStore(db)
	messages.SetEmbeddingErrorStore(embeddingErrors)
	messages.SetKnowledgeGraphStore(knowledgeGraph)
	agentProfiles := storage.NewAgentProfileStore(db)
	privacyStore := storage.NewPrivacyStore(db)
	handlers := NewHandlers(agentInstance, sessions, messages, contextEngine, tracker, fullCfg)
	handlers.AttachAgentProfiles(agentProfiles)
	handlers.AttachPrivacyStore(privacyStore)
	handlers.AttachEmbeddingErrors(embeddingErrors)
	handlers.AttachKnowledgeGraphStore(knowledgeGraph)

	mux := http.NewServeMux()

	// WebChat UI at root
	mux.Handle("/", webchat.Handler())

	// Native Prometheus Metrics
	mux.HandleFunc("/metrics", handlers.Metrics)

	// API routes
	mux.HandleFunc("/api/v1/health", handlers.Health)
	mux.HandleFunc("/api/v1/privacy", handlers.Privacy)
	mux.HandleFunc("/api/v1/memory/nodes", handlers.MemoryNodes)
	mux.HandleFunc("/api/v1/memory/nodes/", handlers.MemoryNodeDelete)
	mux.HandleFunc("/api/v1/memory/edges", handlers.MemoryEdges)
	mux.HandleFunc("/api/v1/memory/search", handlers.MemorySearch)
	mux.HandleFunc("/api/v1/overview", handlers.Overview)
	mux.HandleFunc("/api/v1/assistant", handlers.Assistant)
	mux.HandleFunc("/api/v1/channels", handlers.Channels)
	mux.HandleFunc("/api/v1/channels/whatsapp/qr", handlers.ChannelWhatsAppQR)
	mux.HandleFunc("/api/v1/channels/action", handlers.ChannelAction)
	mux.HandleFunc("/api/v1/channels/allowlist", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handlers.ChannelAllowlist(w, r)
		case http.MethodPut:
			handlers.ChannelAllowlistMode(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "use GET or PUT")
		}
	})
	mux.HandleFunc("/api/v1/channels/allowlist/approve", handlers.ChannelAllowlistApprove)
	mux.HandleFunc("/api/v1/channels/allowlist/revoke", handlers.ChannelAllowlistRevoke)
	mux.HandleFunc("/api/v1/instances", handlers.Instances)
	mux.HandleFunc("/api/v1/instances/action", handlers.InstanceAction)
	mux.HandleFunc("/api/v1/agents", handlers.Agents)
	mux.HandleFunc("/api/v1/agents/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/activate") {
			if r.Method == http.MethodPost {
				handlers.AgentProfileActivate(w, r)
				return
			}
			writeError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		switch r.Method {
		case http.MethodPut:
			handlers.AgentProfileUpdate(w, r)
		case http.MethodDelete:
			handlers.AgentProfileDelete(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "use PUT or DELETE")
		}
	})
	mux.HandleFunc("/api/v1/skills", handlers.Skills)
	mux.HandleFunc("/api/v1/skills/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/enable") {
			handlers.SkillSetEnabled(w, r, true)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/disable") {
			handlers.SkillSetEnabled(w, r, false)
			return
		}
		if r.Method == http.MethodDelete {
			handlers.SkillDelete(w, r)
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "use DELETE or PUT /enable|/disable")
	})
	mux.HandleFunc("/api/v1/nodes", handlers.Nodes)
	mux.HandleFunc("/api/v1/nodes/action", handlers.NodeAction)
	mux.HandleFunc("/api/v1/debug", handlers.Debug)
	mux.HandleFunc("/api/v1/debug/action", handlers.DebugAction)
	mux.HandleFunc("/api/v1/debug/events", handlers.DebugEvents)
	mux.HandleFunc("/api/v1/logs", handlers.Logs)
	mux.HandleFunc("/api/v1/logs/export", handlers.LogsExport)
	mux.HandleFunc("/api/v1/docs/openapi", handlers.OpenAPIDoc)
	mux.HandleFunc("/api/v1/tools/health", handlers.ToolsHealth)
	mux.HandleFunc("/api/v1/chat", handlers.Chat)
	mux.HandleFunc("/api/v1/chat/abort", handlers.ChatAbort)
	mux.HandleFunc("/api/v1/chat/inject", handlers.ChatInject)
	mux.HandleFunc("/api/v1/cron/jobs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handlers.CronJobs(w, r)
		case http.MethodPost:
			handlers.CronJobCreate(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "use GET or POST")
		}
	})
	mux.HandleFunc("/api/v1/cron/jobs/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/enabled") {
			if r.Method == http.MethodPut {
				handlers.CronJobSetEnabled(w, r)
				return
			}
			writeError(w, http.StatusMethodNotAllowed, "use PUT")
			return
		}
		switch r.Method {
		case http.MethodPut:
			handlers.CronJobUpdate(w, r)
		case http.MethodDelete:
			handlers.CronJobDelete(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "use PUT or DELETE")
		}
	})
	mux.HandleFunc("/api/v1/cron/history", handlers.CronHistory)
	mux.HandleFunc("/api/v1/cron/run", handlers.CronRun)
	mux.HandleFunc("/api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handlers.ListSessions(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "use GET")
		}
	})
	mux.HandleFunc("/api/v1/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/stats") {
			if r.Method == http.MethodGet {
				handlers.GetSessionStats(w, r)
				return
			}
			writeError(w, http.StatusMethodNotAllowed, "use GET")
			return
		}
		if strings.HasSuffix(r.URL.Path, "/overrides") {
			switch r.Method {
			case http.MethodGet:
				handlers.GetSessionOverrides(w, r)
			case http.MethodPut:
				handlers.UpdateSessionOverrides(w, r)
			default:
				writeError(w, http.StatusMethodNotAllowed, "use GET or PUT")
			}
			return
		}
		if strings.HasSuffix(r.URL.Path, "/agent") {
			switch r.Method {
			case http.MethodGet:
				handlers.GetSessionAgentProfile(w, r)
			case http.MethodPut:
				handlers.UpdateSessionAgentProfile(w, r)
			default:
				writeError(w, http.StatusMethodNotAllowed, "use GET or PUT")
			}
			return
		}
		switch r.Method {
		case http.MethodGet:
			handlers.GetSession(w, r)
		case http.MethodDelete:
			handlers.DeleteSession(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "use GET or DELETE")
		}
	})
	mux.HandleFunc("/api/v1/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handlers.GetConfig(w, r)
		case http.MethodPut:
			handlers.UpdateConfig(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "use GET or PUT")
		}
	})
	// Setup endpoint remains behind AuthMiddleware (unlike /api/v1/health).
	mux.HandleFunc("/api/v1/setup", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			handlers.Setup(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "use POST")
		}
	})
	mux.HandleFunc("/ws", handlers.HandleWebSocket)

	// Middleware chain: logs → trace ID → security headers → CORS → rate limit → auth
	var handler http.Handler = mux
	rateLimiter := NewRateLimiter(100) // 100 req/min per IP
	handler = rateLimiter.Middleware(handler)
	handler = BodySizeLimitMiddleware(handler)
	handler = CORSMiddleware(handler)
	handler = SecurityHeadersMiddleware(handler)
	if authToken != "" {
		handler = AuthMiddleware(authToken)(handler)
	}
	handler = RequestIDMiddleware(handler)
	handler = RequestLoggerMiddleware(handler)

	addr := fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port)

	return &Server{
		httpServer: &http.Server{
			Addr:         addr,
			Handler:      handler,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 120 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
		handlers:    handlers,
		authToken:   authToken,
		cfg:         cfg,
		rateLimiter: rateLimiter,
	}
}

// Start begins listening for connections.
// If TLSCertFile and TLSKeyFile are set in config, the server starts in HTTPS mode.
// If Bind is not loopback and TLS is not configured, the server refuses to start —
// exposing plain HTTP to the network is a security violation.
func (s *Server) Start() error {
	isTLS := s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != ""
	isLoopback := s.cfg.Bind == "127.0.0.1" || s.cfg.Bind == "localhost" || s.cfg.Bind == "::1"

	// Security check: refuse plain HTTP on network interface
	if !isLoopback && !isTLS {
		return fmt.Errorf(
			"security: cannot bind plain HTTP to %s — set gateway.tls_cert_file and "+
				"gateway.tls_key_file, or restrict gateway.bind to 127.0.0.1",
			s.cfg.Bind,
		)
	}

	listener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.httpServer.Addr, err)
	}

	if isLoopback {
		log.Printf("Gateway listening on %s (loopback only — safe)", s.httpServer.Addr)
	} else {
		log.Printf("Gateway listening on %s (TLS — network accessible)", s.httpServer.Addr)
	}

	if s.authToken != "" {
		log.Printf("  ↳ Auth token required")
	}

	if isTLS {
		log.Printf("  ↳ TLS enabled (cert: %s)", s.cfg.TLSCertFile)
		return s.httpServer.ServeTLS(listener, s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
	}
	return s.httpServer.Serve(listener)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("Gateway shutting down...")
	defer func() {
		if s.rateLimiter != nil {
			s.rateLimiter.Stop()
		}
	}()
	return s.httpServer.Shutdown(ctx)
}

// Handler returns the root http.Handler, suitable for use with httptest.NewServer.
func (s *Server) Handler() http.Handler { return s.httpServer.Handler }

// AttachRuntimeSources wires optional runtime components used by dashboard endpoints.
func (s *Server) AttachRuntimeSources(manager *plugin.Manager, scheduler *agentcron.Scheduler, allowlist *plugin.Allowlist, mcpServers []config.MCPServerConfig) {
	if s == nil || s.handlers == nil {
		return
	}
	s.handlers.AttachRuntimeSources(manager, scheduler, allowlist, mcpServers)
}

// AttachToolRegistry wires runtime tool registry for live config-driven tool updates.
func (s *Server) AttachToolRegistry(registry *tools.Registry) {
	if s == nil || s.handlers == nil {
		return
	}
	s.handlers.AttachToolRegistry(registry)
}

// AttachChannelRuntime wires runtime channel lifecycle controls.
func (s *Server) AttachChannelRuntime(connector tools.ChannelConnector, lifecycle tools.ChannelLifecycleController) {
	if s == nil || s.handlers == nil {
		return
	}
	s.handlers.AttachChannelRuntime(connector, lifecycle)
}

// AttachMCPStatusSource wires MCP runtime status/restart source.
func (s *Server) AttachMCPStatusSource(source MCPRuntimeStatusSource) {
	if s == nil || s.handlers == nil {
		return
	}
	s.handlers.AttachMCPStatusSource(source)
}
