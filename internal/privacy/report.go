package privacy

import (
	"sort"
	"strings"

	"github.com/openclio/openclio/internal/cost"
	"github.com/openclio/openclio/internal/storage"
)

// ProviderUsage is one provider row in privacy reporting.
type ProviderUsage struct {
	Provider      string  `json:"provider"`
	Calls         int     `json:"calls"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	EstimatedCost float64 `json:"estimated_cost"`
	Privacy       string  `json:"privacy"` // local | cloud
}

// Report is the shared payload used by CLI and API privacy reporting.
type Report struct {
	Period  string `json:"period"`
	Privacy struct {
		ScrubOutput     bool  `json:"scrub_output"`
		SecretsRedacted int64 `json:"secrets_redacted"`
	} `json:"privacy"`
	Totals struct {
		Calls         int     `json:"calls"`
		InputTokens   int     `json:"input_tokens"`
		OutputTokens  int     `json:"output_tokens"`
		EstimatedCost float64 `json:"estimated_cost"`
	} `json:"totals"`
	Providers []ProviderUsage `json:"providers"`
}

// BuildReport assembles a privacy report from cost and redaction stores.
func BuildReport(
	tracker *cost.Tracker,
	privacyStore *storage.PrivacyStore,
	scrubOutput bool,
	period string,
) (*Report, error) {
	if strings.TrimSpace(period) == "" {
		period = "all"
	}

	report := &Report{Period: period}
	report.Privacy.ScrubOutput = scrubOutput

	if tracker != nil {
		if summary, err := tracker.GetSummary(period); err != nil {
			return nil, err
		} else if summary != nil {
			report.Totals.Calls = summary.CallCount
			report.Totals.InputTokens = summary.InputTokens
			report.Totals.OutputTokens = summary.OutputTokens
			report.Totals.EstimatedCost = summary.TotalCost
		}

		byProvider, err := tracker.ProviderBreakdown(period)
		if err != nil {
			return nil, err
		}
		providers := make([]ProviderUsage, 0, len(byProvider))
		for provider, s := range byProvider {
			row := ProviderUsage{
				Provider:      provider,
				Calls:         s.CallCount,
				InputTokens:   s.InputTokens,
				OutputTokens:  s.OutputTokens,
				EstimatedCost: s.TotalCost,
				Privacy:       "cloud",
			}
			if strings.EqualFold(provider, "ollama") {
				row.Privacy = "local"
			}
			providers = append(providers, row)
		}
		sort.Slice(providers, func(i, j int) bool {
			return providers[i].Provider < providers[j].Provider
		})
		report.Providers = providers
	}

	if privacyStore != nil {
		total, err := privacyStore.TotalByCategory("secret")
		if err != nil {
			return nil, err
		}
		report.Privacy.SecretsRedacted = total
	}

	return report, nil
}
