// Package context implements the context engine:
//   - Tier 1: Working memory (in-RAM, last few turns)
//   - Tier 2: Episodic memory (SQLite + vector search)
//   - Tier 3: Semantic memory (persistent facts and preferences)
//   - Tier 4: Knowledge graph memory (entities and relations)
//
// It assembles optimal context for each LLM call within a token budget.
package context

import (
	"fmt"
	"strings"
	"unicode"
)

// ToolDef describes a tool available to the agent.
type ToolDef struct {
	Name        string
	Description string
	Schema      string // JSON Schema for parameters
}

// ContextMessage is a message included in the assembled context.
type ContextMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AssembledContext is the final context ready to send to the LLM.
type AssembledContext struct {
	SystemPrompt string           `json:"system_prompt"`
	Messages     []ContextMessage `json:"messages"`
	ToolDefs     []ToolDef        `json:"tool_defs,omitempty"`

	// Token accounting
	Stats ContextStats `json:"stats"`
}

// ContextStats tracks token usage per tier for debugging and monitoring.
type ContextStats struct {
	SystemPromptTokens     int `json:"system_prompt_tokens"`
	UserMessageTokens      int `json:"user_message_tokens"`
	RecentTurnTokens       int `json:"recent_turn_tokens"`
	RetrievedHistoryTokens int `json:"retrieved_history_tokens"`
	RetrievedMessagesCount int `json:"retrieved_messages_count"`
	SemanticMemoryTokens   int `json:"semantic_memory_tokens"`
	ToolDefTokens          int `json:"tool_def_tokens"`
	TotalTokens            int `json:"total_tokens"`
	BudgetTotal            int `json:"budget_total"`
	BudgetRemaining        int `json:"budget_remaining"`
}

// MessageProvider retrieves messages for context assembly.
type MessageProvider interface {
	// GetRecent returns the last N messages for a session.
	GetRecentMessages(sessionID string, limit int) ([]ContextMessage, error)

	// GetEmbeddings returns stored embeddings for semantic search.
	GetStoredEmbeddings(sessionID string) ([]StoredEmbedding, error)
}

// MemoryProvider retrieves semantic memories (facts, preferences).
type MemoryProvider interface {
	// GetMemories returns relevant facts and preferences.
	GetMemories(limit int) ([]string, error)
}

// KnowledgeNode is one searchable node from the knowledge graph.
type KnowledgeNode struct {
	ID         int64
	Type       string
	Name       string
	Confidence float64
}

// KnowledgeProvider is an optional extension on MessageProvider for KG-backed context.
type KnowledgeProvider interface {
	SearchKnowledge(query, nodeType string, limit int) ([]KnowledgeNode, error)
}

// EmbeddingErrorReporter records embedding failures for monitoring.
type EmbeddingErrorReporter interface {
	RecordEmbeddingError(source, errorMessage string) error
}

// Engine is the context engine that assembles optimal context for each LLM call.
type Engine struct {
	embedder             Embedder
	maxTokens            int
	retrievalK           int
	embeddingErrReporter EmbeddingErrorReporter
	embeddingErrSource   string
}

// NewEngine creates a new context engine.
func NewEngine(embedder Embedder, maxTokensPerCall int, retrievalK int) *Engine {
	return &Engine{
		embedder:           embedder,
		maxTokens:          maxTokensPerCall,
		retrievalK:         retrievalK,
		embeddingErrSource: "context_assemble",
	}
}

// SetEmbeddingErrorReporter attaches optional embedding error reporting.
func (e *Engine) SetEmbeddingErrorReporter(reporter EmbeddingErrorReporter, source string) {
	e.embeddingErrReporter = reporter
	if source != "" {
		e.embeddingErrSource = source
	}
}

