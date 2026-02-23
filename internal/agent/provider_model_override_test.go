package agent

import (
	"context"
	"testing"
)

type captureProvider struct {
	lastModel string
}

func (p *captureProvider) Name() string { return "capture" }

func (p *captureProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	p.lastModel = req.Model
	return &ChatResponse{Content: "ok"}, nil
}

type captureStreamProvider struct {
	captureProvider
}

func (p *captureStreamProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	p.lastModel = req.Model
	ch := make(chan StreamChunk, 1)
	ch <- StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

func TestWithModelOverridesChatModel(t *testing.T) {
	raw := &captureProvider{}
	wrapped := WithModel(raw, "forced-model")

	if _, err := wrapped.Chat(context.Background(), ChatRequest{Model: "other"}); err != nil {
		t.Fatalf("chat error: %v", err)
	}
	if raw.lastModel != "forced-model" {
		t.Fatalf("expected forced model, got %q", raw.lastModel)
	}
}

func TestWithModelPreservesStreaming(t *testing.T) {
	raw := &captureStreamProvider{}
	wrapped := WithModel(raw, "forced-model")

	streamer, ok := wrapped.(Streamer)
	if !ok {
		t.Fatal("expected wrapped provider to preserve Streamer")
	}
	ch, err := streamer.Stream(context.Background(), ChatRequest{Model: "other"})
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}
	for range ch {
	}
	if raw.lastModel != "forced-model" {
		t.Fatalf("expected forced model in stream path, got %q", raw.lastModel)
	}
}

func TestWithDefaultModelOnlySetsWhenMissing(t *testing.T) {
	raw := &captureProvider{}
	wrapped := WithDefaultModel(raw, "default-model")

	if _, err := wrapped.Chat(context.Background(), ChatRequest{Model: "user-model"}); err != nil {
		t.Fatalf("chat error: %v", err)
	}
	if raw.lastModel != "user-model" {
		t.Fatalf("expected caller model to be preserved, got %q", raw.lastModel)
	}

	if _, err := wrapped.Chat(context.Background(), ChatRequest{}); err != nil {
		t.Fatalf("chat error: %v", err)
	}
	if raw.lastModel != "default-model" {
		t.Fatalf("expected default model when missing, got %q", raw.lastModel)
	}
}

func TestWithModelRouterSetsModelForChat(t *testing.T) {
	raw := &captureProvider{}
	router := NewModelRouter(ModelRouterConfig{
		Strategy:       "cost_optimized",
		CheapModel:     "cheap-model",
		MidModel:       "mid-model",
		ExpensiveModel: "exp-model",
	}, nil)
	wrapped := WithModelRouter(raw, router)

	_, err := wrapped.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
	if raw.lastModel != "cheap-model" {
		t.Fatalf("expected router-selected model, got %q", raw.lastModel)
	}
}

func TestWithModelRouterSetsModelForStream(t *testing.T) {
	raw := &captureStreamProvider{}
	router := NewModelRouter(ModelRouterConfig{
		Strategy:       "cost_optimized",
		CheapModel:     "cheap-model",
		MidModel:       "mid-model",
		ExpensiveModel: "exp-model",
	}, nil)
	wrapped := WithModelRouter(raw, router)

	streamer, ok := wrapped.(Streamer)
	if !ok {
		t.Fatal("expected wrapped provider to preserve Streamer")
	}
	ch, err := streamer.Stream(context.Background(), ChatRequest{
		Messages: []Message{{
			Role:    "user",
			Content: "architect deep reasoning tradeoff benchmark root cause ```go\nfunc main() {}\n```",
		}},
	})
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}
	for range ch {
	}
	if raw.lastModel != "exp-model" {
		t.Fatalf("expected router-selected model in stream path, got %q", raw.lastModel)
	}
}
