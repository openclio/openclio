package tools

import (
	"sync"
	"sync/atomic"
)

var (
	toolCallCounts   = make(map[string]*int64)
	toolCallCountsMu sync.Mutex
	redactionCount   int64
)

func IncToolCall(name string) {
	toolCallCountsMu.Lock()
	ptr, ok := toolCallCounts[name]
	if !ok {
		var v int64
		ptr = &v
		toolCallCounts[name] = ptr
	}
	toolCallCountsMu.Unlock()
	atomic.AddInt64(ptr, 1)
}

func IncRedactions(n int) {
	atomic.AddInt64(&redactionCount, int64(n))
}

func SnapshotMetrics() (map[string]int64, int64) {
	toolCallCountsMu.Lock()
	defer toolCallCountsMu.Unlock()
	out := make(map[string]int64, len(toolCallCounts))
	for k, p := range toolCallCounts {
		out[k] = atomic.LoadInt64(p)
	}
	return out, atomic.LoadInt64(&redactionCount)
}
