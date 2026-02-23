// Package plugin provides the channel adapter infrastructure for the agent.
// Adapters receive messages from external channels (Telegram, WebChat, etc.)
// and route them through the agent loop. Each adapter runs independently as
// a goroutine and communicates via Go channels.
package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// InboundMessage is a message received from an external channel.
type InboundMessage struct {
	AdapterName string // which adapter sent this
	UserID      string // external user identifier
	ChatID      string // external chat/channel identifier
	Text        string // message text
	SessionID   string // agent session ID (assigned by router)
}

// OutboundMessage is a message to send back to an external channel.
type OutboundMessage struct {
	ChatID string // external chat/channel identifier
	UserID string // external user identifier
	Text   string // response text
}

// Adapter is the interface all channel adapters implement.
type Adapter interface {
	// Name returns the adapter identifier (e.g., "telegram", "webchat").
	Name() string

	// Start begins processing messages. It should block until stopped.
	// Messages are sent via the inbound channel and responses received on outbound.
	Start(ctx context.Context, inbound chan<- InboundMessage, outbound <-chan OutboundMessage) error

	// Stop signals the adapter to shut down gracefully.
	Stop()

	// Health returns nil if the adapter is healthy, or an error otherwise.
	Health() error
}

// QRCodeState is an optional runtime status payload for adapters that support
// QR-based pairing flows.
type QRCodeState struct {
	Event     string    `json:"event"`
	Code      string    `json:"code,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// Manager manages the lifecycle of all registered adapters.
type Manager struct {
	adapters []Adapter
	inbound  chan InboundMessage
	outbound map[string]chan OutboundMessage // adapterName → chan
	stats    map[string]*AdapterStatus
	logger   *slog.Logger
	wg       sync.WaitGroup
	mu       sync.RWMutex
}

// AdapterStatus captures runtime information for one channel adapter.
type AdapterStatus struct {
	Name                string    `json:"name"`
	Running             bool      `json:"running"`
	Healthy             bool      `json:"healthy"`
	LastHealthCheck     time.Time `json:"last_health_check,omitempty"`
	LastHealthError     string    `json:"last_health_error,omitempty"`
	RestartCount        int       `json:"restart_count"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	LastStart           time.Time `json:"last_start,omitempty"`
	LastError           string    `json:"last_error,omitempty"`
}

// NewManager creates a new adapter manager.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{
		inbound:  make(chan InboundMessage, 64),
		outbound: make(map[string]chan OutboundMessage),
		stats:    make(map[string]*AdapterStatus),
		logger:   logger,
	}
}

// Register adds an adapter to the manager.
func (m *Manager) Register(a Adapter) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, existing := range m.adapters {
		if existing.Name() == a.Name() {
			m.logError("adapter already registered, skipping", "adapter", a.Name())
			return
		}
	}

	m.adapters = append(m.adapters, a)
	m.outbound[a.Name()] = make(chan OutboundMessage, 32)
	if _, exists := m.stats[a.Name()]; !exists {
		m.stats[a.Name()] = &AdapterStatus{
			Name:    a.Name(),
			Healthy: true,
		}
	}
}

// Unregister removes an adapter from manager routing/state by name.
// The adapter should be stopped before unregistering.
func (m *Manager) Unregister(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, a := range m.adapters {
		if a.Name() != name {
			continue
		}
		m.adapters = append(m.adapters[:i], m.adapters[i+1:]...)
		break
	}
	delete(m.outbound, name)
	if st, ok := m.stats[name]; ok {
		st.Running = false
		st.Healthy = false
		st.LastHealthCheck = time.Now().UTC()
		st.LastHealthError = "disconnected"
	}
}

// Inbound returns the channel for receiving messages from all adapters.
func (m *Manager) Inbound() <-chan InboundMessage { return m.inbound }

// Send delivers a response to a specific adapter.
func (m *Manager) Send(adapterName string, msg OutboundMessage) error {
	m.mu.RLock()
	ch, ok := m.outbound[adapterName]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("unknown adapter: %s", adapterName)
	}

	select {
	case ch <- msg:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("adapter %s: send timeout", adapterName)
	}
}

// logInfo logs at info level if the logger is non-nil.
func (m *Manager) logInfo(msg string, args ...any) {
	if m.logger != nil {
		m.logger.Info(msg, args...)
	}
}

// logError logs at error level if the logger is non-nil.
func (m *Manager) logError(msg string, args ...any) {
	if m.logger != nil {
		m.logger.Error(msg, args...)
	}
}

// maxRestarts is the maximum number of times an adapter is restarted after crashing.
const maxRestarts = 5

