# openclio — Local-first Personal AI Agent in Go

> A fast, private, token-efficient AI agent. Single binary, no cloud storage, no telemetry.

[![CI](https://github.com/openclio/openclio/actions/workflows/ci.yml/badge.svg)](https://github.com/openclio/openclio/actions)

---

## Quick Start

```bash
# Install
curl -sSL https://raw.githubusercontent.com/openclio/openclio/main/install.sh | sh

# Set an API key
export ANTHROPIC_API_KEY="sk-ant-..."

# First-time setup
openclio init

# Chat
openclio
```

## Features

| Feature | Detail |
|---|---|
| **Up to 7.2× fewer tokens** | 3-tier memory engine, prompt caching, token budget allocator |
| **Cost & Budgets** | Hard session/daily limits, real-time USD estimations |
| **Observability** | Log rotation, secret scrubbing, token tracking, `/debug` tools |
| **Single binary** | One ~24MB stripped binary (`CGO_ENABLED=0`), no Node.js, no pkg managers, no Docker required |
| **Multi-provider** | Anthropic, OpenAI, Gemini, Ollama (local) + optional failover |
| **Secure by default** | Loopback-only, JWT auth, path traversal prevention |
| **Web UI / Chat** | Visit `http://localhost:18789` or connect a channel adapter |
| **Channel adapters** | Telegram, Discord, WebChat (built-in), WhatsApp (experimental) |
| **MCP servers** | Connect any MCP-compatible tool server over stdio |
| **Cron tasks** | Schedule agent runs in `config.yaml` |

## Token Efficiency

The 3-tier context engine measurably reduces tokens sent per LLM call compared to naively including the full conversation history:

| Conversation length | Naive (all messages) | Engine (budget-capped) | Reduction |
|---|---|---|---|
| 10 turns | ~339 tokens | ~201 tokens | 41% |
| 25 turns | ~754 tokens | ~200 tokens | 74% |
| 50 turns | ~1,447 tokens | ~201 tokens | **87% (7.2×)** |

The engine combines recent-turn working memory, vector-similarity episodic retrieval, and a hard token budget allocator. Prompt caching (`cache_control` markers) further reduces billing on repeated system prompt content.

## Installation

### Quick install (Linux/macOS)
```bash
curl -sSL https://raw.githubusercontent.com/openclio/openclio/main/install.sh | sh
```

### From source
```bash
git clone https://github.com/openclio/openclio
cd openclio
make build
# Binary at bin/openclio
```

### Homebrew
```bash
# First time: tap the repository
brew tap openclio/tap https://github.com/openclio/openclio
brew install openclio/tap/openclio
```
> **Note:** SHA256 values in the formula are placeholders — they are filled with real values when a release binary is published.

## Usage

```
openclio                         Start interactive chat (default)
openclio init                    First-time setup wizard
openclio chat                    Start interactive chat
openclio serve                   Start HTTP server + channel adapters
openclio cost                    Show token usage and cost summary
openclio status                  Show agent status and config summary
openclio auth rotate             Generate a new auth token
openclio cron list               List scheduled cron jobs
openclio cron run <name>         Trigger a cron job immediately
openclio cron history            Show recent cron job results
openclio wipe                    Delete all conversation data (with confirmation)
openclio export                  Export all data to JSON
openclio allow <adapter> <id>    Approve a channel sender
openclio deny  <adapter> <id>    Block a channel sender
openclio allowlist               Show approved senders
openclio skills list             List available skill files
openclio migrate openclaw <path> Import OpenClaw history/identity files
openclio version                 Print version
```

### Interactive chat
```bash
openclio chat
```

Chat commands:
| Command | Action |
|---|---|
| `/help` | Show available commands |
| `/new` | Start a new session |
| `/sessions` | List recent sessions |
| `/history` | Show current session messages |
| `/usage` | Show token usage for this session |
| `/skill <name>` | Load a skill file into context |
| `exit` / `quit` | Quit |

### Server mode (HTTP + channel adapters)
```bash
openclio serve
# UI:   http://localhost:18789/?token=<auth-token>
# API:  http://localhost:18789/api/v1/
# WS:   ws://localhost:18789/ws
```

## Configuration

Config file: `~/.openclio/config.yaml` (created automatically on first run)

```yaml
model:
  provider: anthropic          # anthropic | openai | gemini | ollama
  model: claude-sonnet-4-20250514
  api_key_env: ANTHROPIC_API_KEY
  fallback_providers: [openai, ollama]

embeddings:
  provider: auto               # auto | openai | ollama
  model: nomic-embed-text

gateway:
  port: 18789
  bind: 127.0.0.1             # loopback only (safe default)
  grpc_port: 0                # set > 0 to enable gRPC adapter port

context:
  max_tokens_per_call: 8000

logging:
  level: info                  # debug | info | warn | error
  output: stderr               # stderr | stdout | /path/to/file.log

# Optional: Telegram bot
channels:
  telegram:
    token_env: TELEGRAM_BOT_TOKEN

# Optional: Scheduled tasks
cron:
  - name: daily-summary
    schedule: "0 9 * * *"     # 9 AM daily
    prompt: "Give me a brief summary of what I should focus on today."
    channel: telegram
```

See [docs/configuration.md](docs/configuration.md) for the full reference.

## Channel Adapters

Connect messaging platforms by setting the relevant environment variable and enabling the adapter in `config.yaml`:

| Adapter | Environment variable | Config key | Status |
|---|---|---|---|
| **WebChat** | *(built-in)* | Always enabled in `serve` mode | Stable |
| **Telegram** | `TELEGRAM_BOT_TOKEN` | `channels.telegram.token_env` | Stable |
| **Discord** | `DISCORD_BOT_TOKEN` | `channels.discord.token_env` | Stable |
| **WhatsApp** | *(none required)* | `channels.whatsapp.enabled: true` | Experimental (QR login via whatsmeow; Cloud API not yet available) |
| **Slack** | `SLACK_BOT_TOKEN` | `channels.slack.token_env` | Stable |

Example config for Telegram + Discord:
```yaml
channels:
  allow_all: true              # false = only approved senders (use openclio allow/deny)
  telegram:
    token_env: TELEGRAM_BOT_TOKEN
  discord:
    token_env: DISCORD_BOT_TOKEN
    app_id_env: DISCORD_APP_ID  # optional, for slash-command registration
```

### Out-of-process gRPC adapters

Custom adapters can connect over gRPC instead of running in-process. Enable the gRPC port:

```yaml
gateway:
  grpc_port: 18790
```

The adapter connects to `127.0.0.1:18790`, sends `InboundMessage` requests, and receives streaming `OutboundMessage` tokens back. See `proto/agent.proto` for the full service definition.

## MCP Servers

Connect any [Model Context Protocol](https://modelcontextprotocol.io) stdio server to expose its tools to the agent:

```yaml
mcp_servers:
  - name: filesystem
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/user/docs"]

  - name: github
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_TOKEN: GITHUB_TOKEN   # value is an env var name, not the literal token
```

Tools exposed by MCP servers appear alongside built-in tools in the agent's tool registry.

## Personalization

Create optional files in `~/.openclio/`:

| File | Purpose | Token limit |
|---|---|---|
| `identity.md` | Agent persona/name | ~100 tokens |
| `user.md` | About you (name, preferences) | ~100 tokens |
| `memory.md` | Persistent facts the agent should remember | ~500 tokens |
| `skills/*.md` | On-demand skill files (loaded with `/skill <name>`) | Unlimited |

Run `openclio init` to create these files interactively.

## Security

See [SECURITY.md](SECURITY.md) for the full threat model. Key points:

- **Network**: Binds to `127.0.0.1` by default — invisible to the network
- **Auth**: Auto-generated JWT token stored at `~/.openclio/auth.token` (0600)
- **Tools**: Path traversal blocked, dangerous commands blocklisted
- **Keys**: API keys loaded from env vars only, never written to disk
- **Logs**: All log output scrubbed of API keys and Bearer tokens
- **Injection**: Tool results wrapped in isolation delimiters before LLM sees them

## Architecture

```
cmd/openclio/       Entry point — subcommand routing
internal/
├── agent/          LLM providers (Anthropic, OpenAI, Gemini, Ollama), agent loop
├── context/        3-tier memory engine — recent turns + vector search + facts
├── tools/          exec, read_file, write_file, list_dir, web_fetch
├── gateway/        HTTP + WebSocket server, JWT auth, rate limiting
├── rpc/            gRPC AgentCore server for out-of-process channel adapters
├── plugin/         Channel adapter manager (Telegram, Discord, WebChat, WhatsApp)
├── mcp/            MCP stdio client — connects external tool servers
├── cron/           Scheduled task runner
├── workspace/      identity.md, user.md, memory.md, skills
├── cost/           Token usage tracking, cost estimation
├── logger/         Structured slog logger with secret scrubbing
└── storage/        SQLite (WAL mode, 0600 permissions)
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). All contributions welcome.

## License

MIT — see [LICENSE](LICENSE).
