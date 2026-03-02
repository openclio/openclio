# Changelog

All notable changes to openclio are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
openclio uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

No unreleased changes at this time.

---

## [0.1.2] — 2026-03-02

### Added

**Authentication**
- `openclio auth login` — terminal-based OpenAI OAuth flow; opens a browser tab (or prints the URL) and completes PKCE sign-in without any manual API key copy-paste
- OAuth sign-in integrated into `openclio init` wizard as an alternative to manual API key entry for OpenAI
- OAuth token stored at `~/.openclio/openai_oauth_token.json` (mode `0600`)

**Memory — Tier 4: Knowledge Graph**
- Automatic entity and relation extraction from conversations (`internal/kg`)
- Entities stored in `kg_nodes` + `kg_edges` SQLite tables (migration 012)
- Knowledge graph queried per-message and injected as a `[Knowledge graph]` system block within the token budget
- `openclio memory list` — display all stored entities
- `openclio memory search <query>` — search entities by name or type
- `openclio memory edit` — open entity store in `$EDITOR`

**History and Undo**
- `openclio history` — show recent `write_file` and `exec` tool actions with IDs
- `openclio undo <id>` — revert one `write_file` action by its history ID

**Privacy**
- `openclio privacy` — show privacy settings, redaction rules, and aggregate usage summary

**Context engine**
- 4-tier memory engine: working memory → episodic (vector) → semantic facts → knowledge graph
- Tier 4 shares the semantic memory token budget and injects only when query terms match stored entities

---

## [0.1.1] — 2026-02-26

### Added

- Provider presets in `openclio init`: one-step setup for Anthropic, OpenAI, Gemini, and Ollama
- Memory management enhancements: `~/.openclio/memory.md` improvements and templates
- Image generation features
- Installer improvements: server auto-start, improved UX, `OPENCLIO_INSTALL_DIR` normalization
- Open-core distribution boundaries and private-edition scaffolding (`internal/edition`)

### Fixed

- Ollama provider: force IPv4 (`127.0.0.1`) to avoid IPv6 connection failures on some systems
- WhatsApp channel: improved path resolution and session management
- MCP client: improved error handling on server disconnects
- Installer: prevent unbound variable errors when running with `set -u`
- Tool execution: policy enforcement in the tool registry

### Changed

- Security comparison docs: updated from "Your Agent" to "OpenClio" branding

---

## [0.1.0] — 2026-02-20

Initial public release.

### Added

**Core agent**
- 3-tier context engine: working memory (recent turns) + episodic memory (SQLite vector search) + semantic memory (persistent facts)
- Token budget allocator — hard per-call ceiling with priority-ordered allocation across system prompt, history, tool definitions, and user message
- Proactive context compaction at 50% budget (not reactive on overflow)
- Tool result auto-summarization — full output stored on disk, compressed summary in context
- Prompt caching support for Anthropic and OpenAI (reduces billing on repeated system prompt content)

**LLM providers**
- Anthropic (Claude) provider with streaming and tool use
- OpenAI (GPT-4o, GPT-4) provider with streaming and function calling
- Google Gemini provider
- Ollama provider for local models (IPv4 fixed in v0.1.1)
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
- `openclio init` — interactive setup wizard with 5 personality styles
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
- Per-job channel routing (run a prompt, deliver result to Telegram/Discord/etc.)
- `openclio cron run <name>` for manual trigger

**Personalization**
- `~/.openclio/identity.md` — agent persona (compressed to ~100 tokens before injection)
- `~/.openclio/user.md` — user preferences (compressed to ~100 tokens)
- `~/.openclio/memory.md` — persistent facts the agent remembers across sessions
- `~/.openclio/skills/` — on-demand skill files loaded with `/skill <name>`
- 8 built-in skill templates seeded on first run

**Security**
- 8-layer security model: network isolation, JWT auth, tool sandboxing, API key protection, data privacy, process isolation, supply chain hardening, prompt injection defense
- Log output auto-scrubs API key patterns (`sk-...`, Bearer tokens)
- Tool results wrapped in isolation delimiters before LLM sees external content
- Linux namespace sandboxing for `exec` tool (configurable)
- Docker sandbox mode (optional)

**Storage**
- Single SQLite database (`~/.openclio/data.db`, 0600 permissions, WAL mode)
- `sqlite-vec` extension for vector similarity search
- Versioned migration runner (11 migrations)

**Observability**
- Structured JSON logging via `slog` (stdlib)
- Log rotation (configurable max size and file count)
- Per-request trace IDs
- `/debug` chat commands for context inspection and token breakdown
- Optional Prometheus metrics endpoint

**Distribution**
- `curl -sSL .../install.sh | sh` one-line installer
- Pre-built binaries: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
- Homebrew formula (`brew install openclio/tap/openclio`)
- Dockerfile for server deployments
- GPG-signed release binaries
- SBOM (Software Bill of Materials) published with each release
- `govulncheck` + `gosec` in CI pipeline

---

## Links

- [Releases](https://github.com/openclio/openclio/releases)
- [0.1.2 diff](https://github.com/openclio/openclio/compare/v0.1.1...v0.1.2)
- [0.1.1 diff](https://github.com/openclio/openclio/compare/v0.1.0...v0.1.1)
