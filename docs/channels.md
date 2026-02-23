# Channel Setup Guide

How to connect your agent to messaging channels.

---

## Built-in Channels (no config needed)

### CLI Chat
```bash
openclio chat
```

### WebChat (browser UI)
```bash
openclio serve
# → http://127.0.0.1:18789
```

---

## Telegram

### 1. Create a bot
1. Open Telegram → message **@BotFather**
2. Send `/newbot`, choose a name and username (must end in `bot`)
3. Copy the token: `123456789:AAF...`

### 2. Configure
```bash
export TELEGRAM_BOT_TOKEN=123456789:AAF...
```
```yaml
# ~/.openclio/config.yaml
channels:
  telegram:
    token_env: TELEGRAM_BOT_TOKEN
```

```bash
openclio serve
```

### Tips
- Groups: mention the bot or use `/chat@yourbot <message>`
- Restrict access: set `allow_all: false` and run `openclio allow telegram <your_id>`

---

## Discord

### 1. Create an application
1. Go to [discord.com/developers/applications](https://discord.com/developers/applications)
2. **New Application** → name it
3. **Bot** → **Add Bot** → copy the **Token**
4. **OAuth2 → General** → copy the **Application ID**

### 2. Invite the bot
```
https://discord.com/api/oauth2/authorize?client_id=YOUR_APP_ID&scope=bot+applications.commands&permissions=2048
```

### 3. Enable intents
In the Developer Portal → **Bot**, enable:
- ✅ Server Members Intent
- ✅ Message Content Intent

### 4. Configure
```bash
export DISCORD_BOT_TOKEN=your_bot_token
export DISCORD_APP_ID=your_application_id
```
```yaml
channels:
  discord:
    token_env: DISCORD_BOT_TOKEN
    app_id_env: DISCORD_APP_ID    # optional — enables /chat slash command
```

```bash
openclio serve
```

---

## Allowlist (strict mode)

By default, all senders can use the agent. To restrict:

```yaml
channels:
  allow_all: false
```

Manage approved senders:
```bash
openclio allow telegram 123456789        # approve
agent deny  telegram 123456789        # revoke
openclio allow discord  987654321012345  # approve Discord user
openclio allowlist                       # list all approved senders
```

A blocked user receives:
> 🔒 *Access denied.* Your sender ID: `123456789`  
> Ask the owner to run: `openclio allow telegram 123456789`

---

## WhatsApp

Planned — will use [whatsmeow](https://github.com/tulir/whatsmeow) (Go-native, no Baileys).

---

## Multiple Channels

All adapters share the same agent loop:
```yaml
channels:
  telegram:
    token_env: TELEGRAM_BOT_TOKEN
  discord:
    token_env: DISCORD_BOT_TOKEN
    app_id_env: DISCORD_APP_ID
```
