package cost

import (
	"testing"
)

func TestEstimateCost(t *testing.T) {
	// Known model
	c := EstimateCost("gpt-4o", 1000000, 1000000)
	if c <= 0 {
		t.Error("expected positive cost for gpt-4o")
	}

	// Unknown model — 0 cost
	c2 := EstimateCost("unknown-future-model", 100000, 50000)
	if c2 != 0 {
		t.Errorf("expected 0 for unknown model, got %f", c2)
	}

	// Zero tokens
	c3 := EstimateCost("claude-sonnet-4-20250514", 0, 0)
	if c3 != 0 {
		t.Errorf("expected 0 cost for 0 tokens, got %f", c3)
	}

	// Cheap vs expensive model — haiku should cost less than sonnet
	cheapCost := EstimateCost("claude-3-haiku-20240307", 100000, 50000)
	expensiveCost := EstimateCost("claude-sonnet-4-20250514", 100000, 50000)
	if cheapCost >= expensiveCost {
		t.Errorf("haiku should cost less than sonnet: %f vs %f", cheapCost, expensiveCost)
	}
}

func TestFormatSummary(t *testing.T) {
	summaries := map[string]*Summary{
		"today": {Period: "today", InputTokens: 5000, OutputTokens: 2000, TotalCost: 0.045, CallCount: 10},
		"week":  {Period: "week", InputTokens: 30000, OutputTokens: 12000, TotalCost: 0.27, CallCount: 60},
	}
	byProvider := map[string]*Summary{
		"anthropic": {InputTokens: 20000, OutputTokens: 8000, TotalCost: 0.18},
	}

	result := FormatSummary(summaries, byProvider, nil)
	if result == "" {
		t.Error("FormatSummary should return non-empty string")
	}
}
