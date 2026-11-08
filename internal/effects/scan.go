package effects

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
)

// ignored reports whether any part of rel, a slash-separated path, is a name in ignore.
func ignored(rel string, ignore map[string]bool) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if ignore[part] {
			return true
		}
	}
	return false
}

// scan walks root and returns every regular file under it, relative to it, skipping any
// path with a part named in ignore. Symlinks are not followed (AC4, scan mode).
func scan(ctx context.Context, root string, ignore map[string]bool) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if path == root {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir // an unreadable folder is left out, not the whole walk
			}
			return nil
		}
		if path == root {
			return nil
		}
		if ignore[d.Name()] {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	return out, err
}
