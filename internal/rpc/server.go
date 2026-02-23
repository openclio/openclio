// Package rpc implements the gRPC AgentCore service that out-of-process
// channel adapters connect to. Adapters send InboundMessages and receive
// streaming OutboundMessages back over the same gRPC connection.
package rpc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"

	"google.golang.org/grpc"

	"github.com/openclio/openclio/internal/agent"
	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/rpc/agentpb"
	"github.com/openclio/openclio/internal/storage"
)

// CoreServer implements the AgentCore gRPC service.
type CoreServer struct {
	agentpb.UnimplementedAgentCoreServer
	agent    *agent.Agent
	sessions *storage.SessionStore
	messages *storage.MessageStore
	grpcSrv  *grpc.Server
}

// NewCoreServer creates a CoreServer. Pass nil for agent to run in health-only mode.
func NewCoreServer(
	agentInstance *agent.Agent,
	sessions *storage.SessionStore,
	messages *storage.MessageStore,
) *CoreServer {
	return &CoreServer{
		agent:    agentInstance,
		sessions: sessions,
		messages: messages,
	}
}

// Chat receives a user message and streams the agent response back token by token.
func (s *CoreServer) Chat(req *agentpb.InboundMessage, stream grpc.ServerStreamingServer[agentpb.OutboundMessage]) error {
	if s.agent == nil {
		return fmt.Errorf("agent unavailable: no provider configured")
	}
	if req.Text == "" {
		return fmt.Errorf("text is required")
	}

	// Get or create session keyed by adapter + user_id
	session, err := s.sessions.GetByChannelSender(req.AdapterName, req.UserId)
	if err != nil {
		if !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("looking up session: %w", err)
		}
		session, err = s.sessions.Create(req.AdapterName, req.UserId)
		if err != nil {
			return fmt.Errorf("creating session: %w", err)
		}
	}

	if s.messages != nil {
		tokens := agentctx.EstimateTokens(req.Text)
		if _, err := s.messages.Insert(session.ID, "user", req.Text, tokens); err != nil {
			log.Printf("gRPC: failed to store user message: %v", err)
		}
	}

	var msgProvider agentctx.MessageProvider
	if s.messages != nil {
		msgProvider = &grpcMsgProvider{store: s.messages, sessionID: session.ID}
	}

	runCtx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	stopStreaming := false
	sendFailed := false

	resp, err := s.agent.RunStream(
		runCtx,
		session.ID,
		req.Text,
		msgProvider,
		nil,
		func(token string) {
			if stopStreaming {
				return
			}

			if err := stream.Send(&agentpb.OutboundMessage{
				AdapterName: req.AdapterName,
				ChatId:      req.ChatId,
				Text:        token,
				RequestId:   req.RequestId,
			}); err != nil {
				log.Printf("gRPC stream send failed: %v", err)
				sendFailed = true
				stopStreaming = true
				cancel()
				return
			}
			if err := stream.Context().Err(); err != nil {
				log.Printf("gRPC stream context cancelled: %v", err)
				stopStreaming = true
				cancel()
				return
			}
		},
		nil,
	)
	if sendFailed || stopStreaming {
		return nil
	}
	if err != nil {
		return fmt.Errorf("agent error: %w", err)
	}

	if s.messages != nil && resp != nil {
		respTokens := agentctx.EstimateTokens(resp.Text)
		if _, err := s.messages.Insert(session.ID, "assistant", resp.Text, respTokens); err != nil {
			log.Printf("gRPC: failed to store assistant message: %v", err)
		}
	}

	return nil
}

// Health reports whether the core is ready to serve requests.
func (s *CoreServer) Health(_ context.Context, _ *agentpb.Empty) (*agentpb.HealthStatus, error) {
	if s.agent == nil {
		return &agentpb.HealthStatus{Healthy: false, Message: "no provider configured"}, nil
	}
	return &agentpb.HealthStatus{Healthy: true, Message: "ok"}, nil
}

// ListenAndServe starts the gRPC server on addr (e.g. "127.0.0.1:18790").
// It blocks until the server is stopped.
func (s *CoreServer) ListenAndServe(addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("gRPC listen on %s: %w", addr, err)
	}
	s.grpcSrv = grpc.NewServer()
	agentpb.RegisterAgentCoreServer(s.grpcSrv, s)
	log.Printf("gRPC AgentCore listening on %s", addr)
	return s.grpcSrv.Serve(lis)
}

// Stop gracefully shuts down the gRPC server.
func (s *CoreServer) Stop() {
	if s.grpcSrv != nil {
		s.grpcSrv.GracefulStop()
	}
}

// grpcMsgProvider adapts storage.MessageStore to agentctx.MessageProvider.
type grpcMsgProvider struct {
	store     *storage.MessageStore
	sessionID string
}

func (p *grpcMsgProvider) GetRecentMessages(sessionID string, limit int) ([]agentctx.ContextMessage, error) {
	msgs, err := p.store.GetRecent(sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]agentctx.ContextMessage, len(msgs))
	for i, m := range msgs {
		out[i] = agentctx.ContextMessage{Role: m.Role, Content: m.Content}
	}
	return out, nil
}

func (p *grpcMsgProvider) GetStoredEmbeddings(sessionID string) ([]agentctx.StoredEmbedding, error) {
	embedded, err := p.store.GetEmbeddings(sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]agentctx.StoredEmbedding, len(embedded))
	for i, e := range embedded {
		out[i] = agentctx.StoredEmbedding{
			MessageID: e.ID,
			Role:      e.Role,
			Content:   e.Content,
			Tokens:    e.Tokens,
			Embedding: e.Embedding,
		}
	}
	return out, nil
}

func (p *grpcMsgProvider) SearchKnowledge(query, nodeType string, limit int) ([]agentctx.KnowledgeNode, error) {
	nodes, err := p.store.SearchKnowledge(query, nodeType, limit)
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
