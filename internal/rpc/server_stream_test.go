package rpc

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	"github.com/openclio/openclio/internal/agent"
	"github.com/openclio/openclio/internal/config"
	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/rpc/agentpb"
	"github.com/openclio/openclio/internal/storage"
)

type streamTestProvider struct {
	tokens []string
}

type noOpEmbedder struct{}

func (noOpEmbedder) Embed(text string) ([]float32, error) {
	_ = text
	return nil, nil
}

func (noOpEmbedder) Dimensions() int { return 0 }

func (p *streamTestProvider) Name() string { return "stream-test" }

func (p *streamTestProvider) Chat(ctx context.Context, req agent.ChatRequest) (*agent.ChatResponse, error) {
	_ = ctx
	_ = req
	return &agent.ChatResponse{Content: ""}, nil
}

func (p *streamTestProvider) Stream(ctx context.Context, req agent.ChatRequest) (<-chan agent.StreamChunk, error) {
	_ = req
	ch := make(chan agent.StreamChunk)
	go func() {
		defer close(ch)
		for _, tok := range p.tokens {
			select {
			case <-ctx.Done():
				return
			case ch <- agent.StreamChunk{Text: tok}:
			}
			time.Sleep(5 * time.Millisecond)
		}
		select {
		case <-ctx.Done():
			return
		case ch <- agent.StreamChunk{Done: true}:
		}
	}()
	return ch, nil
}

type fakeOutboundStream struct {
	ctx             context.Context
	cancel          context.CancelFunc
	sendErrAt       int
	cancelAfterSend bool
	sendCalls       int
	sent            []*agentpb.OutboundMessage
}

func newFakeOutboundStream(t *testing.T) *fakeOutboundStream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return &fakeOutboundStream{
		ctx:    ctx,
		cancel: cancel,
	}
}

func (f *fakeOutboundStream) Send(msg *agentpb.OutboundMessage) error {
	f.sendCalls++
	if f.sendErrAt > 0 && f.sendCalls == f.sendErrAt {
		return errors.New("send failed")
	}
	f.sent = append(f.sent, msg)
	if f.cancelAfterSend {
		f.cancel()
	}
	return nil
}

func (f *fakeOutboundStream) SetHeader(metadata.MD) error { return nil }
func (f *fakeOutboundStream) SendHeader(metadata.MD) error {
	return nil
}
func (f *fakeOutboundStream) SetTrailer(metadata.MD)   {}
func (f *fakeOutboundStream) Context() context.Context { return f.ctx }
func (f *fakeOutboundStream) SendMsg(any) error        { return nil }
func (f *fakeOutboundStream) RecvMsg(any) error        { return nil }

func newCoreServerForStreamTests(t *testing.T, tokens []string) (*CoreServer, *storage.SessionStore, *storage.MessageStore) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "rpc-stream.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}

	sessions := storage.NewSessionStore(db)
	messages := storage.NewMessageStore(db)

	eng := agentctx.NewEngine(noOpEmbedder{}, 8000, 10)
	cfg := config.DefaultConfig()
	a := agent.NewAgent(&streamTestProvider{tokens: tokens}, eng, nil, cfg.Agent, "test-model")

	return NewCoreServer(a, sessions, messages), sessions, messages
}

func mustSessionByPair(t *testing.T, sessions *storage.SessionStore, channel, sender string) *storage.Session {
	t.Helper()
	s, err := sessions.GetByChannelSender(channel, sender)
	if err != nil {
		t.Fatalf("GetByChannelSender: %v", err)
	}
	return s
}

func TestCoreServerChatPersistsUserAndAssistantOnSuccess(t *testing.T) {
	srv, sessions, messages := newCoreServerForStreamTests(t, []string{"hello ", "world"})
	stream := newFakeOutboundStream(t)

	err := srv.Chat(&agentpb.InboundMessage{
		AdapterName: "webchat",
		UserId:      "u1",
		ChatId:      "c1",
		RequestId:   "r1",
		Text:        "hi",
	}, stream)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	if stream.sendCalls < 2 {
		t.Fatalf("expected >=2 streamed tokens, got %d", stream.sendCalls)
	}

	session := mustSessionByPair(t, sessions, "webchat", "u1")
	history, err := messages.GetRecent(session.ID, 10)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 stored messages, got %d", len(history))
	}
	if history[0].Role != "user" || history[0].Content != "hi" {
		t.Fatalf("unexpected user message: role=%q content=%q", history[0].Role, history[0].Content)
	}
	if history[1].Role != "assistant" || history[1].Content != "hello world" {
		t.Fatalf("unexpected assistant message: role=%q content=%q", history[1].Role, history[1].Content)
	}
}

func TestCoreServerChatStopsWhenStreamSendFails(t *testing.T) {
	srv, sessions, messages := newCoreServerForStreamTests(t, []string{"one", "two", "three"})
	stream := newFakeOutboundStream(t)
	stream.sendErrAt = 1

	err := srv.Chat(&agentpb.InboundMessage{
		AdapterName: "webchat",
		UserId:      "u2",
		ChatId:      "c2",
		RequestId:   "r2",
		Text:        "hello",
	}, stream)
	if err != nil {
		t.Fatalf("expected clean stop on send failure, got err=%v", err)
	}

	if stream.sendCalls != 1 {
		t.Fatalf("expected stream to stop after first failed send, got %d send calls", stream.sendCalls)
	}

	session := mustSessionByPair(t, sessions, "webchat", "u2")
	history, err := messages.GetRecent(session.ID, 10)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected only user message stored after send failure, got %d entries", len(history))
	}
	if history[0].Role != "user" {
		t.Fatalf("expected stored role=user, got %q", history[0].Role)
	}
}

func TestCoreServerChatStopsWhenClientContextCancelled(t *testing.T) {
	srv, sessions, messages := newCoreServerForStreamTests(t, []string{"alpha", "beta", "gamma"})
	stream := newFakeOutboundStream(t)
	stream.cancelAfterSend = true

	err := srv.Chat(&agentpb.InboundMessage{
		AdapterName: "webchat",
		UserId:      "u3",
		ChatId:      "c3",
		RequestId:   "r3",
		Text:        "cancel me",
	}, stream)
	if err != nil {
		t.Fatalf("expected clean stop on client cancellation, got err=%v", err)
	}

	if stream.sendCalls != 1 {
		t.Fatalf("expected stream to stop after client cancellation, got %d send calls", stream.sendCalls)
	}

	session := mustSessionByPair(t, sessions, "webchat", "u3")
	history, err := messages.GetRecent(session.ID, 10)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected only user message stored after cancellation, got %d entries", len(history))
	}
	if history[0].Role != "user" {
		t.Fatalf("expected stored role=user, got %q", history[0].Role)
	}
}
