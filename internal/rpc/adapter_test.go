package rpc_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/openclio/openclio/internal/plugin"
	"github.com/openclio/openclio/internal/rpc"
	"github.com/openclio/openclio/internal/rpc/agentpb"
)

// mockAdapter is a test implementation of plugin.Adapter.
type mockAdapter struct {
	name    string
	healthy bool
}

func (m *mockAdapter) Name() string { return m.name }
func (m *mockAdapter) Start(ctx context.Context, _ chan<- plugin.InboundMessage, _ <-chan plugin.OutboundMessage) error {
	<-ctx.Done()
	return nil
}
func (m *mockAdapter) Stop() {}
func (m *mockAdapter) Health() error {
	if !m.healthy {
		return fmt.Errorf("mock adapter %s: not healthy", m.name)
	}
	return nil
}

// startAdapterSrv starts a gRPC ChannelAdapter server on a bufconn listener
// and returns the listener and a cleanup function.
func startAdapterSrv(t *testing.T, adapter plugin.Adapter, inbound chan<- plugin.InboundMessage) (*bufconn.Listener, func()) {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)

	srv := grpc.NewServer()
	agentpb.RegisterChannelAdapterServer(srv, rpc.NewAdapterServer(adapter, inbound))

	go func() {
		if err := srv.Serve(lis); err != nil {
			// Ignore errors from graceful stop
		}
	}()

	cleanup := func() {
		srv.GracefulStop()
		lis.Close()
	}
	return lis, cleanup
}

// dialAdapterBufconn creates a gRPC ClientConn over a bufconn listener.
func dialAdapterBufconn(t *testing.T, lis *bufconn.Listener) *grpc.ClientConn {
	t.Helper()

	conn, err := grpc.NewClient(
		"passthrough:///adapter-bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to create gRPC client connection: %v", err)
	}
	return conn
}

func TestAdapterServer_SendMessage(t *testing.T) {
	inbound := make(chan plugin.InboundMessage, 4)
	adapter := &mockAdapter{name: "test", healthy: true}

	lis, cleanup := startAdapterSrv(t, adapter, inbound)
	defer cleanup()

	conn := dialAdapterBufconn(t, lis)
	defer conn.Close()

	client := rpc.NewAdapterClientFromConn(conn, "test")

	err := client.Send(&agentpb.OutboundMessage{
		AdapterName: "test",
		ChatId:      "chat-123",
		Text:        "hello from agent",
	})
	if err != nil {
		t.Fatalf("Send() returned error: %v", err)
	}

	// Assert the mock adapter received the message via inbound channel
	select {
	case msg := <-inbound:
		if msg.Text != "hello from agent" {
			t.Errorf("expected text 'hello from agent', got %q", msg.Text)
		}
		if msg.ChatID != "chat-123" {
			t.Errorf("expected ChatID 'chat-123', got %q", msg.ChatID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for inbound message")
	}
}

func TestAdapterServer_Health_Healthy(t *testing.T) {
	adapter := &mockAdapter{name: "test", healthy: true}
	lis, cleanup := startAdapterSrv(t, adapter, nil)
	defer cleanup()

	conn := dialAdapterBufconn(t, lis)
	defer conn.Close()

	client := rpc.NewAdapterClientFromConn(conn, "test")
	if err := client.Health(); err != nil {
		t.Errorf("Health() returned unexpected error: %v", err)
	}
}

func TestAdapterServer_Health_Unhealthy(t *testing.T) {
	adapter := &mockAdapter{name: "test", healthy: false}
	lis, cleanup := startAdapterSrv(t, adapter, nil)
	defer cleanup()

	conn := dialAdapterBufconn(t, lis)
	defer conn.Close()

	client := rpc.NewAdapterClientFromConn(conn, "test")
	err := client.Health()
	if err == nil {
		t.Error("expected error for unhealthy adapter")
	}
}

func TestAdapterServer_Health_NilAdapter(t *testing.T) {
	lis, cleanup := startAdapterSrv(t, nil, nil)
	defer cleanup()

	conn := dialAdapterBufconn(t, lis)
	defer conn.Close()

	client := rpc.NewAdapterClientFromConn(conn, "nil-adapter")
	err := client.Health()
	if err == nil {
		t.Error("expected error when adapter is nil")
	}
}
