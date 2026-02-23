package context

import (
	"math"
	"sort"
)

// ScoredMessage pairs a message index with its relevance score.
type ScoredMessage struct {
	Index   int
	Score   float32
	Content string
	Role    string
	Tokens  int
}

// CosineSimilarity computes cosine similarity between two vectors.
// Returns a value between -1 and 1, where 1 means identical.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	denominator := math.Sqrt(normA) * math.Sqrt(normB)
	if denominator == 0 {
		return 0
	}

	return float32(dotProduct / denominator)
}

// StoredEmbedding represents a message with its precomputed embedding.
type StoredEmbedding struct {
	MessageID int64
	SessionID string
	Role      string
	Content   string
	Summary   string
	Tokens    int
	Embedding []float32
}

// SearchSimilar finds the top-K most similar messages to the query embedding.
// Uses brute-force cosine similarity — fast enough for <100K messages (<10ms).
func SearchSimilar(query []float32, stored []StoredEmbedding, topK int) []ScoredMessage {
	if len(query) == 0 || len(stored) == 0 {
		return nil
	}

	scored := make([]ScoredMessage, 0, len(stored))
	for i, s := range stored {
		if len(s.Embedding) == 0 {
			continue
		}
		score := CosineSimilarity(query, s.Embedding)
		content := s.Content
		if s.Summary != "" {
			content = s.Summary // prefer summary to save tokens
		}
		scored = append(scored, ScoredMessage{
			Index:   i,
			Score:   score,
			Content: content,
			Role:    s.Role,
			Tokens:  EstimateTokens(content),
		})
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	// Return top K
	if topK > len(scored) {
		topK = len(scored)
	}
	return scored[:topK]
}
