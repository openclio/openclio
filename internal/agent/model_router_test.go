package agent

import "testing"

func TestModelRouterSelect_CheapByDefault(t *testing.T) {
	router := NewModelRouter(ModelRouterConfig{
		Strategy:       "cost_optimized",
		CheapModel:     "cheap-model",
		MidModel:       "mid-model",
		ExpensiveModel: "exp-model",
	}, nil)

	route := router.Select(ChatRequest{
		Messages: []Message{{Role: "user", Content: "hello there"}},
		Model:    "fallback-model",
	})

	if route.Tier != TierCheap {
		t.Fatalf("expected cheap tier, got %q", route.Tier)
	}
	if route.Model != "cheap-model" {
		t.Fatalf("expected cheap model, got %q", route.Model)
	}
}

func TestModelRouterSelect_ExpensiveForDeepCodeSignals(t *testing.T) {
	router := NewModelRouter(ModelRouterConfig{
		Strategy:       "cost_optimized",
		CheapModel:     "cheap-model",
		MidModel:       "mid-model",
		ExpensiveModel: "exp-model",
	}, nil)

	msgs := make([]Message, 0, 20)
	for i := 0; i < 19; i++ {
		msgs = append(msgs, Message{Role: "assistant", Content: "context"})
	}
	msgs = append(msgs, Message{
		Role: "user",
		Content: "Please architect a multi-step migration and threat model.\n" +
			"```go\nfunc main() {}\n```\n" +
			"See internal/agent/model_router.go and /tmp/work/file.txt",
	})

	route := router.Select(ChatRequest{Messages: msgs})
	if route.Tier != TierExpensive {
		t.Fatalf("expected expensive tier, got %q (reasons=%v)", route.Tier, route.Reasons)
	}
	if route.Model != "exp-model" {
		t.Fatalf("expected expensive model, got %q", route.Model)
	}
}

func TestModelRouterSelect_QualityFirstUpgradesCheapToMid(t *testing.T) {
	router := NewModelRouter(ModelRouterConfig{
		Strategy:       "quality_first",
		CheapModel:     "cheap-model",
		MidModel:       "mid-model",
		ExpensiveModel: "exp-model",
	}, nil)

	route := router.Select(ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})

	if route.Tier != TierMid {
		t.Fatalf("expected mid tier from quality_first, got %q", route.Tier)
	}
	if route.Model != "mid-model" {
		t.Fatalf("expected mid model, got %q", route.Model)
	}
}

func TestModelRouterSelect_SpeedFirstDowngradesExpensiveToMid(t *testing.T) {
	router := NewModelRouter(ModelRouterConfig{
		Strategy:       "speed_first",
		CheapModel:     "cheap-model",
		MidModel:       "mid-model",
		ExpensiveModel: "exp-model",
	}, nil)

	route := router.Select(ChatRequest{
		Messages: []Message{{
			Role: "user",
			Content: "architect deep reasoning tradeoff benchmark root cause " +
				"```python\nprint('x')\n```",
		}},
	})

	if route.Tier != TierMid {
		t.Fatalf("expected mid tier from speed_first, got %q", route.Tier)
	}
	if route.Model != "mid-model" {
		t.Fatalf("expected mid model, got %q", route.Model)
	}
}

func TestModelRouterSelect_PrivacyFirstUsesPrivacyModel(t *testing.T) {
	router := NewModelRouter(ModelRouterConfig{
		Strategy:     "privacy_first",
		PrivacyModel: "local-private-model",
		CheapModel:   "cheap-model",
	}, nil)

	route := router.Select(ChatRequest{
		Messages: []Message{{Role: "user", Content: "anything"}},
		Model:    "fallback-model",
	})

	if route.Model != "local-private-model" {
		t.Fatalf("expected privacy model, got %q", route.Model)
	}
}

func TestModelRouterSelect_FallsBackToRequestModelWhenTierUnset(t *testing.T) {
	router := NewModelRouter(ModelRouterConfig{
		Strategy: "cost_optimized",
	}, nil)

	route := router.Select(ChatRequest{
		Messages: []Message{{Role: "user", Content: "simple"}},
		Model:    "request-model",
	})

	if route.Model != "request-model" {
		t.Fatalf("expected request model fallback, got %q", route.Model)
	}
}