// Start launches all registered adapters with automatic restart on crash.
func (m *Manager) Start(ctx context.Context) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 1. Start health checks in the background
	m.wg.Add(1)
	go m.healthCheckLoop(ctx)

	// 2. Start adapters — each runs in a supervised goroutine
	for _, a := range m.adapters {
		adapter := a
		outbound := m.outbound[adapter.Name()]

		m.wg.Add(1)
		go m.runWithRestart(ctx, adapter, outbound)
	}
}

// RunOne starts a single adapter in the same supervised restart loop used by Start.
// Use this for adapters connected at runtime after the manager has already started.
func (m *Manager) RunOne(ctx context.Context, a Adapter) {
	m.mu.RLock()
	outbound, ok := m.outbound[a.Name()]
	m.mu.RUnlock()
	if !ok {
		m.logError("cannot run adapter: adapter is not registered", "adapter", a.Name())
		return
	}

	m.wg.Add(1)
	go m.runWithRestart(ctx, a, outbound)
}

// runWithRestart starts an adapter and restarts it on crash with exponential backoff.
// It gives up after maxRestarts consecutive failures and resets the counter when
// the adapter runs successfully for at least 60 seconds.
func (m *Manager) runWithRestart(ctx context.Context, adapter Adapter, outbound <-chan OutboundMessage) {
	defer m.wg.Done()

	backoff := time.Second
	restarts := 0

	for {
		if ctx.Err() != nil {
			return // context cancelled — normal shutdown
		}

		if restarts > 0 {
			m.updateStatus(adapter.Name(), func(s *AdapterStatus) {
				s.RestartCount++
			})
			m.logInfo("restarting adapter",
				"adapter", adapter.Name(),
				"attempt", restarts,
				"backoff", backoff,
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 16*time.Second {
				backoff *= 2
			}
		}

		start := time.Now()
		m.updateStatus(adapter.Name(), func(s *AdapterStatus) {
			s.Running = true
			s.LastStart = start
		})
		m.logInfo("starting adapter", "adapter", adapter.Name())

		err := adapter.Start(ctx, m.inbound, outbound)

		// Adapter exited cleanly via context cancellation
		if ctx.Err() != nil {
			return
		}

		if err != nil {
			// If the adapter lived for > 60 seconds before crashing, reset the counter
			if time.Since(start) > 60*time.Second {
				restarts = 0
				backoff = time.Second
			}

			restarts++
			m.updateStatus(adapter.Name(), func(s *AdapterStatus) {
				s.Running = false
				s.LastError = err.Error()
				s.ConsecutiveFailures = restarts
			})
			if restarts > maxRestarts {
				m.logError("adapter permanently failed — giving up",
					"adapter", adapter.Name(),
					"restarts", restarts,
					"last_error", err,
				)
				return
			}
			m.logError("adapter exited with error — will retry",
				"adapter", adapter.Name(),
				"error", err,
				"restart_in", backoff,
			)
		} else {
			// Clean exit (no error, no ctx cancel) — don't restart
			m.updateStatus(adapter.Name(), func(s *AdapterStatus) {
				s.Running = false
				s.ConsecutiveFailures = 0
				s.LastError = ""
			})
			m.logInfo("adapter exited cleanly", "adapter", adapter.Name())
			return
		}
	}
}

// Stop signals all adapters to shut down and waits for them to finish.
func (m *Manager) Stop() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, a := range m.adapters {
		a.Stop()
	}
	m.wg.Wait()
	m.logInfo("all adapters stopped")
}

// healthCheckLoop periodically checks the health of all registered adapters.
func (m *Manager) healthCheckLoop(ctx context.Context) {
	defer m.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.RLock()
			adapters := append([]Adapter(nil), m.adapters...)
			m.mu.RUnlock()
			for _, a := range adapters {
				if err := a.Health(); err != nil {
					m.updateStatus(a.Name(), func(s *AdapterStatus) {
						s.Healthy = false
						s.LastHealthCheck = time.Now().UTC()
						s.LastHealthError = err.Error()
					})
					m.logError("adapter unhealthy", "adapter", a.Name(), "error", err)
					a.Stop()
				} else {
					m.updateStatus(a.Name(), func(s *AdapterStatus) {
						s.Healthy = true
						s.LastHealthCheck = time.Now().UTC()
						s.LastHealthError = ""
					})
				}
			}
		}
	}
}

// Statuses returns a snapshot of current adapter runtime status.
func (m *Manager) Statuses() []AdapterStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]AdapterStatus, 0, len(m.stats))
	for _, s := range m.stats {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *Manager) updateStatus(name string, fn func(*AdapterStatus)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.stats[name]
	if !ok {
		s = &AdapterStatus{Name: name}
		m.stats[name] = s
	}
	fn(s)
}

// AdapterByName returns a registered adapter by name, or nil when not found.
func (m *Manager) AdapterByName(name string) Adapter {
	needle := name
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, a := range m.adapters {
		if a.Name() == needle {
			return a
		}
	}
	return nil
}
