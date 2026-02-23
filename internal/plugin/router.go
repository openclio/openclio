package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/openclio/openclio/internal/agent"
	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/storage"

	"github.com/google/uuid"
)

// Router connects adapter messages to the agent loop.
// It enforces Layer 2 security: adapter token registration and unknown
// sender approval before messages reach the agent.
type Router struct {
	agentInstance *agent.Agent
	sessions      *storage.SessionStore
	messages      *storage.MessageStore
	contextEngine *agentctx.Engine
	manager       *Manager
	allowlist     *Allowlist
	logger        *slog.Logger
}

// NewRouter creates a new router with optional allowlist enforcement.
func NewRouter(
	agentInstance *agent.Agent,
	sessions *storage.SessionStore,
	messages *storage.MessageStore,
	contextEngine *agentctx.Engine,
	manager *Manager,
	logger *slog.Logger,
) *Router {
	return &Router{
		agentInstance: agentInstance,
		sessions:      sessions,
		messages:      messages,
		contextEngine: contextEngine,
		manager:       manager,
		logger:        logger,
	}
}

// WithAllowlist attaches an allowlist to the router.
// When set, unknown senders are blocked and shown a rejection message.
func (r *Router) WithAllowlist(al *Allowlist) *Router {
	r.allowlist = al
	return r
}

// sessionCache maps "adapterName:chatID" → sessionID with mutex protection.
// Entries are TTL-evicted to prevent unbounded growth.
var sessionCache = struct {
	mu        sync.Mutex
	m         map[string]sessionCacheEntry
	lastPrune time.Time
	ttl       time.Duration
}{m: make(map[string]sessionCacheEntry), ttl: 24 * time.Hour}

type sessionCacheEntry struct {
	sessionID string
	lastSeen  time.Time
}

const sessionCachePruneInterval = 5 * time.Minute

// Run processes inbound messages from all adapters indefinitely.
func (r *Router) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-r.manager.Inbound():
			if !ok {
				return
			}
			go r.handleMessage(ctx, msg)
		}
	}
}

func (r *Router) handleMessage(ctx context.Context, msg InboundMessage) {
	// ── Layer 2: Unknown sender check ─────────────────────────────────────────
	// If an allowlist is configured and allow_all=false, reject unknown senders.
	if r.allowlist != nil && !r.allowlist.IsAllowed(msg.AdapterName, msg.UserID) {
		r.logger.Warn("blocked unknown sender",
			"adapter", msg.AdapterName,
			"user_id", msg.UserID,
		)
		r.manager.Send(msg.AdapterName, OutboundMessage{
			ChatID: msg.ChatID,
			UserID: msg.UserID,
			Text: "🔒 *Access denied.*\n\n" +
				"This is a private AI agent. Your ID is not on the approved sender list.\n\n" +
				fmt.Sprintf("Your sender ID: `%s`\n", msg.UserID) +
				"Ask the owner to run:\n" +
				fmt.Sprintf("`openclio allow %s %s`", msg.AdapterName, msg.UserID),
		})
		return
	}

	sessionID, err := r.getOrCreateSessionID(msg)
	if err != nil {
		r.logger.Error("failed to create session", "adapter", msg.AdapterName, "error", err)
		return
	}

	// Store user message
	tokens := agentctx.EstimateTokens(msg.Text)
	if _, err := r.messages.Insert(sessionID, "user", msg.Text, tokens); err != nil {
		r.logger.Warn("failed to store message", "error", err)
	}

	msgProvider := &routerMsgProvider{
		messages:  r.messages,
		sessionID: sessionID,
	}

	traceID := uuid.New().String()[:8]
	ctx = context.WithValue(ctx, routerCtxKey("trace"), traceID)

	r.logger.Info("routing message",
		"adapter", msg.AdapterName,
		"session", sessionID[:8],
		"trace", traceID,
	)

	resp, err := r.agentInstance.Run(ctx, sessionID, msg.Text, msgProvider, nil)
	if err != nil {
		r.logger.Error("agent error", "error", err, "trace", traceID)
		r.manager.Send(msg.AdapterName, OutboundMessage{
			ChatID: msg.ChatID,
			UserID: msg.UserID,
			Text:   fmt.Sprintf("⚠ Error: %v", err),
		})
		return
	}

	respTokens := agentctx.EstimateTokens(resp.Text)
	r.messages.Insert(sessionID, "assistant", resp.Text, respTokens)

	if err := r.manager.Send(msg.AdapterName, OutboundMessage{
		ChatID: msg.ChatID,
		UserID: msg.UserID,
		Text:   resp.Text,
	}); err != nil {
		r.logger.Error("failed to send response", "adapter", msg.AdapterName, "error", err)
	}
}

