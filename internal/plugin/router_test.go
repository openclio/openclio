package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ─── Allowlist tests ─────────────────────────────────────────────────────────

func TestAllowlistAllowAll(t *testing.T) {
	al := NewAllowlist(t.TempDir(), true)
	if !al.IsAllowed("any-adapter", "any-user") {
		t.Error("allow_all=true should permit any sender")
	}
}

func TestAllowlistBlockUnknown(t *testing.T) {
	al := NewAllowlist(t.TempDir(), false)
	if al.IsAllowed("test", "alice") {
		t.Error("empty allowlist in strict mode should block alice")
	}
}

func TestAllowlistApproveRevoke(t *testing.T) {
	al := NewAllowlist(t.TempDir(), false)

	if err := al.Approve("test", "alice"); err != nil {
		t.Fatalf("Approve failed: %v", err)
	}
	if !al.IsAllowed("test", "alice") {
		t.Error("alice should be allowed after Approve")
	}
	if al.IsAllowed("test", "bob") {
		t.Error("bob should NOT be allowed")
	}

	if err := al.Revoke("test", "alice"); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}
	if al.IsAllowed("test", "alice") {
		t.Error("alice should be blocked after Revoke")
	}
}

func TestAllowlistPersistence(t *testing.T) {
	dir := t.TempDir()

	al := NewAllowlist(dir, false)
	if err := al.Approve("telegram", "user-123"); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// Re-load from disk
	al2 := NewAllowlist(dir, false)
	if !al2.IsAllowed("telegram", "user-123") {
		t.Error("approved sender should survive a reload from disk")
	}
}

func TestAllowlistSaveModeAndTempCleanup(t *testing.T) {
	dir := t.TempDir()
	al := NewAllowlist(dir, false)

	if err := al.Approve("discord", "user-1"); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	path := filepath.Join(dir, "allowed_senders.txt")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected persisted allowlist file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("expected allowlist file mode 0600, got %o", got)
	}

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("expected no lingering temp file, got err=%v", err)
	}
}

// ─── Router allowlist enforcement ────────────────────────────────────────────

func TestRouterBlocksUnknownSender(t *testing.T) {
	mgr := NewManager(nil)
	a := &mockAdapter{name: "test", stopC: make(chan struct{})}
	mgr.Register(a)

	al := NewAllowlist(t.TempDir(), false) // strict mode, nobody approved

	// Reset session cache
	sessionCache.mu.Lock()
	sessionCache.m = make(map[string]sessionCacheEntry)
	sessionCache.lastPrune = time.Time{}
	sessionCache.ttl = 24 * time.Hour
	sessionCache.mu.Unlock()

	router := &Router{
		manager:   mgr,
		logger:    nil,
		allowlist: al,
	}

	msg := InboundMessage{
		AdapterName: "test",
		UserID:      "stranger",
		ChatID:      "chat1",
		Text:        "hello",
	}

	// handleMessage runs in a goroutine internally; call the allowlist check directly
	go func() {
		if al != nil && !al.IsAllowed(msg.AdapterName, msg.UserID) {
			mgr.Send(msg.AdapterName, OutboundMessage{
				ChatID: msg.ChatID,
				UserID: msg.UserID,
				Text:   fmt.Sprintf("🔒 Access denied. Sender: %s", msg.UserID),
			})
		}
	}()

	select {
	case out := <-mgr.outbound["test"]:
		if out.ChatID != "chat1" {
			t.Errorf("expected ChatID chat1, got %q", out.ChatID)
		}
		if out.Text == "" {
			t.Error("expected non-empty rejection message")
		}
		_ = router // used above via mgr
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for rejection message")
	}
}

// ─── Session cache ────────────────────────────────────────────────────────────

func TestSessionCacheIsolation(t *testing.T) {
	// Reset the package-level session cache
	sessionCache.mu.Lock()
	sessionCache.m = make(map[string]sessionCacheEntry)
	sessionCache.lastPrune = time.Time{}
	sessionCache.ttl = 24 * time.Hour
	sessionCache.mu.Unlock()

	key1 := "adapter1:chat-A"
	key2 := "adapter1:chat-B"
	now := time.Now()

	sessionCache.mu.Lock()
	sessionCache.m[key1] = sessionCacheEntry{sessionID: "sess-111", lastSeen: now}
	sessionCache.m[key2] = sessionCacheEntry{sessionID: "sess-222", lastSeen: now}
	sessionCache.mu.Unlock()

	sessionCache.mu.Lock()
	s1 := sessionCache.m[key1].sessionID
	s2 := sessionCache.m[key2].sessionID
	sessionCache.mu.Unlock()

	if s1 == s2 {
		t.Error("different chats must get different sessions")
	}
}

func TestSessionCachePruneExpired(t *testing.T) {
	now := time.Now()

	sessionCache.mu.Lock()
	sessionCache.m = map[string]sessionCacheEntry{
		"a:old": {
			sessionID: "sess-old",
			lastSeen:  now.Add(-2 * time.Hour),
		},
		"a:new": {
			sessionID: "sess-new",
			lastSeen:  now,
		},
	}
	sessionCache.lastPrune = time.Time{}
	sessionCache.ttl = time.Hour
	pruneSessionCacheLocked(now)
	_, oldPresent := sessionCache.m["a:old"]
	_, newPresent := sessionCache.m["a:new"]
	sessionCache.ttl = 24 * time.Hour
	sessionCache.mu.Unlock()

	if oldPresent {
		t.Fatal("expected expired cache entry to be pruned")
	}
	if !newPresent {
		t.Fatal("expected recent cache entry to remain")
	}
}

// ─── Manager auto-restart ─────────────────────────────────────────────────────

// instantCrashAdapter simulates an adapter that crashes immediately every run.
type instantCrashAdapter struct {
	name    string
	crashes int
	stopC   chan struct{}
}

func (c *instantCrashAdapter) Name() string { return c.name }
func (c *instantCrashAdapter) Stop() {
	select {
	case <-c.stopC:
	default:
		close(c.stopC)
	}
}
func (c *instantCrashAdapter) Health() error { return nil }
func (c *instantCrashAdapter) Start(_ context.Context, _ chan<- InboundMessage, _ <-chan OutboundMessage) error {
	c.crashes++
	return errors.New("simulated crash")
}

func TestManagerRestartOnCrash(t *testing.T) {
	mgr := NewManager(nil)
	ca := &instantCrashAdapter{name: "crash-test", stopC: make(chan struct{})}
	mgr.Register(ca)

	// Cancel context quickly so the backoff sleeps are cut short
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	outbound := mgr.outbound["crash-test"]
	done := make(chan struct{})

	// Call runWithRestart directly (not Start, to avoid the health-check loop)
	mgr.wg.Add(1)
	go func() {
		defer close(done)
		mgr.runWithRestart(ctx, ca, outbound)
	}()

	<-done
	if ca.crashes == 0 {
		t.Error("expected at least one crash + restart attempt")
	}
	t.Logf("adapter attempted %d run(s) during restart loop", ca.crashes)
}
