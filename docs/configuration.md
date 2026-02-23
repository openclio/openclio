# Configuration Reference

> Full YAML config for `~/.openclio/config.yaml`. All fields shown with defaults. Omit any section you don't need.

```yaml
# ── Model / Provider ──────────────────────────────────────────────────────────
model:
  provider:   anthropic          # anthropic | openai | gemini | ollama
  model:      claude-sonnet-4-20250514
  api_key_env: ANTHROPIC_API_KEY # name of env var holding your API key
  fallback_providers: []         # optional, e.g. [openai, ollama]

# ── Embeddings ────────────────────────────────────────────────────────────────
embeddings:
  provider: auto                 # auto | openai | ollama
  model: nomic-embed-text
  api_key_env: OPENAI_API_KEY
  base_url: http://localhost:11434

# ── Gateway (HTTP + WebSocket server) ─────────────────────────────────────────
gateway:
  port: 18789                    # port to listen on
  bind: 127.0.0.1                # 0.0.0.0 to expose to network (use with TLS)

# ── Context Engine ────────────────────────────────────────────────────────────
context:
  max_tokens_per_call:  8000     # total token budget per LLM call
  history_retrieval_k:  10       # how many past messages to retrieve via vector search
  proactive_compaction: 0.5      # compact history when context reaches 50% of budget
  compaction_keep_recent: 5      # keep this many recent turns un-compacted
  compaction_model: ""           # optional cheaper summarizer model
  tool_result_summary:  true     # auto-summarize large tool results

# ── Agent Loop ────────────────────────────────────────────────────────────────
agent:
  max_tool_iterations: 10        # max LLM ↔ tool round-trips per user message

# ── Tools ─────────────────────────────────────────────────────────────────────
tools:
  max_output_size: 102400        # max bytes captured from a shell command (100 KB)

  exec:
    sandbox: none                # none | namespace (Linux) | docker
    timeout: 30s                 # max wall time per shell command
    docker_image: alpine:latest
    network_access: false
    require_confirmation: false

  browser:
    enabled: false               # opt-in; requires local Chrome/Chromium
    chrome_path: ""              # auto-detect if empty
    headless: true
    timeout: 30s

  web_search:                    # optional — omit to disable
    provider:    brave
    api_key_env: BRAVE_API_KEY

# ── MCP Servers (optional, stdio) ────────────────────────────────────────────
mcp_servers:
  - name: filesystem
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "."]
  - name: github
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_TOKEN: GITHUB_TOKEN # env var indirection supported

# ── CLI ───────────────────────────────────────────────────────────────────────
cli:
  scanner_buffer: 65536          # terminal input buffer size in bytes (64 KB)

# ── Channel Adapters ──────────────────────────────────────────────────────────
channels:
  allow_all: true                # false = block senders not in allowed_senders.txt

  telegram:                      # omit to disable
    token_env: TELEGRAM_BOT_TOKEN

  discord:                       # omit to disable
    token_env: DISCORD_BOT_TOKEN
    app_id_env: DISCORD_APP_ID   # optional — needed for slash commands

  whatsapp:                      # QR login via WhatsApp Linked Devices
    enabled: false
    data_dir: ~/.openclio        # optional session store path (whatsapp.db)
    # token_env / webhook_url are reserved for a future Cloud API mode and currently ignored

# ── Scheduled Tasks (Cron) ────────────────────────────────────────────────────
cron:
  - name: daily-summary
    schedule: "0 9 * * *"
    prompt: "Summarize yesterday's activity"
    channel: telegram
    session_mode: isolated       # isolated | shared (default: isolated)

# ── Logging ───────────────────────────────────────────────────────────────────
logging:
  level:  info                   # debug | info | warn | error
  output: stderr                 # stderr | stdout | /path/to/file.log
```

---

## Field Reference

### `model`

| Field | Type | Default | Description |
|---|---|---|---|
| `provider` | string | `anthropic` | LLM provider: `anthropic`, `openai`, `gemini`, `ollama` |
| `model` | string | `claude-sonnet-4-20250514` | Model name passed to the provider API |
| `api_key_env` | string | `ANTHROPIC_API_KEY` | **Name** of the env var holding the API key (never the key itself) |
| `fallback_providers` | []string | `[]` | Optional ordered failover providers |
| `fallback_models` | map | `{}` | Optional per-provider model overrides for fallback providers |
| `fallback_api_key_env` | map | `{}` | Optional per-provider API key env var names for fallback providers |

**Provider quick-reference:**

| Provider | Example models | API key env |
|---|---|---|
| `anthropic` | `claude-sonnet-4-20250514`, `claude-3-haiku-20240307` | `ANTHROPIC_API_KEY` |
| `openai` | `gpt-4o`, `gpt-4o-mini`, `gpt-4-turbo` | `OPENAI_API_KEY` |
| `gemini` | `gemini-1.5-flash`, `gemini-1.5-pro` | `GEMINI_API_KEY` |
| `ollama` | `llama3.2`, `mistral`, `qwen2.5-coder` | *(none — runs locally)* |

### `embeddings`

| Field | Type | Default | Description |
|---|---|---|---|
| `provider` | string | `auto` | `auto`, `openai`, `ollama` |
| `model` | string | `nomic-embed-text` | Embedding model name |
| `api_key_env` | string | `OPENAI_API_KEY` | Env var for OpenAI embedding key |
| `base_url` | string | `http://localhost:11434` | Ollama base URL |

