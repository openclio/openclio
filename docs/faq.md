# Frequently Asked Questions

## General

**How much does this cost?**
The agent itself is 100% free and open-source. You only pay for the API tokens you consume via your provider (Anthropic, OpenAI). If you use local models via Ollama, it is completely free.

**Where is my data stored?**
All of your data (chat history, configuration, memories, metrics) is stored locally on your machine in `~/.openclio/`. No data is ever sent to a central server or third-party database.

**Can I run it on my phone?**
The agent runs on your computer or a server. However, you can connect it to Telegram or WhatsApp, allowing you to seamlessly chat with your local AI agent from your phone no matter where you are. Check out the [Channels Guide](channels.md).

## Usage & Memory

**How does the agent remember my name or preferences?**
The agent uses a 3-tier memory engine. It reads `~/.openclio/user.md` for explicit facts about you, uses vector similarity search across its SQLite database for episodic memory, and automatically drops old context to save tokens.

**Is it safe to let the agent run arbitrary tool commands?**
By default, the agent runs commands directly on your host machine. We have blocked extremely dangerous commands (`rm -rf`, `mkfs`) and prevent path traversal beyond your safe directories, but you should treat the agent as an administrator on your network. For hard isolation, we recommend configuring a Docker sandbox (see [Tools Guide](tools.md)).

## Troubleshooting

**I'm seeing a lot of "budget exceeded" errors. What do I do?**
Check your `~/.openclio/config.yaml`. By default, the agent caps itself at spending 500,000 tokens per day. You can increase `max_tokens_per_day` or switch to a cheaper model (like `claude-3-haiku` or `gpt-4o-mini`).

**The agent can't remember something from yesterday.**
If a past message was very short or didn't contain enough keywords, the semantic search engine might not retrieve it. Try explicitly telling the agent to "write this down in your persistent memory" — it will use the `memory_write` tool to save it permanently in `~/.openclio/memory.md`.
