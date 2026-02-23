package rpc_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/openclio/openclio/internal/rpc"
	"github.com/openclio/openclio/internal/rpc/agentpb"
	"github.com/openclio/openclio/internal/storage"
)

const bufSize = 1024 * 1024

// dialBufconn starts a CoreServer over an in-memory listener and returns a client.
func dialBufconn(t *testing.T, srv *rpc.CoreServer) agentpb.AgentCoreClient {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	t.Cleanup(func() { lis.Close() })

	grpcSrv := grpc.NewServer()
	agentpb.RegisterAgentCoreServer(grpcSrv, srv)
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.GracefulStop)

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return agentpb.NewAgentCoreClient(conn)
}

func TestCoreServer_Health_NoAgent(t *testing.T) {
	srv := rpc.NewCoreServer(nil, nil, nil)
	client := dialBufconn(t, srv)

	resp, err := client.Health(context.Background(), &agentpb.Empty{})
	if err != nil {
		t.Fatalf("Health() error: %v", err)
	}
	if resp.Healthy {
		t.Error("expected healthy=false when no agent configured")
	}
}

func TestCoreServer_Health_WithDB(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}

	sessions := storage.NewSessionStore(db)
	messages := storage.NewMessageStore(db)

	// nil agent → health reports not ready
	srv := rpc.NewCoreServer(nil, sessions, messages)
	client := dialBufconn(t, srv)

	resp, err := client.Health(context.Background(), &agentpb.Empty{})
	if err != nil {
		t.Fatalf("Health() error: %v", err)
	}
	if resp.Healthy {
		t.Error("expected healthy=false with nil agent")
	}
	if resp.Message == "" {
		t.Error("expected non-empty message")
	}
}

func TestCoreServer_Chat_NoAgent(t *testing.T) {
	srv := rpc.NewCoreServer(nil, nil, nil)
	client := dialBufconn(t, srv)

	stream, err := client.Chat(context.Background(), &agentpb.InboundMessage{
		AdapterName: "test",
		UserId:      "user1",
		ChatId:      "chat1",
		Text:        "hello",
	})
	if err != nil {
		t.Fatalf("Chat() RPC failed: %v", err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Error("expected error when no agent configured")
	}
}
