package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/openclio/openclio/internal/storage"
)

func setupCLIHistoryUndoDB(t *testing.T) *storage.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "history-undo.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	return db
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy stdout: %v", err)
	}
	_ = r.Close()
	return buf.String()
}

func TestRunHistoryPrintsEntries(t *testing.T) {
	db := setupCLIHistoryUndoDB(t)
	defer db.Close()

	logStore := storage.NewActionLogStore(db)
	if err := logStore.RecordWriteFile("/tmp/demo.txt", true, "before", "after", true, ""); err != nil {
		t.Fatalf("RecordWriteFile: %v", err)
	}
	if err := logStore.RecordExec("echo hello", "/tmp", "hello\n", true, ""); err != nil {
		t.Fatalf("RecordExec: %v", err)
	}

	out := captureStdout(t, func() {
		runHistory(db, []string{"--last", "5"})
	})

	if !strings.Contains(out, "TOOL") || !strings.Contains(out, "write_file") || !strings.Contains(out, "exec") {
		t.Fatalf("history output missing expected rows:\n%s", out)
	}
	if !strings.Contains(out, "YES") {
		t.Fatalf("history output should show undoable write action:\n%s", out)
	}
}

func TestRunUndoRestoresOverwrittenFile(t *testing.T) {
	db := setupCLIHistoryUndoDB(t)
	defer db.Close()

	target := filepath.Join(t.TempDir(), "restore.txt")
	if err := os.WriteFile(target, []byte("new content"), 0644); err != nil {
		t.Fatalf("write target file: %v", err)
	}

	logStore := storage.NewActionLogStore(db)
	if err := logStore.RecordWriteFile(target, true, "old content", "new content", true, ""); err != nil {
		t.Fatalf("RecordWriteFile: %v", err)
	}
	entries, err := logStore.List(1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("List: err=%v len=%d", err, len(entries))
	}

	_ = captureStdout(t, func() {
		runUndo(db, []string{strconv.FormatInt(entries[0].ID, 10)})
	})

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(data) != "old content" {
		t.Fatalf("expected restored content, got %q", string(data))
	}
}

func TestRunUndoRemovesNewFile(t *testing.T) {
	db := setupCLIHistoryUndoDB(t)
	defer db.Close()

	target := filepath.Join(t.TempDir(), "created.txt")
	if err := os.WriteFile(target, []byte("created content"), 0644); err != nil {
		t.Fatalf("write target file: %v", err)
	}

	logStore := storage.NewActionLogStore(db)
	if err := logStore.RecordWriteFile(target, false, "", "created content", true, ""); err != nil {
		t.Fatalf("RecordWriteFile: %v", err)
	}
	entries, err := logStore.List(1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("List: err=%v len=%d", err, len(entries))
	}

	_ = captureStdout(t, func() {
		runUndo(db, []string{strconv.FormatInt(entries[0].ID, 10)})
	})

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected file to be removed, stat err=%v", err)
	}
}
