# API Reference

The agent exposes a REST + WebSocket API when running in server mode (`openclio serve`).

## Authentication

All endpoints (except `/api/v1/health`) require a Bearer token:

```
Authorization: Bearer <token>
```

The token is generated on first `openclio serve` run and stored at `~/.openclio/auth_token`. It is printed (first 4 chars only) each time the server starts.

Rotate it with: `agent auth rotate`

---

## Endpoints

### `GET /api/v1/health`

Returns server health. No authentication required.

**Response `200`:**
```json
{
  "status": "ok",
  "time": "2025-01-01T00:00:00Z",
  "version": "v1.2.3"
}
```

---

### `POST /api/v1/chat`

Send a message to the agent and receive a response.

**Request body:**
```json
{
  "message": "What files are in my home directory?",
  "session_id": "optional-existing-session-id"
}
```

| Field | Required | Description |
|---|---|---|
| `message` | ✅ | User message text |
| `session_id` | — | Attach to an existing session. Omit to auto-create a new one. |

**Response `200`:**
```json
{
  "response": "Here are the files in your home directory: ...",
  "session_id": "sess_abc123",
  "usage": {
    "input_tokens": 412,
    "output_tokens": 88,
    "llm_calls": 1
  },
  "tools_used": [
    {
      "name": "exec",
      "arguments": {"command": "ls ~"},
      "result": "Documents  Downloads  Desktop"
    }
  ],
  "duration_ms": 1240
}
```

**Error responses:**

| Status | Cause |
|---|---|
| `400` | Bad request, malformed JSON, or body > 4 MB |
| `401` | Missing or invalid Bearer token |
| `429` | Rate limit exceeded (100 req/min per IP) |
| `500` | Agent or LLM provider error |

---

### `GET /api/v1/sessions`

List the most recent sessions (up to 50).

**Response `200`:**
```json
{
  "sessions": [
    {
      "id": "sess_abc123",
      "title": "File management",
      "created_at": "2025-01-01T10:00:00Z",
      "updated_at": "2025-01-01T10:05:00Z"
    }
  ],
  "count": 1
}
```

---

### `GET /api/v1/sessions/:id`

Get a single session including its full message history.

**Response `200`:**
```json
{
  "session": {
    "id": "sess_abc123",
    "title": "File management",
    "created_at": "2025-01-01T10:00:00Z"
  },
  "messages": [
    {"role": "user",      "content": "List my files", "created_at": "..."},
    {"role": "assistant", "content": "Here they are…", "created_at": "..."}
  ]
}
```

**Error `404`** if the session does not exist.

---

### `DELETE /api/v1/sessions/:id`

Permanently delete a session and all its messages.

**Response `200`:**
```json
{"deleted": true}
```

---

### `GET /api/v1/config`

Returns the active server configuration (sensitive fields like API keys are never included).

**Response `200`:**
```json
{
  "model":   {"provider": "anthropic", "model": "claude-sonnet-4-20250514"},
  "gateway": {"port": 18789, "bind": "127.0.0.1"},
  "agent":   {"max_tool_iterations": 10}
}
```

---

### `WebSocket /ws`

Persistent bidirectional chat connection. The server upgrades to WebSocket on connection.

**Client → Server** (send a message):
```json
{"content": "Hello, agent!"}
```

**Server → Client** (agent response):
```json
{"role": "assistant", "content": "Hello! How can I help?"}
```

The web chat UI at `/` uses this WebSocket endpoint automatically.

---

## Rate Limits

| Scope | Limit |
|---|---|
| Per IP | 100 requests / minute |
| Request body | 4 MB maximum |
| Tool iterations | 10 per message (configurable via `agent.max_tool_iterations`) |

---

## SDK / Client examples

**curl:**
```bash
TOKEN=$(cat ~/.openclio/auth_token)
curl -s -X POST http://localhost:18789/api/v1/chat \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"message": "What is todays date?"}'
```

**Python (`httpx`):**
```python
import httpx, os

client = httpx.Client(
    base_url="http://localhost:18789",
    headers={"Authorization": f"Bearer {os.getenv('AGENT_TOKEN')}"},
)
resp = client.post("/api/v1/chat", json={"message": "Hello!"})
print(resp.json()["response"])
```

**JavaScript (`fetch`):**
```js
const resp = await fetch("http://localhost:18789/api/v1/chat", {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    Authorization: `Bearer ${process.env.AGENT_TOKEN}`,
  },
  body: JSON.stringify({ message: "Hello from JS!" }),
});
const { response, session_id } = await resp.json();
```
