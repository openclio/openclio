# How Memory Works

The agent uses a **3-tier memory system** to efficiently fit long conversations within the LLM's token budget.

## Token Budget

Every LLM call has a token budget (default: 8,000 tokens). The context engine allocates this budget across tiers:

```
Budget: 8,000 tokens
├── System prompt        ~500 tokens  (mandatory)
├── User message         ~200 tokens  (mandatory)
├── Tier 1 — Recent      ~2,400 tokens (recent turns)
├── Tier 2 — Retrieved   ~1,600 tokens (semantic search)
├── Tier 3 — Memory      ~800 tokens  (persistent facts)
└── Reserved for output  ~2,500 tokens (30%)
```

## Tier 1 — Working Memory (Recent Turns)

The last 3–5 turns of conversation are always included. This ensures continuity in active conversations.

## Tier 2 — Episodic Memory (Vector Search)

All past messages are embedded with OpenAI's `text-embedding-3-small` (or skipped if no OpenAI key). When you send a message, the engine performs semantic search over all past messages and retrieves the most relevant ones.

This means the agent can recall things you said weeks ago if they're relevant to the current question.

**No embedding key?** The agent falls back to a NoOp embedder — only Tier 1 and Tier 3 are used.

## Tier 3 — Semantic Memory (Persistent Facts)

Create `~/.openclio/memory.md` with facts you want the agent to always remember:

```markdown
# Memory

- My name is Idris
- I'm building a Go AI agent project
- I prefer concise technical answers
- My server is at 192.168.1.50
- I use macOS with zsh
```

This file is loaded fresh on every call, so edits take effect immediately.

## Personalization Files

| File | Injected | Purpose |
|---|---|---|
| `~/.openclio/identity.md` | Every call | Agent persona/name |
| `~/.openclio/user.md` | Every call | About you |
| `~/.openclio/memory.md` | Every call | Facts to always remember |
| `~/.openclio/skills/*.md` | On demand | Specialized instructions |

All files are auto-compressed to fit within token budgets.

## Skills (On-Demand)

Skills are loaded only when you explicitly request them:

```bash
# Create a skill
cat > ~/.openclio/skills/code-review.md << 'EOF'
When reviewing code, check for:
- Security vulnerabilities
- Performance bottlenecks
- Missing error handling
- Test coverage gaps
EOF

# Use it in chat
/skill code-review
Please review this function: ...
```

## Tuning

```yaml
context:
  max_tokens_per_call: 8000    # increase for longer conversations
  history_retrieval_k: 5       # how many past messages to retrieve
```
