package rpc

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/openclio/openclio/internal/rpc/agentpb"
)

// AdapterClient is a gRPC client the agent uses to deliver OutboundMessages
// to an external channel adapter implementing the ChannelAdapter service.
type AdapterClient struct {
	conn   *grpc.ClientConn
	client agentpb.ChannelAdapterClient
	name   string
}

// NewAdapterClient creates an AdapterClient connecting to addr.
// Additional grpc.DialOption values (e.g. a custom dialer for tests) may be
// passed as opts; insecure transport credentials are always added as a default.
func NewAdapterClient(addr, name string, opts ...grpc.DialOption) (*AdapterClient, error) {
	defaultOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	allOpts := append(defaultOpts, opts...)
	conn, err := grpc.NewClient(addr, allOpts...)
	if err != nil {
		return nil, fmt.Errorf("adapter client %s: connect to %s: %w", name, addr, err)
	}
	return &AdapterClient{
		conn:   conn,
		client: agentpb.NewChannelAdapterClient(conn),
		name:   name,
	}, nil
}

// NewAdapterClientFromConn wraps an existing gRPC connection as an AdapterClient.
// This is intended for tests that supply their own in-memory connection.
func NewAdapterClientFromConn(conn *grpc.ClientConn, name string) *AdapterClient {
	return &AdapterClient{
		conn:   conn,
		client: agentpb.NewChannelAdapterClient(conn),
		name:   name,
	}
}

// Send delivers an OutboundMessage to the external adapter.
func (c *AdapterClient) Send(msg *agentpb.OutboundMessage) error {
	_, err := c.client.SendMessage(context.Background(), msg)
	if err != nil {
		return fmt.Errorf("adapter client %s: send: %w", c.name, err)
	}
	return nil
}

// Health checks whether the external adapter is reachable and healthy.
func (c *AdapterClient) Health() error {
	status, err := c.client.Health(context.Background(), &agentpb.Empty{})
	if err != nil {
		return fmt.Errorf("adapter client %s: health check failed: %w", c.name, err)
	}
	if !status.Healthy {
		return fmt.Errorf("adapter client %s: unhealthy: %s", c.name, status.Message)
	}
	return nil
}

// Close shuts down the underlying gRPC connection.
func (c *AdapterClient) Close() error {
	return c.conn.Close()
}
