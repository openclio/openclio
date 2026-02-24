package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveLocalPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}

	got := resolveLocalPath("~/wa-store")
	want := filepath.Join(home, "wa-store")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}

	got = resolveLocalPath("~")
	if got != home {
		t.Fatalf("expected %q, got %q", home, got)
	}

	abs := "/tmp/openclio-wa"
	got = resolveLocalPath(abs)
	if got != abs {
		t.Fatalf("expected %q, got %q", abs, got)
	}

	spaced := "  ~/foo  "
	got = resolveLocalPath(spaced)
	if !strings.HasSuffix(got, string(filepath.Separator)+"foo") {
		t.Fatalf("expected resolved path ending in /foo, got %q", got)
	}
}
