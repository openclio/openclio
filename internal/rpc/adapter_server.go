package rpc

import (
	"context"

	"github.com/openclio/openclio/internal/plugin"
	"github.com/openclio/openclio/internal/rpc/agentpb"
)

// AdapterServer is a reference ChannelAdapterServer that wraps an in-process
// plugin.Adapter. It is used for testing and as a starting point for building
// real out-of-process adapter binaries.
//
// StreamMessages is left as Unimplemented — it is a v2 bidirectional feature.
type AdapterServer struct {
	agentpb.UnimplementedChannelAdapterServer
	adapter plugin.Adapter
	inbound chan<- plugin.InboundMessage
}

// NewAdapterServer creates an AdapterServer backed by the given plugin.Adapter.
// inbound is the channel into which received messages are forwarded; it may be
// nil if the server is used for health-only purposes.
func NewAdapterServer(adapter plugin.Adapter, inbound chan<- plugin.InboundMessage) *AdapterServer {
	return &AdapterServer{
		adapter: adapter,
		inbound: inbound,
	}
}

// SendMessage delivers an OutboundMessage from the agent core to this adapter.
// In a real adapter binary, this would forward the text to the external platform.
// In this reference implementation, the message is forwarded to the inbound
// channel so tests can assert receipt.
func (s *AdapterServer) SendMessage(ctx context.Context, msg *agentpb.OutboundMessage) (*agentpb.SendResult, error) {
	if s.inbound != nil {
		im := plugin.InboundMessage{
			AdapterName: msg.AdapterName,
			ChatID:      msg.ChatId,
			Text:        msg.Text,
		}
		select {
		case s.inbound <- im:
		case <-ctx.Done():
			return &agentpb.SendResult{Ok: false, Error: ctx.Err().Error()}, nil
		}
	}
	return &agentpb.SendResult{Ok: true}, nil
}

// Health delegates to the underlying plugin.Adapter's Health() method.
func (s *AdapterServer) Health(ctx context.Context, _ *agentpb.Empty) (*agentpb.HealthStatus, error) {
	if s.adapter == nil {
		return &agentpb.HealthStatus{Healthy: false, Message: "no adapter configured"}, nil
	}
	if err := s.adapter.Health(); err != nil {
		return &agentpb.HealthStatus{Healthy: false, Message: err.Error()}, nil
	}
	return &agentpb.HealthStatus{Healthy: true, Message: "ok"}, nil
}