func (r *Router) getOrCreateSessionID(msg InboundMessage) (string, error) {
	cacheKey := msg.AdapterName + ":" + msg.ChatID
	now := time.Now()

	sessionCache.mu.Lock()
	defer sessionCache.mu.Unlock()

	pruneSessionCacheLocked(now)

	if cached, ok := sessionCache.m[cacheKey]; ok {
		if now.Sub(cached.lastSeen) <= sessionCache.ttl {
			cached.lastSeen = now
			sessionCache.m[cacheKey] = cached
			return cached.sessionID, nil
		}
		delete(sessionCache.m, cacheKey)
	}

	session, err := r.sessions.Create(msg.AdapterName, msg.UserID)
	if err != nil {
		return "", err
	}

	sessionCache.m[cacheKey] = sessionCacheEntry{
		sessionID: session.ID,
		lastSeen:  now,
	}
	return session.ID, nil
}

func pruneSessionCacheLocked(now time.Time) {
	if sessionCache.ttl <= 0 {
		return
	}
	if !sessionCache.lastPrune.IsZero() && now.Sub(sessionCache.lastPrune) < sessionCachePruneInterval {
		return
	}
	for k, entry := range sessionCache.m {
		if now.Sub(entry.lastSeen) > sessionCache.ttl {
			delete(sessionCache.m, k)
		}
	}
	sessionCache.lastPrune = now
}

type routerCtxKey string

type routerMsgProvider struct {
	messages  *storage.MessageStore
	sessionID string
}

func (p *routerMsgProvider) GetRecentMessages(sessionID string, limit int) ([]agentctx.ContextMessage, error) {
	msgs, err := p.messages.GetRecent(sessionID, limit)
	if err != nil {
		return nil, err
	}
	var result []agentctx.ContextMessage
	for _, m := range msgs {
		result = append(result, agentctx.ContextMessage{Role: m.Role, Content: m.Content})
	}
	return result, nil
}

func (p *routerMsgProvider) GetStoredEmbeddings(sessionID string) ([]agentctx.StoredEmbedding, error) {
	msgs, err := p.messages.GetEmbeddings(sessionID)
	if err != nil {
		return nil, err
	}
	var result []agentctx.StoredEmbedding
	for _, m := range msgs {
		result = append(result, agentctx.StoredEmbedding{
			MessageID: m.ID, SessionID: m.SessionID, Role: m.Role,
			Content: m.Content, Summary: m.Summary, Tokens: m.Tokens, Embedding: m.Embedding,
		})
	}
	return result, nil
}

func (p *routerMsgProvider) SearchKnowledge(query, nodeType string, limit int) ([]agentctx.KnowledgeNode, error) {
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

func (p *routerMsgProvider) GetOldMessages(sessionID string, keepRecentTurns int) ([]agent.CompactionMessage, error) {
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

func (p *routerMsgProvider) ArchiveMessages(sessionID string, olderThanID int64) (int64, error) {
	return p.messages.ArchiveMessages(sessionID, olderThanID)
}

func (p *routerMsgProvider) InsertCompactionSummary(sessionID, content string, tokens int) error {
	_, err := p.messages.Insert(sessionID, "system", content, tokens)
	return err
}
