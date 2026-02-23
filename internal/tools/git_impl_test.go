package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/openclio/openclio/internal/storage"
)

func TestGitToolsLifecycle(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.Mkdir(repo, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cmd := exec.Command("git", "init")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v (%s)", err, out)
	}
	// configure local user for commits
	if err := exec.Command("git", "-C", repo, "config", "user.email", "test@example.com").Run(); err != nil {
		t.Fatalf("git config email failed: %v", err)
	}
	if err := exec.Command("git", "-C", repo, "config", "user.name", "Test User").Run(); err != nil {
		t.Fatalf("git config name failed: %v", err)
	}

	// create file
	file := filepath.Join(repo, "README.md")
	if err := os.WriteFile(file, []byte("# hi\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// status
	_, err := CallTool(ctx, "git_status", map[string]any{"repo_path": repo})
	if err != nil {
		t.Fatalf("git_status failed: %v", err)
	}

	// commit
	_, err = CallTool(ctx, "git_commit", map[string]any{"repo_path": repo, "files": []any{"README.md"}, "message": "initial"})
	if err != nil {
		t.Fatalf("git_commit failed: %v", err)
	}

	// log
	_, err = CallTool(ctx, "git_log", map[string]any{"repo_path": repo, "n": 5})
	if err != nil {
		t.Fatalf("git_log failed: %v", err)
	}

	// branch
	_, err = CallTool(ctx, "git_branch", map[string]any{"repo_path": repo})
	if err != nil {
		t.Fatalf("git_branch failed: %v", err)
	}

	// diff (no changes)
	_, err = CallTool(ctx, "git_diff", map[string]any{"repo_path": repo})
	if err != nil {
		t.Fatalf("git_diff failed: %v", err)
	}

	// ensure DB migrations don't interfere
	dbPath := filepath.Join(tmp, ".openclio", "data.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	db.Close()
}
