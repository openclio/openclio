# Vision — openclio

> A local-first personal AI agent, built in Go, that stays fast, private, and token-efficient.

---

## What openclio Is

openclio is a self-hosted personal AI agent you run on your own machine. It connects to the LLM providers you already use (Anthropic, OpenAI, Gemini, Ollama), responds through the messaging apps you already live in (Telegram, Discord, WhatsApp, Slack), and does real work — running commands, reading and editing files, searching the web, scheduling tasks.

Everything stays on your machine. Nothing phones home.

---

## Why It Exists

Personal AI agents are powerful. But the dominant open-source options have architectural problems that compound over time:

- They send your entire conversation history to the LLM on every message
- They embed thousands of tokens of workspace files into every system prompt
- They run as heavyweight Node.js processes with hundreds of transitive dependencies
- They treat channels and tools as in-process monoliths, so one crash can take down everything

openclio was built to fix all of this at the architecture level — not with configuration knobs, but by getting the design right from the start.

---

## Core Principles

### 1. Token efficiency is a first-class feature
Every LLM call has a token budget. The 3-tier context engine (working memory → episodic retrieval → semantic facts) sends only what is relevant, not everything. Tool results are auto-summarized. System prompts are compressed and cached. At 50 conversation turns, openclio uses ~87% fewer input tokens than a naive implementation.

### 2. Local-first, always
Your data lives in a single SQLite database on your machine (`~/.openclio/data.db`). Zero telemetry. Zero cloud storage. Zero analytics. You can export or wipe everything in one command.

### 3. Single binary
`curl | sh` and you're running. No Node.js, no npm, no Docker, no build step. One ~24MB Go binary that works on Linux, macOS, and Windows. Cross-compiled for amd64 and arm64.

### 4. Process isolation by default
Channel adapters (Telegram, Discord, WhatsApp) run as separate OS processes communicating over gRPC. A crash or exploit in one adapter cannot affect another or reach your files and tools.

### 5. Security without configuration
Loopback-only by default. Auto-generated JWT auth. API keys loaded from environment variables — never stored in config files. Dangerous commands blocklisted. Path traversal blocked at the code level. Eight defense layers, all active by default.

### 6. Minimal, auditable dependencies
~15 direct Go dependencies. No node_modules. No npm audit noise. Every dependency vendored, every release binary GPG-signed and reproducible.

---

## What openclio Is Not

- **Not a cloud service.** There is no openclio.com account, no subscription, no SaaS tier.
- **Not an enterprise platform.** v1 targets individual developers, power users, and privacy-conscious people.
- **Not a framework.** openclio is a working agent, not a toolkit for building agents.
- **Not a chat UI.** The web UI is a thin convenience layer. The real value is in the agent, the memory engine, and the channel adapters.

---

## Roadmap

### Phase 1 — Foundation (done)
- HTTP/WebSocket gateway with JWT auth
- Agent loop with multi-provider support (Anthropic, OpenAI, Gemini, Ollama)
- 3-tier context engine with SQLite vector search
- CLI chat interface
- YAML config system

### Phase 2 — Tools + Memory (done)
- Built-in tools: `exec`, `read_file`, `write_file`, `list_dir`, `web_search`, `web_fetch`
- Tool result auto-summarization
- Token budget optimizer with proactive compaction
- Prompt caching

### Phase 3 — Channels (done)
- Telegram, Discord, Slack adapters
- Built-in WebChat UI
- WhatsApp adapter (experimental)
- gRPC out-of-process adapter interface

### Phase 4 — Polish (in progress)
- Cron/scheduled tasks
- Workspace personalization (`identity.md`, `user.md`, `memory.md`, skills)
- MCP server integration
- Security audit
- First public release

### Beyond v1
- Browser automation
- Voice/TTS
- Multi-agent task delegation
- Native mobile companion app

---

## Design Non-Goals

These are things we have deliberately chosen not to do in v1:

- Multi-user support (openclio is personal, not shared)
- Cloud sync or backup (local-only is the point)
- GUI installer (CLI is first-class)
- Plugin marketplace (quality over quantity)
- Automatic updates (you control when you upgrade)

---

*openclio is MIT-licensed. Contributions welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).*
