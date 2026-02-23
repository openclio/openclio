package tools

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func init() {
	_ = ReplaceTool("search_files", searchFilesTool)
	_ = ReplaceTool("move_file", moveFileTool)
	_ = ReplaceTool("delete_file", deleteFileTool)
}

func resolveRoot(payload map[string]any) (string, error) {
	if rp, ok := payload["work_dir"].(string); ok && strings.TrimSpace(rp) != "" {
		abs, err := filepath.Abs(rp)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return wd, nil
}

func searchFilesTool(ctx context.Context, payload map[string]any) (any, error) {
	root, err := resolveRoot(payload)
	if err != nil {
		return nil, err
	}
	pattern, _ := payload["pattern"].(string)
	if strings.TrimSpace(pattern) == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	limit := 100
	if l, ok := payload["limit"].(float64); ok {
		limit = int(l)
	}
	// support simple substring or regex if prefixed with "re:"
	isRegex := false
	if strings.HasPrefix(pattern, "re:") {
		pattern = strings.TrimPrefix(pattern, "re:")
		isRegex = true
	}
	var re *regexp.Regexp
	if isRegex {
		r, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
		re = r
		pattern = ""
	}

	out := make([]map[string]any, 0, 64)
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// skip unreadable
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		match := false
		if isRegex {
			match = re.MatchString(rel)
		} else {
			match = strings.Contains(rel, pattern)
		}
		if match {
			info, _ := d.Info()
			out = append(out, map[string]any{
				"path":        rel,
				"size":        info.Size(),
				"modified_at": info.ModTime().UTC().Format(time.RFC3339),
			})
			if len(out) >= limit {
				return fmt.Errorf("limit_reached")
			}
		}
		return nil
	})
	if err != nil && err.Error() != "limit_reached" {
		return nil, err
	}
	return out, nil
}

func moveFileTool(ctx context.Context, payload map[string]any) (any, error) {
	root, err := resolveRoot(payload)
	if err != nil {
		return nil, err
	}
	srcI, ok := payload["src"]
	if !ok {
		return nil, fmt.Errorf("src is required")
	}
	dstI, ok := payload["dst"]
	if !ok {
		return nil, fmt.Errorf("dst is required")
	}
	src := filepath.Join(root, filepath.Clean(srcI.(string)))
	dst := filepath.Join(root, filepath.Clean(dstI.(string)))
	absSrc, _ := filepath.Abs(src)
	absDst, _ := filepath.Abs(dst)
	if !strings.HasPrefix(absSrc, root) || !strings.HasPrefix(absDst, root) {
		return nil, fmt.Errorf("src or dst escapes work_dir")
	}
	overwrite := false
	if o, ok := payload["overwrite"]; ok {
		if b, ok := o.(bool); ok {
			overwrite = b
		}
	}
	if !overwrite {
		if _, err := os.Stat(absDst); err == nil {
			return nil, fmt.Errorf("destination exists; set overwrite=true to replace")
		}
	}
	if err := os.MkdirAll(filepath.Dir(absDst), 0755); err != nil {
		return nil, err
	}
	if err := os.Rename(absSrc, absDst); err != nil {
		// fallback to copy+remove
		in, err := os.Open(absSrc)
		if err != nil {
			return nil, err
		}
		defer in.Close()
		out, err := os.Create(absDst)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return nil, err
		}
		out.Close()
		if err := os.Remove(absSrc); err != nil {
			return nil, err
		}
	}
	return map[string]any{"moved": true, "dst": strings.TrimPrefix(absDst, root+string(os.PathSeparator))}, nil
}

func deleteFileTool(ctx context.Context, payload map[string]any) (any, error) {
	root, err := resolveRoot(payload)
	if err != nil {
		return nil, err
	}
	pathI, ok := payload["path"]
	if !ok {
		return nil, fmt.Errorf("path is required")
	}
	rel := filepath.Clean(pathI.(string))
	abs := filepath.Join(root, rel)
	abs, _ = filepath.Abs(abs)
	if !strings.HasPrefix(abs, root) {
		return nil, fmt.Errorf("path escapes work_dir")
	}
	force := false
	if f, ok := payload["force"]; ok {
		if b, ok := f.(bool); ok {
			force = b
		}
	}
	if !force {
		return nil, fmt.Errorf("delete_file requires force=true to confirm deletion")
	}
	if err := os.RemoveAll(abs); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": rel}, nil
}
