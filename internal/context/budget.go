package context

// BudgetAllocation describes how tokens are distributed across context components.
type BudgetAllocation struct {
	SystemPrompt     int // tokens allocated to system prompt
	UserMessage      int // tokens for current user message
	RecentTurns      int // tokens for working memory (last N turns)
	RetrievedHistory int // tokens for semantically retrieved past messages
	SemanticMemory   int // tokens for facts/preferences
	ToolDefinitions  int // tokens for tool schemas
	Reserved         int // tokens reserved for model response
	Total            int // total budget
}

// AllocateBudget distributes a total token budget across context components.
//
// Priority order (highest to lowest):
//  1. System prompt — always included, compressed
//  2. User message — always included in full
//  3. Reserved for response — model needs room to answer
//  4. Recent turns — last 3-5 turns of current conversation
//  5. Tool definitions — only enabled tools
//  6. Retrieved history — semantically relevant past messages
//  7. Semantic memory — facts and preferences
//
// The allocator guarantees: total allocations <= totalBudget.
func AllocateBudget(totalBudget int, systemPromptTokens int, userMessageTokens int, toolDefTokens int) BudgetAllocation {
	alloc := BudgetAllocation{
		Total: totalBudget,
	}

	remaining := totalBudget

	// 1. System prompt (mandatory)
	alloc.SystemPrompt = min(systemPromptTokens, remaining/4) // cap at 25% of budget
	remaining -= alloc.SystemPrompt

	// 2. User message (mandatory, full)
	alloc.UserMessage = min(userMessageTokens, remaining)
	remaining -= alloc.UserMessage

	// 3. Reserve 30% of remaining for model response
	alloc.Reserved = remaining * 30 / 100
	remaining -= alloc.Reserved

	// 4. Recent turns — 35% of what's left
	alloc.RecentTurns = remaining * 35 / 100
	remaining -= alloc.RecentTurns

	// 5. Tool definitions — actual size or 15% of remaining, whichever is smaller
	alloc.ToolDefinitions = min(toolDefTokens, remaining*15/100)
	remaining -= alloc.ToolDefinitions

	// 6. Retrieved history — 60% of what's left
	alloc.RetrievedHistory = remaining * 60 / 100
	remaining -= alloc.RetrievedHistory

	// 7. Semantic memory — everything remaining
	alloc.SemanticMemory = remaining

	return alloc
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
