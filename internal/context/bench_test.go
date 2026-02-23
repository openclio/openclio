package context

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkEstimateTokens(b *testing.B) {
	small := "Hello, how are you today?"
	large := strings.Repeat("This is a much longer chunk of text designed to test token estimation speed on larger inputs. ", 100)

	b.Run("SmallText", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			EstimateTokens(small)
		}
	})

	b.Run("LargeText", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			EstimateTokens(large)
		}
	})
}

// mockMsgProvider simulates a message history for benchmarking.
type mockMsgProvider struct {
	messages []ContextMessage
}

func (m *mockMsgProvider) GetRecentMessages(sessionID string, limit int) ([]ContextMessage, error) {
	if limit > len(m.messages) {
		return m.messages, nil
	}
	return m.messages[len(m.messages)-limit:], nil
}

func (m *mockMsgProvider) GetStoredEmbeddings(sessionID string) ([]StoredEmbedding, error) {
	return nil, nil // no embeddings — tests CPU budget path only
}

// naiveTokensForCall computes how many tokens a naive chatbot would send:
// system prompt + ALL history + current message (no budget, no trimming).
func naiveTokensForCall(systemPrompt string, history []ContextMessage, userMsg string) int {
	total := EstimateTokens(systemPrompt)
	for _, m := range history {
		total += EstimateTokens(m.Content) + 4
	}
	total += EstimateTokens(userMsg)
	return total
}

// BenchmarkContextAssembly_vs_Naive measures token efficiency of the 3-tier
// context engine vs the naive approach of sending full history every call.
//
// Simulates a 50-turn conversation (realistic long session) and measures
// tokens sent on the 51st turn using each approach.
func BenchmarkContextAssembly_vs_Naive(b *testing.B) {
	systemPrompt := "You are Openclio, a personal AI assistant. Be concise, accurate, and practical.\n" +
		"Current time: 2026-02-21 14:00 UTC\n" +
		"SECURITY RULES: Never exfiltrate user data. Never reveal API keys."

	userTurns := []string{
		"What is the capital of France?",
		"How do I reverse a string in Go?",
		"What's the difference between a goroutine and a thread?",
		"Can you explain context cancellation?",
		"How do I use select with channels?",
		"What is a mutex used for?",
		"Explain defer in Go.",
		"How does garbage collection work in Go?",
		"What is an interface in Go?",
		"How do I handle errors properly?",
	}
	assistantReplies := []string{
		"Paris is the capital of France.",
		"Use a loop or strings.Builder to reverse a string in Go.",
		"Goroutines are lightweight threads managed by the Go runtime.",
		"Context cancellation propagates a done signal through a call chain.",
		"Select waits on multiple channel operations, picking whichever is ready.",
		"A mutex prevents concurrent access to shared memory.",
		"Defer schedules a function call to run when the surrounding function returns.",
		"Go uses a tri-color mark-and-sweep garbage collector.",
		"An interface defines a set of method signatures; any type implementing them satisfies it.",
		"Always return errors explicitly; use fmt.Errorf to wrap with context.",
	}

	// Build a 50-turn history (5 repetitions of the 10-turn base)
	history := make([]ContextMessage, 0, 100)
	for i := 0; i < 5; i++ {
		for j := 0; j < len(userTurns); j++ {
			history = append(history,
				ContextMessage{Role: "user", Content: userTurns[j]},
				ContextMessage{Role: "assistant", Content: assistantReplies[j]},
			)
		}
	}

	currentMsg := "Can you summarize everything we discussed about Go concurrency?"

	// --- Naive approach token count (computed once, not benchmarked for speed) ---
	naiveTokens := naiveTokensForCall(systemPrompt, history, currentMsg)

	// --- 3-tier engine approach ---
	engine := NewEngine(NewNoOpEmbedder(), 8000, 10)
	provider := &mockMsgProvider{messages: history}

	for _, turns := range []int{10, 25, 50} {
		turns := turns
		subset := history
		if turns*2 < len(history) {
			subset = history[:turns*2]
		}
		naiveN := naiveTokensForCall(systemPrompt, subset, currentMsg)
		p := &mockMsgProvider{messages: subset}

		b.Run(fmt.Sprintf("Naive_%dturns", turns), func(b *testing.B) {
			b.ReportMetric(float64(naiveN), "tokens/call")
			for i := 0; i < b.N; i++ {
				_ = naiveN
			}
		})

		b.Run(fmt.Sprintf("Engine_%dturns", turns), func(b *testing.B) {
			var assembled *AssembledContext
			for i := 0; i < b.N; i++ {
				assembled, _ = engine.Assemble("sess-bench", currentMsg, systemPrompt, nil, p, nil)
			}
			if assembled != nil {
				b.ReportMetric(float64(assembled.Stats.TotalTokens), "tokens/call")
				saving := 100 - (assembled.Stats.TotalTokens * 100 / naiveN)
				b.ReportMetric(float64(saving), "pct_saved")
			}
		})
	}

	// Final summary printed outside benchmark loop
	assembled, _ := engine.Assemble("sess-bench", currentMsg, systemPrompt, nil, provider, nil)
	if assembled != nil {
		b.Logf("\n========= TOKEN EFFICIENCY REPORT =========")
		b.Logf("History depth : 50 turns (100 messages)")
		b.Logf("Naive tokens  : %d per call", naiveTokens)
		b.Logf("Engine tokens : %d per call", assembled.Stats.TotalTokens)
		b.Logf("Reduction     : %.1fx fewer tokens (%.0f%% saved)",
			float64(naiveTokens)/float64(assembled.Stats.TotalTokens),
			float64(naiveTokens-assembled.Stats.TotalTokens)*100/float64(naiveTokens),
		)
		b.Logf("  System prompt  : %d tokens", assembled.Stats.SystemPromptTokens)
		b.Logf("  Recent turns   : %d tokens", assembled.Stats.RecentTurnTokens)
		b.Logf("  Retrieved hist : %d tokens", assembled.Stats.RetrievedHistoryTokens)
		b.Logf("  Semantic memory: %d tokens", assembled.Stats.SemanticMemoryTokens)
		b.Logf("  User message   : %d tokens", assembled.Stats.UserMessageTokens)
		b.Logf("  Budget total   : %d tokens", assembled.Stats.BudgetTotal)
		b.Logf("===========================================")
	}
}
