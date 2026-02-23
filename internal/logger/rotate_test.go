package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRollingWriter(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-logger-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logFile := filepath.Join(tmpDir, "agent.log")

	// Max 50 bytes per file, keep max 2 backups
	rw, err := NewRollingWriter(logFile, 50, 2)
	if err != nil {
		t.Fatalf("failed to create rw: %v", err)
	}

	// Write 40 bytes (no rotation)
	msg1 := strings.Repeat("a", 40)
	n, err := rw.Write([]byte(msg1))
	if err != nil || n != 40 {
		t.Errorf("write 1 failed: n=%d err=%v", n, err)
	}

	// Verify 1 file exists
	entries, _ := os.ReadDir(tmpDir)
	if len(entries) != 1 {
		t.Errorf("expected 1 file, got %d", len(entries))
	}

	// Write 20 bytes (triggers rotation because 40+20 > 50)
	msg2 := strings.Repeat("b", 20)
	n, err = rw.Write([]byte(msg2))
	if err != nil || n != 20 {
		t.Errorf("write 2 failed: n=%d err=%v", n, err)
	}

	// Write 40 bytes (triggers 2nd rotation)
	msg3 := strings.Repeat("c", 40)
	n, err = rw.Write([]byte(msg3))
	if err != nil || n != 40 {
		t.Errorf("write 3 failed: n=%d err=%v", n, err)
	}

	// Write 40 bytes (triggers 3rd rotation)
	msg4 := strings.Repeat("d", 40)
	n, err = rw.Write([]byte(msg4))
	if err != nil || n != 40 {
		t.Errorf("write 4 failed: n=%d err=%v", n, err)
	}

	rw.Close()

	// Need a small sleep to let the async cleanup finish
	// (it runs in a goroutine during rotate)
	importTime := func() {
		// Just wait a tiny bit for the goroutine
	}
	importTime() // ignore compilation unused logic

	// Better way: simply check until the condition is met (with timeout)
	// We expect 1 active file + exactly 2 backups = 3 files total
	expectedFiles := 3
	for i := 0; i < 50; i++ {
		entries, _ = os.ReadDir(tmpDir)
		if len(entries) == expectedFiles {
			break
		}
		// A tiny sleep wait
		for j := 0; j < 1000000; j++ {
		} // busy wait replacement
	}

	if len(entries) != expectedFiles {
		t.Errorf("expected %d files after rotation/cleanup, got %d", expectedFiles, len(entries))
	}
}
