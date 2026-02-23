# AGENTS.md — Working in the openclio Codebase

This file is for AI coding agents (Claude Code, Copilot, Cursor, etc.) working in this repository. It describes the project structure, coding conventions, and how to run tests and builds correctly.

---

## Project Overview

openclio is a local-first personal AI agent written in Go. It is a single binary that:
1. Runs an HTTP/WebSocket gateway
2. Executes an agent loop (LLM calls + tool execution)
3. Manages a 3-tier context engine for token-efficient memory
4. Communicates with channel adapters (Telegram, Discord, etc.) over gRPC
5. Persists all data in a single SQLite database

---

## Directory Structure

```
cmd/openclio/         Entry point — cobra subcommand routing
internal/
├── agent/            LLM providers + agent loop
│   ├── anthropic.go  Anthropic (Claude) provider
│   ├── openai.go     OpenAI provider
│   ├── gemini.go     Google Gemini provider
│   ├── ollama.go     Ollama (local) provider
│   └── loop.go       Agent loop: receive → context → LLM → tools → respond
├── context/          3-tier memory engine
│   ├── engine.go     Main context assembler
│   ├── budget.go     Token budget allocator
│   ├── working.go    Tier 1: recent turns (in-RAM)
│   ├── episodic.go   Tier 2: vector search over past messages
│   └── semantic.go   Tier 3: persistent facts and preferences
├── tools/            Built-in tools
│   ├── registry.go   Tool registry and dispatcher
│   ├── exec.go       Shell command execution
│   ├── files.go      read_file, write_file, list_dir
│   └── web.go        web_search, web_fetch
├── gateway/          HTTP + WebSocket server
│   ├── server.go     Server setup and middleware
│   ├── chat.go       /api/v1/chat handler
│   ├── sessions.go   Session CRUD
│   └── auth.go       JWT middleware
├── rpc/              gRPC AgentCore server (for out-of-process adapters)
├── plugin/           In-process channel adapter manager
│   ├── manager.go    Start/stop/restart adapters
│   ├── telegram.go
│   ├── discord.go
│   ├── slack.go
│   └── whatsapp.go
├── mcp/              MCP stdio client
├── cron/             Scheduled task runner
├── workspace/        identity.md, user.md, memory.md, skills loader
├── cost/             Token tracking and cost estimation
├── logger/           Structured slog wrapper with secret scrubbing
└── storage/          SQLite layer
    ├── db.go         Connection, WAL mode, migrations
    ├── sessions.go   Session repository
    ├── messages.go   Message repository + vector embedding storage
    ├── memories.go   Semantic memory repository
    └── tools.go      Tool result repository

proto/                Protobuf definitions for gRPC channel adapter interface
docs/                 User-facing documentation
scripts/              Build and release scripts
Formula/              Homebrew formula
```

---

## How to Build

```bash
# Build for current platform
make build
# Output: bin/openclio

# Build all platforms
make build-all

# Clean
make clean
```

The binary embeds version info via ldflags. Do not use `go build` directly — always use `make build` so the version is set correctly.

---

## How to Test

```bash
# All unit tests
make test
# or
go test ./...

# Integration tests (real SQLite, no network)
go test -tags=integration ./...

# With coverage
make coverage

# Lint
make lint
```

Tests use `testify` for assertions. External services (LLM APIs, Telegram, etc.) are always mocked in unit and integration tests. Live tests (`-tags=live`) require real API keys and are not run in CI.

---

## Coding Conventions

### General
- Standard `gofmt` formatting — enforced by CI
- `golangci-lint` with the project's `.golangci.yml` config
- Error wrapping: use `fmt.Errorf("context: %w", err)` — never discard errors
- No `panic` in library code — only in `main` during startup validation

### Interfaces
- Define interfaces at the consumer, not the producer
- Keep interfaces small (1-3 methods)
- The LLM provider interface is in `internal/agent/provider.go` — all providers implement it

### Logging
- Use `internal/logger` — never `fmt.Println` or `log.Printf` in production code
- Always pass context: `logger.InfoCtx(ctx, "message", "key", value)`
- Never log raw API keys, tokens, or passwords — the logger scrubs known patterns automatically but don't rely on it as the only defense

### Database
- All DB access goes through repository structs in `internal/storage/`
- Never write SQL outside the storage package
- Always use parameterized queries — no string concatenation for SQL

### Security
- File path inputs must be validated through `internal/tools/pathsafe.go` before use
- Exec tool inputs must pass through the blocklist check in `internal/tools/exec.go`
- Never store API keys to disk — read from env vars only

---

## Adding a New LLM Provider

1. Create `internal/agent/<provider>.go`
2. Implement the `Provider` interface (`internal/agent/provider.go`)
3. Register it in `internal/agent/registry.go`
4. Add config fields to `internal/config/config.go`
5. Add a test file with mocked HTTP responses

---

## Adding a New Tool

1. Create or edit a file in `internal/tools/`
2. Implement the `Tool` interface (`internal/tools/registry.go`)
3. Register the tool in `internal/tools/registry.go`
4. Write unit tests with mock filesystem/HTTP as appropriate
5. Document it in `docs/tools.md`

---

## Adding a New Channel Adapter

See `docs/plugins.md` for the full guide. In short:
- In-process adapters: implement the `Adapter` interface in `internal/plugin/`
- Out-of-process adapters: use the gRPC service defined in `proto/agent.proto`

---

## Key Data Flow

```
User sends message
        │
        ▼
  Gateway (/api/v1/chat or /ws)
        │
        ▼
  Agent Loop (internal/agent/loop.go)
        │
        ├──► Context Engine: assemble prompt within token budget
        │         ├── Tier 1: last N turns (working memory)
        │         ├── Tier 2: top-K relevant past messages (vector search)
        │         └── Tier 3: relevant facts (semantic memory)
        │
        ├──► LLM Provider: send assembled context, stream response
        │
        ├──► If tool call: Tool System → execute → summarize result → loop
        │
        └──► Stream final response back to user
                │
                └── Storage: save messages + embeddings to SQLite
```

---

## Environment Variables for Local Dev

```bash
ANTHROPIC_API_KEY=sk-ant-...    # Required for Anthropic provider
OPENAI_API_KEY=sk-...           # Optional
GEMINI_API_KEY=...              # Optional
BRAVE_API_KEY=...               # Optional, for web_search tool
TELEGRAM_BOT_TOKEN=...          # Optional, for Telegram adapter
```

Copy `config.example.yaml` to `~/.openclio/config.yaml` and edit as needed.

---

## CI Checks

All PRs must pass:
1. `go vet ./...`
2. `golangci-lint run`
3. `go test ./...`
4. `govulncheck ./...`

The CI workflow is in `.github/workflows/ci.yml`.
