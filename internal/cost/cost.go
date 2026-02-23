// Package cost tracks token usage and estimates API costs per provider.
package cost

import (
	"database/sql"
	"fmt"

	"github.com/openclio/openclio/internal/storage"
)

// ProviderPricing holds per-token pricing for a provider/model.
// Prices are in USD per 1 million tokens (standard industry format).
type ProviderPricing struct {
	InputPer1M  float64
	OutputPer1M float64
}

// KnownPricing contains approximate pricing for common models.
// Users can override via config.
var KnownPricing = map[string]ProviderPricing{
	"claude-sonnet-4-20250514":   {InputPer1M: 3.0, OutputPer1M: 15.0},
	"claude-3-5-sonnet-20241022": {InputPer1M: 3.0, OutputPer1M: 15.0},
	"claude-3-haiku-20240307":    {InputPer1M: 0.25, OutputPer1M: 1.25},
	"gpt-4o":                     {InputPer1M: 2.5, OutputPer1M: 10.0},
	"gpt-4o-mini":                {InputPer1M: 0.15, OutputPer1M: 0.60},
	"gpt-4-turbo":                {InputPer1M: 10.0, OutputPer1M: 30.0},
	// Ollama is always free
}

// EstimateCost computes the estimated USD cost for a given model and token counts.
func EstimateCost(model string, inputTokens, outputTokens int) float64 {
	pricing, ok := KnownPricing[model]
	if !ok {
		return 0 // unknown model — can't estimate
	}
	inputCost := float64(inputTokens) / 1_000_000 * pricing.InputPer1M
	outputCost := float64(outputTokens) / 1_000_000 * pricing.OutputPer1M
	return inputCost + outputCost
}

// Tracker records token usage to the database.
type Tracker struct {
	db *storage.DB
}

// NewTracker creates a new cost tracker.
func NewTracker(db *storage.DB) *Tracker {
	return &Tracker{db: db}
}

// Record saves a usage record to the database.
func (t *Tracker) Record(sessionID, provider, model string, inputTokens, outputTokens int) error {
	cost := EstimateCost(model, inputTokens, outputTokens)
	_, err := t.db.Conn().Exec(
		`INSERT INTO token_usage (session_id, provider, model, input_tokens, output_tokens, estimated_cost)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, provider, model, inputTokens, outputTokens, cost,
	)
	return err
}

// Summary holds aggregated usage for a time period.
type Summary struct {
	Period       string
	InputTokens  int
	OutputTokens int
	TotalCost    float64
	CallCount    int
}

// GetSummary returns cost/usage aggregates for a given period.
// period: "today", "week", "month", "all"
func (t *Tracker) GetSummary(period string) (*Summary, error) {
	var args []any
	switch period {
	case "today":
		// date('now') is implicit in the query
	case "week":
		args = []any{"-7 days"}
	case "month":
		args = []any{"-30 days"}
	default: // "all"
	}

	var query string
	if len(args) > 0 {
		query = `
			SELECT
				COALESCE(SUM(input_tokens), 0),
				COALESCE(SUM(output_tokens), 0),
				COALESCE(SUM(estimated_cost), 0.0),
				COUNT(*)
			FROM token_usage
			WHERE date(created_at) >= date('now', ?)`
	} else {
		query = `
			SELECT
				COALESCE(SUM(input_tokens), 0),
				COALESCE(SUM(output_tokens), 0),
				COALESCE(SUM(estimated_cost), 0.0),
				COUNT(*)
			FROM token_usage
			WHERE date(created_at) >= '2000-01-01'`
	}
	row := t.db.Conn().QueryRow(query, args...)

	s := &Summary{Period: period}
	if err := row.Scan(&s.InputTokens, &s.OutputTokens, &s.TotalCost, &s.CallCount); err != nil {
		if err == sql.ErrNoRows {
			return s, nil
		}
		return nil, err
	}
	return s, nil
}

// GetSummaryBySession returns aggregated token usage specifically for a given session.
func (t *Tracker) GetSummaryBySession(sessionID string) (*Summary, error) {
	query := `
		SELECT
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(estimated_cost), 0.0),
			COUNT(*)
		FROM token_usage
		WHERE session_id = ?`

	row := t.db.Conn().QueryRow(query, sessionID)

	s := &Summary{Period: "session"}
	if err := row.Scan(&s.InputTokens, &s.OutputTokens, &s.TotalCost, &s.CallCount); err != nil {
		if err == sql.ErrNoRows {
			return s, nil
		}
		return nil, err
	}
	return s, nil
}

// ProviderBreakdown returns usage per provider for a given period.
func (t *Tracker) ProviderBreakdown(period string) (map[string]*Summary, error) {
	var args []any
	switch period {
	case "today":
		// date('now') is implicit in the query
	case "week":
		args = []any{"-7 days"}
	case "month":
		args = []any{"-30 days"}
	default:
	}

	var query string
	if len(args) > 0 {
		query = `
			SELECT provider,
				COALESCE(SUM(input_tokens), 0),
				COALESCE(SUM(output_tokens), 0),
				COALESCE(SUM(estimated_cost), 0.0),
				COUNT(*)
			FROM token_usage
			WHERE date(created_at) >= date('now', ?)
			GROUP BY provider`
	} else {
		query = `
			SELECT provider,
				COALESCE(SUM(input_tokens), 0),
				COALESCE(SUM(output_tokens), 0),
				COALESCE(SUM(estimated_cost), 0.0),
				COUNT(*)
			FROM token_usage
			WHERE date(created_at) >= '2000-01-01'
			GROUP BY provider`
	}
	rows, err := t.db.Conn().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]*Summary)
	for rows.Next() {
		var provider string
		s := &Summary{Period: period}
		if err := rows.Scan(&provider, &s.InputTokens, &s.OutputTokens, &s.TotalCost, &s.CallCount); err != nil {
			continue
		}
		result[provider] = s
	}
	return result, nil
}

// FormatSummary returns a human-readable cost report.
func FormatSummary(summaries map[string]*Summary, byProvider map[string]*Summary, currentSession *Summary) string {
	var sb = new(stringBuilder)

	sb.line("── Token Usage & Cost ──────────────────")
	for _, period := range []string{"today", "week", "month", "all"} {
		s, ok := summaries[period]
		if !ok {
			continue
		}
		label := map[string]string{
			"today": "Today",
			"week":  "This week",
			"month": "This month",
			"all":   "All time",
		}[period]
		sb.linef("  %-12s  %6d in / %6d out  ~$%.4f  (%d calls)",
			label, s.InputTokens, s.OutputTokens, s.TotalCost, s.CallCount)
	}

	if currentSession != nil {
		sb.line("")
		sb.line("── Current Session ─────────────────────")
		sb.linef("  %-12s  %6d in / %6d out  ~$%.4f  (%d calls)",
			"This session", currentSession.InputTokens, currentSession.OutputTokens, currentSession.TotalCost, currentSession.CallCount)
	}

	if len(byProvider) > 0 {
		sb.line("")
		sb.line("── By Provider (all time) ──────────────")
		for provider, s := range byProvider {
			sb.linef("  %-14s  %6d in / %6d out  ~$%.4f",
				provider, s.InputTokens, s.OutputTokens, s.TotalCost)
		}
	}

	return sb.String()
}

// --- helpers ---

type stringBuilder struct {
	s string
}

func (b *stringBuilder) line(s string)            { b.s += s + "\n" }
func (b *stringBuilder) linef(f string, a ...any) { b.s += fmt.Sprintf(f+"\n", a...) }
func (b *stringBuilder) String() string           { return b.s }
