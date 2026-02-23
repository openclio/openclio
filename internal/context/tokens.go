package context

import (
	"sync"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

var (
	encodingOnce sync.Once
	encoding     *tiktoken.Tiktoken
)

func getEncoding() *tiktoken.Tiktoken {
	encodingOnce.Do(func() {
		enc, err := tiktoken.GetEncoding("cl100k_base")
		if err == nil {
			encoding = enc
		}
	})
	return encoding
}

// EstimateTokens returns an approximate token count for a text string.
// Uses cl100k_base when available and falls back to a rough heuristic.
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}

	if enc := getEncoding(); enc != nil {
		n := len(enc.Encode(text, nil, nil))
		if n > 0 {
			return n
		}
	}

	// Fallback: ~4 characters per token.
	n := len(text) / 4
	if n == 0 {
		n = 1
	}
	return n
}

// EstimateTokensForMessages estimates total tokens for a list of message contents.
func EstimateTokensForMessages(contents []string) int {
	total := 0
	for _, c := range contents {
		total += EstimateTokens(c)
		// Each message has ~4 tokens of overhead (role, formatting)
		total += 4
	}
	return total
}
