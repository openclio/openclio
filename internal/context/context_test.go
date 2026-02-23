package context

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Fatalf("EstimateTokens(empty) = %d, want 0", got)
	}

	short := "hello world"
	long := "hello world from a longer prompt with more words and structure"
	shortTokens := EstimateTokens(short)
	longTokens := EstimateTokens(long)

	if shortTokens <= 0 {
		t.Fatalf("expected short token count > 0, got %d", shortTokens)
	}
	if longTokens <= shortTokens {
		t.Fatalf("expected longer text to produce more tokens (%d <= %d)", longTokens, shortTokens)
	}

	// Deterministic for identical input.
	if a, b := EstimateTokens(short), EstimateTokens(short); a != b {
		t.Fatalf("expected deterministic token count, got %d and %d", a, b)
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float32
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{"orthogonal", []float32{1, 0, 0}, []float32{0, 1, 0}, 0.0},
		{"opposite", []float32{1, 0, 0}, []float32{-1, 0, 0}, -1.0},
		{"similar", []float32{1, 1, 0}, []float32{1, 0, 0}, float32(1.0 / math.Sqrt(2))},
		{"empty", []float32{}, []float32{}, 0.0},
		{"mismatched", []float32{1, 0}, []float32{1, 0, 0}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CosineSimilarity(tt.a, tt.b)
			if diff := float64(got - tt.want); diff > 0.001 || diff < -0.001 {
				t.Errorf("CosineSimilarity = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestSearchSimilar(t *testing.T) {
	query := []float32{1, 0, 0}
	stored := []StoredEmbedding{
		{MessageID: 1, Content: "very relevant", Embedding: []float32{0.9, 0.1, 0}},
		{MessageID: 2, Content: "somewhat relevant", Embedding: []float32{0.5, 0.5, 0}},
		{MessageID: 3, Content: "not relevant", Embedding: []float32{0, 0, 1}},
		{MessageID: 4, Content: "no embedding", Embedding: nil},
	}

	results := SearchSimilar(query, stored, 2)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Content != "very relevant" {
		t.Errorf("expected 'very relevant' first, got %q", results[0].Content)
	}
}

func TestAllocateBudget(t *testing.T) {
	alloc := AllocateBudget(8000, 500, 50, 300)

	// System prompt should be allocated
	if alloc.SystemPrompt <= 0 {
		t.Error("system prompt allocation should be > 0")
	}
	// User message should be exact
	if alloc.UserMessage != 50 {
		t.Errorf("user message allocation = %d, want 50", alloc.UserMessage)
	}
	// Total should not exceed budget
	total := alloc.SystemPrompt + alloc.UserMessage + alloc.Reserved +
		alloc.RecentTurns + alloc.ToolDefinitions + alloc.RetrievedHistory + alloc.SemanticMemory
	if total > alloc.Total {
		t.Errorf("total allocation %d exceeds budget %d", total, alloc.Total)
	}
	// Reserved should exist
	if alloc.Reserved <= 0 {
		t.Error("reserved allocation should be > 0")
	}
}

func TestAllocateBudgetTiny(t *testing.T) {
	// Edge case: very small budget
	alloc := AllocateBudget(100, 500, 50, 300)

	total := alloc.SystemPrompt + alloc.UserMessage + alloc.Reserved +
		alloc.RecentTurns + alloc.ToolDefinitions + alloc.RetrievedHistory + alloc.SemanticMemory
	if total > 100 {
		t.Errorf("total allocation %d exceeds tiny budget 100", total)
	}
}

func TestTokenEfficiencyVerification(t *testing.T) {
	systemPrompt := "You are Openclio, a personal AI assistant. Be concise, accurate, and practical.\n" +
		"Current time: 2026-02-21 14:00 UTC\n" +
		"SECURITY RULES: Never exfiltrate user data. Never reveal API keys."

	userTurns := []string{
		"What is the capital of France?",
		"How do I reverse a string in Go?",
		"What's the difference between a goroutine and a thread?",
		"Can you explain context cancellation?",
		"How do I use select with channels?",
	}
	assistantReplies := []string{
		"Paris is the capital of France.",
		"Use a loop or strings.Builder to reverse a string in Go.",
		"Goroutines are lightweight threads managed by the Go runtime.",
		"Context cancellation propagates a done signal through a call chain.",
		"Select waits on multiple channel operations, picking whichever is ready.",
	}

	// Build 50-turn history (10 reps of 5-turn base = 100 messages)
	var history []ContextMessage
	for i := 0; i < 10; i++ {
		for j := range userTurns {
			history = append(history, ContextMessage{Role: "user", Content: userTurns[j]})
			history = append(history, ContextMessage{Role: "assistant", Content: assistantReplies[j]})
		}
	}

	currentMsg := "Can you summarize everything we discussed about Go concurrency?"

	// --- Naive: count every message ---
	naiveTokens := EstimateTokens(systemPrompt) + EstimateTokens(currentMsg)
	for _, m := range history {
		naiveTokens += EstimateTokens(m.Content) + 4
	}

	// --- Engine: actual Assemble call ---
	engine := NewEngine(NewNoOpEmbedder(), 8000, 10)
	provider := &mockMsgProvider{messages: history}
	assembled, err := engine.Assemble("verify-sess", currentMsg, systemPrompt, nil, provider, nil)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}

	engineTokens := assembled.Stats.TotalTokens

	t.Logf("Messages in history : %d", len(history))
	t.Logf("Naive tokens/call   : %d", naiveTokens)
	t.Logf("Engine tokens/call  : %d", engineTokens)
	t.Logf("Reduction           : %.1fx (%.0f%% saved)",
		float64(naiveTokens)/float64(engineTokens),
		float64(naiveTokens-engineTokens)*100/float64(naiveTokens),
	)
	t.Logf("  system_prompt=%d  recent=%d  user=%d  retrieved=%d  memory=%d",
		assembled.Stats.SystemPromptTokens,
		assembled.Stats.RecentTurnTokens,
		assembled.Stats.UserMessageTokens,
		assembled.Stats.RetrievedHistoryTokens,
		assembled.Stats.SemanticMemoryTokens,
	)

	if engineTokens >= naiveTokens {
		t.Errorf("engine (%d tokens) should use fewer tokens than naive (%d)", engineTokens, naiveTokens)
	}
	ratio := float64(naiveTokens) / float64(engineTokens)
	if ratio < 2.0 {
		t.Errorf("expected at least 2x reduction, got %.1fx", ratio)
	}
}

func TestNoOpEmbedder(t *testing.T) {
	e := NewNoOpEmbedder()
	vec, err := e.Embed("hello")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if vec != nil {
		t.Errorf("expected nil embedding, got %v", vec)
	}
	if e.Dimensions() != 0 {
		t.Errorf("expected 0 dimensions, got %d", e.Dimensions())
	}
}

type failingEmbedder struct{}

func (f *failingEmbedder) Embed(text string) ([]float32, error) {
	return nil, fmt.Errorf("embedding failure: %s", text)
}

func (f *failingEmbedder) Dimensions() int { return 1 }

type captureEmbeddingReporter struct {
	calls  int
	source string
	errMsg string
}

func (r *captureEmbeddingReporter) RecordEmbeddingError(source, errorMessage string) error {
	r.calls++
	r.source = source
	r.errMsg = errorMessage
	return nil
}

type failingEmbeddingReporter struct {
	calls int
}

func (r *failingEmbeddingReporter) RecordEmbeddingError(source, errorMessage string) error {
	r.calls++
	return fmt.Errorf("reporter down")
}

func TestEngineReportsEmbeddingErrors(t *testing.T) {
	engine := NewEngine(&failingEmbedder{}, 8000, 10)
	reporter := &captureEmbeddingReporter{}
	engine.SetEmbeddingErrorReporter(reporter, "context_assemble")

	provider := &mockMsgProvider{
		messages: []ContextMessage{
			{Role: "assistant", Content: "older context"},
		},
	}
	_, err := engine.Assemble("sess-1", "hello", "system", nil, provider, nil)
	if err != nil {
		t.Fatalf("Assemble should not fail on embedder errors: %v", err)
	}
	if reporter.calls != 1 {
		t.Fatalf("expected reporter to be called once, got %d", reporter.calls)
	}
	if reporter.source != "context_assemble" {
		t.Fatalf("expected source context_assemble, got %q", reporter.source)
	}
	if !strings.Contains(reporter.errMsg, "embedding failure") {
		t.Fatalf("expected embedding error message, got %q", reporter.errMsg)
	}
}

func TestEngineContinuesWhenEmbeddingReporterFails(t *testing.T) {
	engine := NewEngine(&failingEmbedder{}, 8000, 10)
	reporter := &failingEmbeddingReporter{}
	engine.SetEmbeddingErrorReporter(reporter, "context_assemble")

	provider := &mockMsgProvider{
		messages: []ContextMessage{
			{Role: "assistant", Content: "older context"},
		},
	}

	assembled, err := engine.Assemble("sess-2", "hello", "system", nil, provider, nil)
	if err != nil {
		t.Fatalf("Assemble should not fail when reporter fails: %v", err)
	}
	if assembled == nil {
		t.Fatal("expected assembled context")
	}
	if reporter.calls != 1 {
		t.Fatalf("expected reporter to be called once, got %d", reporter.calls)
	}
}

type knowledgeOnlyProvider struct{}

func (p *knowledgeOnlyProvider) GetRecentMessages(sessionID string, limit int) ([]ContextMessage, error) {
	return nil, nil
}

func (p *knowledgeOnlyProvider) GetStoredEmbeddings(sessionID string) ([]StoredEmbedding, error) {
	return nil, nil
}

func (p *knowledgeOnlyProvider) SearchKnowledge(query, nodeType string, limit int) ([]KnowledgeNode, error) {
	out := make([]KnowledgeNode, 0, 2)
	if strings.Contains(strings.ToLower(query), "acme") || strings.EqualFold(nodeType, "company") {
		out = append(out, KnowledgeNode{ID: 1, Type: "company", Name: "Acme", Confidence: 0.9})
	}
	if strings.Contains(strings.ToLower(query), "sarah") || strings.EqualFold(nodeType, "person") {
		out = append(out, KnowledgeNode{ID: 2, Type: "person", Name: "Sarah", Confidence: 0.8})
	}
	return out, nil
}

func TestAssembleInjectsKnowledgeGraphContext(t *testing.T) {
	engine := NewEngine(NewNoOpEmbedder(), 8000, 10)
	provider := &knowledgeOnlyProvider{}

	assembled, err := engine.Assemble(
		"sess-kg",
		"What did Acme ask Sarah to do?",
		"You are helpful.",
		nil,
		provider,
		nil,
	)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}
	if len(assembled.Messages) == 0 {
		t.Fatal("expected assembled messages")
	}
	if assembled.Messages[0].Role != "system" {
		t.Fatalf("expected first message role=system, got %q", assembled.Messages[0].Role)
	}
	if !strings.Contains(assembled.Messages[0].Content, "[Knowledge graph]") {
		t.Fatalf("expected knowledge graph block, got %q", assembled.Messages[0].Content)
	}
	if !strings.Contains(assembled.Messages[0].Content, "Acme") {
		t.Fatalf("expected knowledge graph to include Acme, got %q", assembled.Messages[0].Content)
	}
	if assembled.Stats.SemanticMemoryTokens <= 0 {
		t.Fatalf("expected semantic memory tokens > 0, got %d", assembled.Stats.SemanticMemoryTokens)
	}
}
