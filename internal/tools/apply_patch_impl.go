package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func init() {
	_ = ReplaceTool("apply_patch", applyPatchTool)
}

type patchManifestEntry struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
}

func applyPatchTool(ctx context.Context, payload map[string]any) (any, error) {
	// support revert operation
	if rid, ok := payload["revert"]; ok {
		backupID, _ := rid.(string)
		return nil, revertBackup(backupID, payload)
	}

	root := ""
	if rp, ok := payload["repo_path"].(string); ok && strings.TrimSpace(rp) != "" {
		root = rp
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		root = wd
	}
	root, _ = filepath.Abs(root)

	changesRaw, ok := payload["changes"]
	if !ok {
		return nil, fmt.Errorf("changes is required")
	}
	changesArr, ok := changesRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("changes must be an array")
	}

	dry := false
	if d, ok := payload["dry_run"]; ok {
		if b, ok := d.(bool); ok {
			dry = b
		}
	}

	type planned struct {
		Path       string
		AbsPath    string
		Before     string
		WillCreate bool
		After      string
	}
	var plan []planned

	for _, it := range changesArr {
		m, ok := it.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid change item")
		}
		pthI, ok := m["path"]
		if !ok {
			return nil, fmt.Errorf("change missing path")
		}
		pth, _ := pthI.(string)
		if strings.TrimSpace(pth) == "" {
			return nil, fmt.Errorf("empty path")
		}
		abs := filepath.Join(root, filepath.Clean(pth))
		abs, _ = filepath.Abs(abs)
		if !strings.HasPrefix(abs, root+string(os.PathSeparator)) && abs != root {
			return nil, fmt.Errorf("path %q escapes repository root", pth)
		}
		after, _ := m["content"].(string)
		existed := true
		before := ""
		if b, err := os.ReadFile(abs); err == nil {
			before = string(b)
		} else if os.IsNotExist(err) {
			existed = false
		} else {
			return nil, fmt.Errorf("reading %s: %w", abs, err)
		}
		plan = append(plan, planned{
			Path:       pth,
			AbsPath:    abs,
			Before:     before,
			WillCreate: !existed,
			After:      after,
		})
	}

	// dry run: return plan summary
	if dry {
		out := make([]map[string]any, 0, len(plan))
		for _, p := range plan {
			out = append(out, map[string]any{
				"path":           p.Path,
				"exists":         !p.WillCreate,
				"before_snippet": snippet(p.Before),
				"after_snippet":  snippet(p.After),
			})
		}
		return map[string]any{"dry_run": true, "plan": out}, nil
	}

	// perform apply with backup
	backupID := fmt.Sprintf("%d", time.Now().UnixNano())
	backupDir := filepath.Join(root, ".openclio", "patch_backups", backupID)
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return nil, fmt.Errorf("creating backup dir: %w", err)
	}
	manifest := make([]patchManifestEntry, 0, len(plan))
	// create backups and apply writes
	for _, p := range plan {
		fullBackupPath := filepath.Join(backupDir, filepath.FromSlash(p.Path))
		if err := os.MkdirAll(filepath.Dir(fullBackupPath), 0700); err != nil {
			_ = revertBackupInternal(root, backupDir, manifest)
			return nil, fmt.Errorf("creating backup parent: %w", err)
		}
		if !p.WillCreate {
			if err := copyFile(p.AbsPath, fullBackupPath); err != nil {
				_ = revertBackupInternal(root, backupDir, manifest)
				return nil, fmt.Errorf("backing up %s: %w", p.Path, err)
			}
		}
		manifest = append(manifest, patchManifestEntry{Path: p.Path, Existed: !p.WillCreate})
		// ensure parent dir
		if err := os.MkdirAll(filepath.Dir(p.AbsPath), 0755); err != nil {
			_ = revertBackupInternal(root, backupDir, manifest)
			return nil, fmt.Errorf("creating parent dir: %w", err)
		}
		// write new content atomically
		tmpf := p.AbsPath + ".tmp"
		if err := os.WriteFile(tmpf, []byte(p.After), 0644); err != nil {
			_ = revertBackupInternal(root, backupDir, manifest)
			return nil, fmt.Errorf("writing temp file: %w", err)
		}
		if err := os.Rename(tmpf, p.AbsPath); err != nil {
			_ = revertBackupInternal(root, backupDir, manifest)
			return nil, fmt.Errorf("rename temp: %w", err)
		}
	}

	// write manifest
	manifestPath := filepath.Join(backupDir, "manifest.json")
	mb, _ := json.Marshal(manifest)
	_ = os.WriteFile(manifestPath, mb, 0600)

	return map[string]any{"ok": true, "backup_id": backupID, "changed": len(plan)}, nil
}

func snippet(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func revertBackup(backupID string, payload map[string]any) error {
	root := ""
	if rp, ok := payload["repo_path"].(string); ok && strings.TrimSpace(rp) != "" {
		root = rp
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		root = wd
	}
	root, _ = filepath.Abs(root)
	backupDir := filepath.Join(root, ".openclio", "patch_backups", backupID)
	manifestPath := filepath.Join(backupDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("reading manifest: %w", err)
	}
	var manifest []patchManifestEntry
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("invalid manifest: %w", err)
	}
	return revertBackupInternal(root, backupDir, manifest)
}

func revertBackupInternal(root, backupDir string, manifest []patchManifestEntry) error {
	for _, e := range manifest {
		backupPath := filepath.Join(backupDir, filepath.FromSlash(e.Path))
		targetPath := filepath.Join(root, filepath.FromSlash(e.Path))
		if e.Existed {
			// restore
			if err := copyFile(backupPath, targetPath); err != nil {
				return fmt.Errorf("restoring %s: %w", e.Path, err)
			}
		} else {
			// delete created file
			if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing created file %s: %w", e.Path, err)
			}
		}
	}
	return nil
}
