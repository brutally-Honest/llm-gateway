//go:build linux

package effects

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// dirIdentity names the directory at dir by walking its absolute path from "/" one
// component at a time with O_NOFOLLOW, so a symlink in any component, a parent folder
// included, is refused (reported as fs.ErrNotExist) rather than followed. Roots are
// symlink-free paths (resolve), so a link anywhere on the path means a folder was swapped
// (`mv /home/u/x /home/u/y; ln -s /home/u /home/u/x`). The result is the identity of the
// final directory, read by fstat on the open descriptor.
func dirIdentity(dir string) (os.FileInfo, error) {
	gone := &fs.PathError{Op: "open", Path: dir, Err: fs.ErrNotExist}
	if !filepath.IsAbs(dir) {
		return os.Lstat(dir)
	}
	const flags = syscall.O_RDONLY | syscall.O_DIRECTORY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	fd, err := retryEINTR(func() (int, error) { return syscall.Open("/", flags, 0) })
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: dir, Err: err}
	}
	for _, comp := range strings.Split(filepath.Clean(dir), string(filepath.Separator)) {
		if comp == "" {
			continue
		}
		next, err := retryEINTR(func() (int, error) { return syscall.Openat(fd, comp, flags, 0) })
		_ = syscall.Close(fd)
		if err != nil {
			if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENOTDIR) {
				return nil, gone
			}
			return nil, &fs.PathError{Op: "open", Path: dir, Err: err}
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), dir)
	defer func() { _ = f.Close() }()
	return f.Stat()
}

func retryEINTR(fn func() (int, error)) (int, error) {
	for {
		fd, err := fn()
		if !errors.Is(err, syscall.EINTR) {
			return fd, err
		}
	}
}
