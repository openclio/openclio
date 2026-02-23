package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseOpenClawJSONL(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "session.jsonl")
	content := `{"role":"user","content":"hello"}
{"type":"assistant","text":"hi there"}
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	msgs, err := parseOpenClawJSONL(path)
	if err != nil {
		t.Fatalf("parseOpenClawJSONL: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Fatalf("unexpected first message: %#v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "hi there" {
		t.Fatalf("unexpected second message: %#v", msgs[1])
	}
}

func TestImportIdentityFileFallback(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	if err := os.WriteFile(filepath.Join(src, "IDENTITY.md"), []byte("legacy identity"), 0600); err != nil {
		t.Fatalf("write source identity: %v", err)
	}

	ok, err := importIdentityFile(src, dst)
	if err != nil {
		t.Fatalf("importIdentityFile: %v", err)
	}
	if !ok {
		t.Fatal("expected identity import to succeed")
	}

	data, err := os.ReadFile(filepath.Join(dst, "identity.md"))
	if err != nil {
		t.Fatalf("read imported identity: %v", err)
	}
	if string(data) != "legacy identity" {
		t.Fatalf("unexpected identity content: %q", string(data))
	}
}
