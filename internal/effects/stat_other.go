//go:build !linux && !darwin && !freebsd && !netbsd

package effects

import "os"

// oNonblock is 0 where there is no O_NONBLOCK to pass.
const oNonblock = 0

// statIDs has no inode or ctime here: both count as matching the index, as in git on
// platforms without them.
func statIDs(os.FileInfo) (ino uint64, ctimeSec, ctimeNsec int64, ok bool) {
	return 0, 0, 0, false
}
