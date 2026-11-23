//go:build darwin

package effects

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// dirIdentity names the directory at dir. The darwin syscall package has no openat, so
// the walk cannot be descriptor-relative: every prefix of the path is Lstat'ed (a symlink
// or non-directory means gone), the final directory is opened with O_NOFOLLOW and
// fstat'ed, then every prefix is checked again. A parent swapped for a link in between
// is caught unless it is swapped back in the same window; openDirRoot's SameFile check
// on the opened root narrows the rest.
func dirIdentity(dir string) (os.FileInfo, error) {
	gone := &fs.PathError{Op: "open", Path: dir, Err: fs.ErrNotExist}
	if !filepath.IsAbs(dir) {
		return os.Lstat(dir)
	}
	clean := filepath.Clean(dir)
	check := func() error {
		cur := string(filepath.Separator)
		for _, comp := range strings.Split(clean, string(filepath.Separator)) {
			if comp == "" {
				continue
			}
			cur = filepath.Join(cur, comp)
			fi, err := os.Lstat(cur)
			if err != nil {
				return err
			}
			if !fi.IsDir() {
				return gone
			}
		}
		return nil
	}
	if err := check(); err != nil {
		return nil, err
	}
	const flags = syscall.O_RDONLY | syscall.O_DIRECTORY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	var fd int
	for {
		var err error
		fd, err = syscall.Open(clean, flags, 0)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			if err == syscall.ELOOP || err == syscall.ENOTDIR {
				return nil, gone
			}
			return nil, &fs.PathError{Op: "open", Path: dir, Err: err}
		}
		break
	}
	f := os.NewFile(uintptr(fd), dir)
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := check(); err != nil {
		return nil, err
	}
	return fi, nil
}
