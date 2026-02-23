package agent

import (
	"log/slog"
	"regexp"
	"strings"
)

// RoutingTier is the selected model-cost tier for one request.
type RoutingTier string

const (
	TierCheap     RoutingTier = "cheap"
	TierMid       RoutingTier = "mid"
	TierExpensive RoutingTier = "expensive"
)

// ModelRouterConfig configures heuristic model routing.
type ModelRouterConfig struct {
	Strategy       string
	CheapModel     string
	MidModel       string
	ExpensiveModel string
	PrivacyModel   string
}

// ModelRoute is the selected model and reasoning metadata.
type ModelRoute struct {
	Tier    RoutingTier
	Model   string
	Reasons []string
}

// ModelRouter routes requests to model tiers using deterministic heuristics.
type ModelRouter struct {
	cfg    ModelRouterConfig
	logger *slog.Logger
}

var (
	pathLikeRegex = regexp.MustCompile(`([A-Za-z]:\\|/)?[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)+`)
	deepKeywords  = []string{
		"architect", "tradeoff", "threat model", "deep reasoning", "benchmark",
		"root cause", "optimiz", "multi-step", "research plan",
	}
	codeKeywords = []string{
		"refactor", "function", "class", "compile", "stack trace", "unit test",
		"sql", "migration", ".go", ".ts", ".py", "regex",
	}
)

// NewModelRouter creates a new heuristic model router.
func NewModelRouter(cfg ModelRouterConfig, logger *slog.Logger) *ModelRouter {
	if strings.TrimSpace(cfg.Strategy) == "" {
		cfg.Strategy = "cost_optimized"
	}
	return &ModelRouter{cfg: cfg, logger: logger}
}

// Select chooses a model for one request.
func (r *ModelRouter) Select(req ChatRequest) ModelRoute {
	strategy := strings.ToLower(strings.TrimSpace(r.cfg.Strategy))
	if strategy == "privacy_first" && strings.TrimSpace(r.cfg.PrivacyModel) != "" {
		return ModelRoute{
			Tier:    TierCheap,
			Model:   r.cfg.PrivacyModel,
			Reasons: []string{"strategy:privacy_first"},
		}
	}

	text, depth := requestSignalText(req)
	lower := strings.ToLower(text)

	score := 0
	reasons := make([]string, 0, 6)
	if depth >= 16 {
		score += 2
		reasons = append(reasons, "conversation_depth")
	}
	if len(text) > 1200 {
		score++
		reasons = append(reasons, "message_length_medium")
	}
	if len(text) > 4000 {
		score += 2
		reasons = append(reasons, "message_length_large")
	}
	if strings.Contains(text, "```") {
		score += 2
		reasons = append(reasons, "code_block")
	}
	if pathLikeRegex.MatchString(text) {
		score++
		reasons = append(reasons, "file_path")
	}
	if containsAny(lower, deepKeywords) {
		score += 3
		reasons = append(reasons, "deep_reasoning_keywords")
	}
	if containsAny(lower, codeKeywords) {
		score++
		reasons = append(reasons, "code_keywords")
	}

	tier := TierCheap
	switch {
	case score >= 5:
		tier = TierExpensive
	case score >= 2:
		tier = TierMid
	}

	switch strategy {
	case "quality_first":
		if tier == TierCheap {
			tier = TierMid
			reasons = append(reasons, "strategy_quality_first")
		} else if tier == TierMid {
			tier = TierExpensive
			reasons = append(reasons, "strategy_quality_first")
		}
	case "speed_first":
		if tier == TierExpensive {
			tier = TierMid
			reasons = append(reasons, "strategy_speed_first")
		}
	case "privacy_first":
		reasons = append(reasons, "strategy_privacy_first_no_privacy_model")
	}

	model := r.modelForTier(tier, req.Model)
	return ModelRoute{
		Tier:    tier,
		Model:   model,
		Reasons: reasons,
	}
}

func (r *ModelRouter) modelForTier(tier RoutingTier, fallback string) string {
	switch tier {
	case TierCheap:
		if strings.TrimSpace(r.cfg.CheapModel) != "" {
			return r.cfg.CheapModel
		}
		if strings.TrimSpace(r.cfg.MidModel) != "" {
			return r.cfg.MidModel
		}
		if strings.TrimSpace(r.cfg.ExpensiveModel) != "" {
			return r.cfg.ExpensiveModel
		}
	case TierMid:
		if strings.TrimSpace(r.cfg.MidModel) != "" {
			return r.cfg.MidModel
		}
		if strings.TrimSpace(r.cfg.ExpensiveModel) != "" {
			return r.cfg.ExpensiveModel
		}
		if strings.TrimSpace(r.cfg.CheapModel) != "" {
			return r.cfg.CheapModel
		}
	case TierExpensive:
		if strings.TrimSpace(r.cfg.ExpensiveModel) != "" {
			return r.cfg.ExpensiveModel
		}
		if strings.TrimSpace(r.cfg.MidModel) != "" {
			return r.cfg.MidModel
		}
		if strings.TrimSpace(r.cfg.CheapModel) != "" {
			return r.cfg.CheapModel
		}
	}
	return fallback
}

func requestSignalText(req ChatRequest) (string, int) {
	depth := len(req.Messages)
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			return req.Messages[i].Content, depth
		}
	}
	if len(req.Messages) > 0 {
		return req.Messages[len(req.Messages)-1].Content, depth
	}
	return "", depth
}

func containsAny(input string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(input, kw) {
			return true
		}
	}
	return false
}