// Assemble builds the optimal context for an LLM call.
//
// Flow:
//  1. Estimate tokens for fixed components (system prompt, user message, tools)
//  2. Allocate budget across tiers
//  3. Tier 1: Load recent turns (working memory)
//  4. Tier 2: Embed user message → search for relevant past messages (episodic memory)
//  5. Tier 3: Load semantic memory facts
//  6. Tier 4: Load relevant knowledge graph entities
//  7. Trim each component to fit budget
//  8. Return assembled context with token stats
func (e *Engine) Assemble(
	sessionID string,
	userMessage string,
	systemPrompt string,
	toolDefs []ToolDef,
	msgProvider MessageProvider,
	memProvider MemoryProvider,
) (*AssembledContext, error) {

	// Estimate fixed tokens
	systemTokens := EstimateTokens(systemPrompt)
	userTokens := EstimateTokens(userMessage)
	toolTokens := 0
	for _, t := range toolDefs {
		toolTokens += EstimateTokens(t.Name + t.Description + t.Schema)
	}

	// Allocate budget
	alloc := AllocateBudget(e.maxTokens, systemTokens, userTokens, toolTokens)

	var messages []ContextMessage
	stats := ContextStats{
		SystemPromptTokens: EstimateTokens(systemPrompt),
		UserMessageTokens:  userTokens,
		BudgetTotal:        e.maxTokens,
	}

	// --- Tier 1: Working Memory (recent turns) ---
	if msgProvider != nil {
		recent, err := msgProvider.GetRecentMessages(sessionID, 10) // get last 10, trim to budget
		if err != nil {
			return nil, fmt.Errorf("loading recent messages: %w", err)
		}

		recentTokensUsed := 0
		for _, msg := range recent {
			msgTokens := EstimateTokens(msg.Content) + 4 // +4 for role overhead
			if recentTokensUsed+msgTokens > alloc.RecentTurns {
				break
			}
			messages = append(messages, msg)
			recentTokensUsed += msgTokens
		}
		stats.RecentTurnTokens = recentTokensUsed
	}

	// --- Tier 2: Episodic Memory (semantic search) ---
	if msgProvider != nil && e.embedder.Dimensions() > 0 {
		// Embed the user's current message
		queryEmbedding, err := e.embedder.Embed(userMessage)
		if err != nil {
			if e.embeddingErrReporter != nil {
				if reportErr := e.embeddingErrReporter.RecordEmbeddingError(e.embeddingErrSource, err.Error()); reportErr != nil {
					fmt.Printf("warning: embedding error reporter failed: %v\n", reportErr)
				}
			}
			// Non-fatal — skip semantic search, fall back to recent turns only
			fmt.Printf("warning: embedding failed, skipping semantic search: %v\n", err)
		} else if queryEmbedding != nil {
			// Search for relevant past messages
			stored, err := msgProvider.GetStoredEmbeddings(sessionID)
			if err != nil {
				return nil, fmt.Errorf("loading embeddings: %w", err)
			}

			similar := SearchSimilar(queryEmbedding, stored, e.retrievalK)

			// Add relevant messages within budget
			retrievedTokensUsed := 0
			retrievedCount := 0
			for _, scored := range similar {
				if scored.Score < 0.3 { // relevance threshold
					continue
				}
				msgTokens := scored.Tokens + 4
				if retrievedTokensUsed+msgTokens > alloc.RetrievedHistory {
					break
				}
				// Avoid duplicating messages already in recent turns
				if !containsContent(messages, scored.Content) {
					messages = append(messages, ContextMessage{
						Role:    scored.Role,
						Content: scored.Content,
					})
					retrievedTokensUsed += msgTokens
					retrievedCount++
				}
			}
			stats.RetrievedHistoryTokens = retrievedTokensUsed
			stats.RetrievedMessagesCount = retrievedCount
		}
	}

	// --- Tier 3: Semantic Memory (facts, preferences) ---
	if memProvider != nil {
		memories, err := memProvider.GetMemories(20)
		if err != nil {
			// Non-fatal
			fmt.Printf("warning: loading memories failed: %v\n", err)
		} else if len(memories) > 0 {
			memoryTokensUsed := 0
			var memoryContent string
			for _, mem := range memories {
				memTokens := EstimateTokens(mem)
				if memoryTokensUsed+memTokens > alloc.SemanticMemory {
					break
				}
				memoryContent += mem + "\n"
				memoryTokensUsed += memTokens
			}
			if memoryContent != "" {
				// Inject as a system-level context block
				messages = append([]ContextMessage{{
					Role:    "system",
					Content: "[User context]\n" + memoryContent,
				}}, messages...)
				stats.SemanticMemoryTokens = memoryTokensUsed
			}
		}
	}

	// --- Tier 4: Knowledge Graph (entities/relations) ---
	if kp, ok := msgProvider.(KnowledgeProvider); ok {
		remainingSemantic := alloc.SemanticMemory - stats.SemanticMemoryTokens
		if remainingSemantic > 0 {
			terms := knowledgeQueryTerms(userMessage)
			seen := make(map[string]struct{})
			lines := make([]string, 0, 12)
			knowledgeTokensUsed := EstimateTokens("[Knowledge graph]\n")

		collectKnowledge:
			for _, term := range terms {
				candidates, err := kp.SearchKnowledge(term, "", 8)
				if err != nil {
					fmt.Printf("warning: searching knowledge by name failed: %v\n", err)
					continue
				}
				typeMatches, err := kp.SearchKnowledge("", term, 8)
				if err != nil {
					fmt.Printf("warning: searching knowledge by type failed: %v\n", err)
				} else {
					candidates = append(candidates, typeMatches...)
				}

				for _, node := range candidates {
					name := strings.TrimSpace(node.Name)
					if name == "" {
						continue
					}
					kind := strings.TrimSpace(node.Type)
					key := strings.ToLower(kind + "|" + name)
					if _, exists := seen[key]; exists {
						continue
					}
					seen[key] = struct{}{}

					line := name
					if kind != "" {
						line = kind + ": " + name
					}
					if node.Confidence > 0 {
						line = fmt.Sprintf("%s (%.2f)", line, node.Confidence)
					}

					lineTokens := EstimateTokens(line)
					if knowledgeTokensUsed+lineTokens > remainingSemantic {
						break collectKnowledge
					}
					lines = append(lines, "- "+line)
					knowledgeTokensUsed += lineTokens
				}
			}

			if len(lines) > 0 {
				messages = append([]ContextMessage{{
					Role:    "system",
					Content: "[Knowledge graph]\n" + strings.Join(lines, "\n"),
				}}, messages...)
				stats.SemanticMemoryTokens += knowledgeTokensUsed
			}
		}
	}

	// Add the current user message at the end
	messages = append(messages, ContextMessage{
		Role:    "user",
		Content: userMessage,
	})

	// Calculate totals
	stats.ToolDefTokens = toolTokens
	stats.TotalTokens = stats.SystemPromptTokens + stats.UserMessageTokens +
		stats.RecentTurnTokens + stats.RetrievedHistoryTokens +
		stats.SemanticMemoryTokens + stats.ToolDefTokens
	stats.BudgetRemaining = e.maxTokens - stats.TotalTokens

	return &AssembledContext{
		SystemPrompt: systemPrompt,
		Messages:     messages,
		ToolDefs:     toolDefs,
		Stats:        stats,
	}, nil
}

// containsContent checks if a message with this content already exists.
func containsContent(msgs []ContextMessage, content string) bool {
	for _, m := range msgs {
		if m.Content == content {
			return true
		}
	}
	return false
}

func knowledgeQueryTerms(message string) []string {
	normalized := strings.ToLower(strings.TrimSpace(message))
	if normalized == "" {
		return nil
	}

	parts := strings.FieldsFunc(normalized, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_')
	})

	seen := make(map[string]struct{}, len(parts))
	terms := make([]string, 0, 8)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) < 3 {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		terms = append(terms, part)
		if len(terms) >= 8 {
			break
		}
	}

	if len(terms) == 0 {
		terms = append(terms, normalized)
	}
	return terms
}
