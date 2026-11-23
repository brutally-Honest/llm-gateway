//go:build darwin || freebsd || netbsd

package effects

import (
	"os"
	"syscall"
)

// oNonblock keeps an open of a swapped-in FIFO from blocking.
const oNonblock = syscall.O_NONBLOCK

// statIDs are the inode and ctime of fi; ok is false when the platform has none.
func statIDs(fi os.FileInfo) (ino uint64, ctimeSec, ctimeNsec int64, ok bool) {
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, 0, 0, false
	}
	return uint64(st.Ino), int64(st.Ctimespec.Sec), int64(st.Ctimespec.Nsec), true
}
