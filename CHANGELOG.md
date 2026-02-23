# Changelog

All notable changes to openclio are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
openclio uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added

**Core agent**
- 3-tier context engine: working memory (recent turns) + episodic memory (SQLite vector search) + semantic memory (persistent facts)
- Token budget allocator — hard per-call ceiling with allocation across system prompt, history, tool definitions, and user message
- Proactive context compaction at 50% budget (not reactive on overflow)
- Tool result auto-summarization — full output stored on disk, compressed summary in context
- Prompt caching support for Anthropic and OpenAI (reduces billing on repeated system prompt content)

**LLM providers**
- Anthropic (Claude) provider with streaming and tool use
- OpenAI (GPT-4o, GPT-4) provider with streaming and function calling
- Google Gemini provider
- Ollama provider for local models
- Provider failover chain — automatic retry on next configured provider

**Gateway**
- HTTP REST API (`/api/v1/chat`, `/api/v1/sessions`, `/api/v1/health`, `/api/v1/config`)
- WebSocket endpoint for streaming responses (`/ws`)
- JWT authentication — auto-generated token stored at `~/.openclio/auth.token` (0600)
- Loopback-only default binding (`127.0.0.1`) — network exposure requires explicit opt-in
- Rate limiting and graceful shutdown

**Tools**
- `exec` — shell command execution with timeout, output size limit, and dangerous-command blocklist
- `read_file` — file reading with path traversal prevention
- `write_file` — file writing restricted to workspace
- `list_dir` — directory listing (recursive optional)
- `web_search` — Brave Search API integration
- `web_fetch` — URL content fetching with HTML-to-text conversion

**Channel adapters**
- Telegram adapter (stable)
- Discord adapter (stable)
- Slack adapter (stable)
- WebChat built-in UI (served at `http://localhost:18789`)
- WhatsApp adapter via whatsmeow — QR code pairing (experimental)
- gRPC out-of-process adapter interface for custom channels

**MCP integration**
- MCP stdio client — connect any Model Context Protocol server and expose its tools to the agent

**CLI**
- `openclio` / `openclio chat` — interactive terminal chat with streaming output
- `openclio serve` — headless server mode (gateway + channel adapters)
- `openclio init` — interactive setup wizard
- `openclio cost` — token usage and cost breakdown by session, day, provider
- `openclio status` — live agent status, connected channels, session count
- `openclio auth rotate` — rotate the auth token
- `openclio cron list/run/history` — manage scheduled tasks
- `openclio wipe` — delete all data with confirmation
- `openclio export` — export all data to JSON
- `openclio allow/deny/allowlist` — manage approved channel senders
- `openclio skills list` — list available skill files
- `openclio migrate openclaw <path>` — import OpenClaw history and identity files

**Cron / scheduled tasks**
- YAML-defined cron jobs with cron expression scheduling
- Per-job channel routing (run a prompt, deliver output to Telegram/Discord/etc.)
- `agent cron run <name>` for manual trigger

**Personalization**
- `~/.openclio/identity.md` — agent persona (compressed to ~100 tokens before injection)
- `~/.openclio/user.md` — user preferences (compressed to ~100 tokens)
- `~/.openclio/memory.md` — persistent facts the agent remembers across sessions
- `~/.openclio/skills/` — on-demand skill files loaded with `/skill <name>` (not injected every call)

**Security**
- 8-layer security model: network isolation, JWT auth, tool sandboxing (namespace/docker/none), API key protection (env vars only), data privacy, process isolation, supply chain hardening, prompt injection defense
- Log output auto-scrubs API key patterns (`sk-...`, Bearer tokens)
- Tool results wrapped in isolation delimiters before LLM sees external content
- Linux namespace sandboxing for `exec` tool (configurable)
- Docker sandbox mode (optional)

**Storage**
- Single SQLite database (`~/.openclio/data.db`, 0600 permissions, WAL mode)
- `sqlite-vec` extension for vector similarity search
- Versioned migration runner

**Observability**
- Structured JSON logging via `slog` (stdlib)
- Log rotation (configurable max size and file count)
- Per-request trace IDs
- `/debug` chat commands for context inspection and token breakdown
- Optional Prometheus metrics endpoint

**Distribution**
- `curl -sSL .../install.sh | sh` installer
- Pre-built binaries: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
- Homebrew formula (`brew install openclio/tap/openclio`)
- Dockerfile for server deployments
- GPG-signed release binaries
- SBOM (Software Bill of Materials) published with each release
- `govulncheck` + `gosec` in CI pipeline

---

## Links

- [Releases](https://github.com/openclio/openclio/releases)
- [Unreleased diff](https://github.com/openclio/openclio/compare/HEAD)