### `gateway`

| Field | Type | Default | Description |
|---|---|---|---|
| `port` | int | `18789` | HTTP port |
| `bind` | string | `127.0.0.1` | Bind address. Use `0.0.0.0` only behind a TLS reverse proxy |

### `context`

| Field | Type | Default | Description |
|---|---|---|---|
| `max_tokens_per_call` | int | `8000` | Total token budget per LLM call |
| `history_retrieval_k` | int | `10` | Past messages recalled via vector search |
| `proactive_compaction` | float | `0.5` | Compact history when ≥ this fraction of budget is used |
| `compaction_keep_recent` | int | `5` | Number of recent turns kept verbatim during compaction |
| `compaction_model` | string | `""` | Optional model name to use for compaction summaries |
| `tool_result_summary` | bool | `true` | Auto-summarize large tool results |

### `agent`

| Field | Type | Default | Description |
|---|---|---|---|
| `max_tool_iterations` | int | `10` | Maximum number of LLM ↔ tool call round-trips per user message. Prevents runaway loops. |

### `tools`

| Field | Type | Default | Description |
|---|---|---|---|
| `max_output_size` | int | `102400` | Max bytes captured from a shell command output (100 KB). Output is truncated at this limit. |

### `tools.exec`

| Field | Type | Default | Description |
|---|---|---|---|
| `sandbox` | string | `none` | `none` / `namespace` (Linux) / `docker` |
| `timeout` | duration | `30s` | Wall-clock limit per command |
| `docker_image` | string | `alpine:latest` | Docker image used in `docker` sandbox mode |
| `network_access` | bool | `false` | Allow network access in docker/namespace sandbox |
| `require_confirmation` | bool | `false` | Prompt for user confirmation before each `exec` run |

### `tools.browser`

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable browser automation tool |
| `chrome_path` | string | `""` | Optional path to Chrome/Chromium binary |
| `headless` | bool | `true` | Run browser in headless mode |
| `timeout` | duration | `30s` | Per-action timeout |

### `mcp_servers`

| Field | Required | Description |
|---|---|---|
| `name` | ✅ | Unique server name used for tool prefixing |
| `command` | ✅ | Executable to launch MCP server (stdio transport) |
| `args` | — | Command arguments |
| `env` | — | Key/value env vars (supports env var indirection) |

### `cli`

| Field | Type | Default | Description |
|---|---|---|---|
| `scanner_buffer` | int | `65536` | Size of the terminal line scanner buffer in bytes. Increase if you paste very long inputs into the interactive CLI. |

### `channels`

| Field | Type | Default | Description |
|---|---|---|---|
| `allow_all` | bool | `true` | `false` = strict allowlist mode |
| `telegram.token_env` | string | — | Env var with Telegram bot token |
| `discord.token_env` | string | — | Env var with Discord bot token |
| `discord.app_id_env` | string | — | Env var with Discord application ID (slash commands) |
| `whatsapp.enabled` | bool | `false` | Enable WhatsApp adapter (QR login mode) |
| `whatsapp.data_dir` | string | `~/.openclio` | Directory used to store `whatsapp.db` session state |
| `whatsapp.token_env` | string | — | Reserved for a future Cloud API mode (currently ignored) |
| `whatsapp.webhook_url` | string | — | Reserved for a future Cloud API mode (currently ignored) |

### `cron`

| Field | Required | Description |
|---|---|---|
| `name` | ✅ | Unique job name |
| `schedule` | ✅ | 5-field cron expression |
| `prompt` | ✅ | Message sent to agent on each run |
| `channel` | — | Adapter to send result to |
| `session_mode` | — | `isolated` (default) or `shared` |

### `logging`

| Field | Type | Default | Description |
|---|---|---|---|
| `level` | string | `info` | `debug` / `info` / `warn` / `error` |
| `output` | string | `stderr` | `stderr`, `stdout`, or file path |

### `retention`

> All fields default to **0**, which disables pruning. Enable only if you want automatic data cleanup.

| Field | Type | Default | Description |
|---|---|---|---|
| `sessions_days` | int | `0` | Delete sessions older than N days. `0` = keep forever. |
| `messages_per_session` | int | `0` | Trim oldest messages when a session exceeds N messages. `0` = no trim. |

---

## Environment Variable Overrides

| Env Var | Overrides |
|---|---|
| `AGENT_PORT` | `gateway.port` |
| `AGENT_BIND` | `gateway.bind` |
| `AGENT_MODEL_PROVIDER` | `model.provider` |
| `AGENT_MODEL` | `model.model` |
| `AGENT_LOG_LEVEL` | `logging.level` |

---

## Minimal Config Examples

**Gemini:**
```yaml
model:
  provider: gemini
  model: gemini-1.5-flash
  api_key_env: GEMINI_API_KEY
```

**Ollama (free, local):**
```yaml
model:
  provider: ollama
  model: llama3.2
```

**OpenAI + Telegram:**
```yaml
model:
  provider: openai
  model: gpt-4o-mini
  api_key_env: OPENAI_API_KEY
channels:
  telegram:
    token_env: TELEGRAM_BOT_TOKEN
```

**Strict rate limits + no tools piping:**
```yaml
agent:
  max_tool_iterations: 5
tools:
  max_output_size: 32768   # 32 KB hard cap on command output
  exec:
    timeout: 10s
```
