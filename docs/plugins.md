# Writing a Channel Adapter

This guide explains how to create a new channel adapter and wire it into the agent.

---

## Overview

A **channel adapter** connects an external messaging platform (Telegram, Discord, WhatsApp, Slack, etc.) to the agent's internal message bus. Adapters run as goroutines within the agent process by default. As of v1.1, out-of-process adapters can connect over gRPC — enable `gateway.grpc_port` in config and see [`proto/agent.proto`](../proto/agent.proto) for the service contract.

### Message Flow

```
External Platform
    ↓  (user sends message)
Adapter.Start() → push to inbound chan
    ↓
plugin.Router.handleMessage()
    ↓  (allowlist check, session lookup)
agent.Run()  →  LLM + tools
    ↓
plugin.Manager.Send(adapterName, OutboundMessage)
    ↓
Adapter outbound loop → send to platform
    ↑  (agent replies)
```

---

## The `plugin.Adapter` Interface

Every adapter must implement:

```go
type Adapter interface {
    Name()  string   // unique identifier, e.g. "slack"
    Start(ctx context.Context,
          inbound  chan<- InboundMessage,
          outbound <-chan OutboundMessage) error
    Stop()
    Health() error
}
```

| Method | Contract |
|---|---|
| `Name()` | Return a unique lowercase string. Used for routing and logging. |
| `Start()` | **Block** until `ctx` is cancelled or `Stop()` is called. Push received messages to `inbound`. Read from `outbound` and deliver to the platform. |
| `Stop()` | Signal `Start()` to return. Called during graceful shutdown. |
| `Health()` | Return `nil` if connected, a descriptive error otherwise. Called every 30 s by the Manager. |

---

## Minimal Adapter Template

```go
package myadapter

import (
    "context"
    "fmt"
    "github.com/openclio/openclio/internal/plugin"
)

type Adapter struct {
    done chan struct{}
}

func New() *Adapter {
    return &Adapter{done: make(chan struct{})}
}

func (a *Adapter) Name() string { return "myadapter" }

func (a *Adapter) Health() error {
    // TODO: ping the platform API
    return nil
}

func (a *Adapter) Stop() {
    select {
    case <-a.done:
    default:
        close(a.done)
    }
}

func (a *Adapter) Start(ctx context.Context, inbound chan<- plugin.InboundMessage, outbound <-chan plugin.OutboundMessage) error {
    // Outbound delivery loop
    go func() {
        for {
            select {
            case <-ctx.Done():
                return
            case <-a.done:
                return
            case msg := <-outbound:
                // TODO: deliver msg.Text to msg.ChatID on the platform
                _ = msg
            }
        }
    }()

    // TODO: set up platform connection / webhook / polling loop
    // Push received messages:
    //   inbound <- plugin.InboundMessage{
    //       AdapterName: a.Name(),
    //       UserID:      "user-123",
    //       ChatID:      "chat-456",
    //       Text:        "Hello!",
    //   }

    // Block until shutdown
    select {
    case <-ctx.Done():
    case <-a.done:
    }
    return nil
}
```

---

## Registering the Adapter

### 1. Add config struct

In `internal/config/config.go`, add to `ChannelsConfig`:

```go
MyPlatform *MyPlatformConfig `yaml:"myplatform,omitempty"`
```

### 2. Add config fields

```go
type MyPlatformConfig struct {
    TokenEnv string `yaml:"token_env"`
}
```

### 3. Wire in `cmd/agent/main.go`

Inside `runServe`, under the channel setup block:

```go
if cfg.Channels.MyPlatform != nil {
    if token := os.Getenv(cfg.Channels.MyPlatform.TokenEnv); token != "" {
        a := myadapter.New(token, internlog.AsLogger(log))
        manager.Register(a)
        log.Info("myadapter registered")
    }
}
```

### 4. Import the package

```go
import myadapter "github.com/openclio/openclio/internal/plugin/adapters/myadapter"
```

---

## Auto-restart

The `Manager.Start()` wraps each adapter in a supervised goroutine (`runWithRestart`) that:

- Retries up to **5 times** after a crash
- Uses **exponential backoff** (1s → 2s → 4s → 8s → 16s)
- Resets the counter if the adapter survives for **60 seconds**
- Logs a final error and gives up after max retries

Your `Start()` implementation should return a descriptive error on failure — the manager logs it and schedules a restart.

---

## Health Checks

The manager polls `Health()` every **30 seconds**. Unhealthy adapters are logged at `ERROR` level. In v2, repeated unhealthy results will also trigger a restart.

```go
func (a *Adapter) Health() error {
    if !a.client.IsConnected() {
        return fmt.Errorf("myadapter: not connected")
    }
    return nil
}
```

---

## gRPC Out-of-Process Adapters

Out-of-process adapters are available now (v1.1). An external adapter binary connects over gRPC instead of running inside the agent process.

**Enable in config:**
```yaml
gateway:
  grpc_port: 18790
```

The adapter binary calls the `AgentCore` service to submit `InboundMessage` requests and receives streaming `OutboundMessage` tokens back. The agent core calls the `ChannelAdapter` service (which the adapter binary exposes) to deliver responses.

See [`proto/agent.proto`](../proto/agent.proto) for the full service contract. Generate Go stubs with:

```bash
make proto
```
