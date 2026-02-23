package plugin

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mockAdapter is a test adapter that echoes messages back.
type mockAdapter struct {
	name  string
	stopC chan struct{}
}

func (m *mockAdapter) Name() string { return m.name }

func (m *mockAdapter) Start(ctx context.Context, inbound chan<- InboundMessage, outbound <-chan OutboundMessage) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-m.stopC:
			return nil
		}
	}
}

func (m *mockAdapter) Stop() {
	select {
	case <-m.stopC:
	default:
		close(m.stopC)
	}
}

func (m *mockAdapter) Health() error {
	return nil
}

type runOneAdapter struct {
	name    string
	started chan struct{}
	stopC   chan struct{}
}

func (a *runOneAdapter) Name() string { return a.name }
func (a *runOneAdapter) Start(ctx context.Context, inbound chan<- InboundMessage, outbound <-chan OutboundMessage) error {
	select {
	case a.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return nil
	case <-a.stopC:
		return errors.New("stopped")
	}
}
func (a *runOneAdapter) Stop() {
	select {
	case <-a.stopC:
	default:
		close(a.stopC)
	}
}
func (a *runOneAdapter) Health() error { return nil }

func TestManagerRegister(t *testing.T) {
	mgr := NewManager(nil)
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}

	a := &mockAdapter{name: "test", stopC: make(chan struct{})}
	mgr.Register(a)

	if len(mgr.adapters) != 1 {
		t.Errorf("expected 1 adapter, got %d", len(mgr.adapters))
	}
	if _, ok := mgr.outbound["test"]; !ok {
		t.Error("expected outbound channel for 'test' adapter")
	}
}

func TestManagerRegisterSkipsDuplicateNames(t *testing.T) {
	mgr := NewManager(nil)
	mgr.Register(&mockAdapter{name: "dup", stopC: make(chan struct{})})
	mgr.Register(&mockAdapter{name: "dup", stopC: make(chan struct{})})

	if len(mgr.adapters) != 1 {
		t.Fatalf("expected duplicate adapter registration to be skipped, got %d adapters", len(mgr.adapters))
	}
}

func TestManagerSend(t *testing.T) {
	mgr := NewManager(nil)
	a := &mockAdapter{name: "test", stopC: make(chan struct{})}
	mgr.Register(a)

	// Send a message and receive it on the outbound channel
	go func() {
		err := mgr.Send("test", OutboundMessage{ChatID: "chat1", Text: "hello"})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	}()

	ch := mgr.outbound["test"]
	select {
	case msg := <-ch:
		if msg.Text != "hello" {
			t.Errorf("expected 'hello', got %q", msg.Text)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for message")
	}
}

func TestManagerSendUnknown(t *testing.T) {
	mgr := NewManager(nil)
	err := mgr.Send("nonexistent", OutboundMessage{})
	if err == nil {
		t.Error("expected error for unknown adapter")
	}
}

func TestManagerStartStop(t *testing.T) {
	mgr := NewManager(nil)
	a := &mockAdapter{name: "test", stopC: make(chan struct{})}
	mgr.Register(a)

	ctx, cancel := context.WithCancel(context.Background())
	mgr.Start(ctx)

	// Stop via context
	cancel()
	mgr.Stop()
}

func TestManagerRunOne(t *testing.T) {
	mgr := NewManager(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a := &runOneAdapter{
		name:    "runtime",
		started: make(chan struct{}, 1),
		stopC:   make(chan struct{}),
	}
	mgr.Register(a)
	mgr.RunOne(ctx, a)

	select {
	case <-a.started:
	case <-time.After(time.Second):
		t.Fatal("expected RunOne to start adapter")
	}

	cancel()
	mgr.Stop()
}
