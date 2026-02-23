# Getting Started with Agent

## Prerequisites

- macOS, Linux, or Windows
- An API key for your chosen provider (Anthropic, OpenAI, or local Ollama)

## 1. Install

Using `go install`:
```bash
go install github.com/openclio/openclio/cmd/agent@latest
```

Using the install script:
```bash
curl -sSL https://raw.githubusercontent.com/openclio/openclio/main/install.sh | sh
```

Or build from source:
```bash
git clone https://github.com/openclio/openclio
cd agent && make build
cp bin/openclio ~/.local/bin/
```

## 2. Run Setup Wizard and Choose Provider

```bash
openclio init
```

The wizard lets you choose `ollama`, `openai`, `anthropic`, or `gemini`.
For cloud providers, it tells you which `*_API_KEY` env var to export.

## 3. Set Credentials (if required)

```bash
# Example for Ollama (local, no cloud key needed)
ollama pull llama3.2
```

Add your export to `~/.bashrc` or `~/.zshrc` to persist it.

## 4. Start Chatting

```bash
openclio
```

That's it. The agent creates `~/.openclio/config.yaml` on first run with sensible defaults.

## 5. Configure (Optional)

```bash
cat ~/.openclio/config.yaml
```

Switch to a different model:
```yaml
model:
  provider: openai
  model: gpt-4o
  api_key_env: OPENAI_API_KEY
```

## 6. Try Some Commands

Inside the chat:
```
/help       — see all commands
/new        — start a fresh session
/sessions   — list past sessions
/history    — show current conversation
/usage      — show token stats
exit        — quit
```

## 7. Start the Server (for Telegram / Web UI)

```bash
export TELEGRAM_BOT_TOKEN="..."
openclio serve
# Visit http://localhost:8080 for the web chat UI
```

## Next Steps

- [Configuration Reference](configuration.md)
- [Setting up Channels](channels.md)
- [Available Tools](tools.md)
- [Security Model](../SECURITY.md)
