package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RollingWriter is an io.Writer that rotates log files when they exceed a maximum size.
// It retains up to maxFiles older logs.
type RollingWriter struct {
	mu       sync.Mutex
	filename string
	maxBytes int64
	maxFiles int

	file *os.File
	size int64
}

// NewRollingWriter creates a new rolling file writer.
// If maxBytes is 0, defaults to 10MB. If maxFiles is 0, defaults to 5.
func NewRollingWriter(filename string, maxBytes int64, maxFiles int) (*RollingWriter, error) {
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024 // 10MB default
	}
	if maxFiles <= 0 {
		maxFiles = 5 // 5 backups default
	}

	w := &RollingWriter{
		filename: filename,
		maxBytes: maxBytes,
		maxFiles: maxFiles,
	}

	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RollingWriter) open() error {
	info, err := os.Stat(w.filename)
	if err == nil {
		w.size = info.Size()
	}

	f, err := os.OpenFile(w.filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	w.file = f
	return nil
}

// Write implements io.Writer.
func (w *RollingWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	writeLen := int64(len(p))
	if w.size+writeLen > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}

	n, err = w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// Close closes the underlying file.
func (w *RollingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

func (w *RollingWriter) rotate() error {
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}

	dir := filepath.Dir(w.filename)
	base := filepath.Base(w.filename)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)

	// Backup current file: name-20060102-150405.000000000.log
	timestamp := time.Now().Format("20060102-150405.000000000")
	backupName := fmt.Sprintf("%s-%s%s", name, timestamp, ext)
	backupPath := filepath.Join(dir, backupName)

	if err := os.Rename(w.filename, backupPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rotating log file: %w", err)
	}

	if err := w.open(); err != nil {
		return err
	}

	go w.cleanup()

	return nil
}

func (w *RollingWriter) cleanup() {
	dir := filepath.Dir(w.filename)
	base := filepath.Base(w.filename)
	ext := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, ext) + "-"

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var backups []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ext) {
			backups = append(backups, filepath.Join(dir, e.Name()))
		}
	}

	if len(backups) <= w.maxFiles {
		return
	}

	// Simple string sort assuming timestamps are monotonically increasing lexicographically
	sort.Strings(backups)

	// Keep the last 'maxFiles', delete the rest
	removeCount := len(backups) - w.maxFiles
	for i := 0; i < removeCount; i++ {
		os.Remove(backups[i])
	}
}
