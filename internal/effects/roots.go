package effects

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// within reports whether path is dir or under it. Both are clean and absolute.
func within(path, dir string) bool {
	if dir == string(filepath.Separator) {
		return true
	}
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// defaultTemps are the temp folders never watched.
func defaultTemps() []string {
	temps := []string{filepath.Clean(os.TempDir()), "/tmp", "/var/tmp"}
	if runtime.GOOS == "darwin" {
		temps = append(temps, "/private/tmp", "/private/var/folders", "/var/folders")
	}
	// Their resolved spellings too, since roots are compared resolved.
	for _, t := range slices.Clone(temps) {
		if r := resolve(t); !slices.Contains(temps, r) {
			temps = append(temps, r)
		}
	}
	return temps
}

// rejectedRoot reports whether dir may never be watched (AC6, E8): /, the home folder
// or any of its ancestors, and anything under one of temps.
func rejectedRoot(dir, home string, temps []string) bool {
	if dir == string(filepath.Separator) || !filepath.IsAbs(dir) {
		return true
	}
	if home != "" && within(home, dir) {
		return true // the home folder or an ancestor of it
	}
	for _, t := range temps {
		if within(dir, t) {
			return true
		}
	}
	return false
}

// rootSet is a session's watched folders: resolved, absolute, none inside another.
type rootSet struct {
	home  string
	temps []string
	roots []string
}

func newRootSet(home string, temps []string) *rootSet {
	if home != "" {
		home = resolve(home)
	}
	return &rootSet{home: home, temps: temps}
}

// covers reports whether path, resolved, is inside a watched folder.
func (r *rootSet) covers(path string) bool {
	path = resolve(path)
	for _, root := range r.roots {
		if within(path, root) {
			return true
		}
	}
	return false
}

// add watches dir. A rejected dir, or one already covered, changes nothing. Watched
// folders inside dir are folded into it and returned, so their saved rows can go.
func (r *rootSet) add(dir string) (added bool, folded []string) {
	if dir == "" {
		return false, nil
	}
	dir = resolve(dir)
	if rejectedRoot(dir, r.home, r.temps) || r.covers(dir) {
		return false, nil
	}
	keep := r.roots[:0:0]
	for _, root := range r.roots {
		if within(root, dir) {
			folded = append(folded, root)
		} else {
			keep = append(keep, root)
		}
	}
	r.roots = append(keep, dir)
	slices.Sort(r.roots)
	return true, folded
}

// list is the watched folders, sorted.
func (r *rootSet) list() []string { return slices.Clone(r.roots) }

// resolve is path with its symlinks resolved, so one folder has one spelling: git reports
// resolved paths, and a client may send either (spec 024 AC5, macos-v1.md B1). A path that
// does not exist yet keeps its missing tail on its resolved existing parent. A relative
// path, or one with no part that resolves, comes back clean.
func resolve(path string) string {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return path
	}
	tail := ""
	for p := path; ; p = filepath.Dir(p) {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, tail)
		}
		if p == filepath.Dir(p) {
			return path
		}
		tail = filepath.Join(filepath.Base(p), tail)
	}
}
