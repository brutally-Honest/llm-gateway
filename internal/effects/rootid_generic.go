//go:build !linux && !darwin

package effects

import "os"

// dirIdentity is Lstat where there is no openat walk or darwin's prefix check: a symlink
// at dir itself is seen, a link in a parent folder is not.
func dirIdentity(dir string) (os.FileInfo, error) {
	return os.Lstat(dir)
}
