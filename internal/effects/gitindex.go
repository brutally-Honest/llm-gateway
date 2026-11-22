package effects

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

// indexEntry is one entry of git's index file. Stat fields are zero when the entry came
// from `git ls-files` (no stat shortcut then: the file is always hashed).
type indexEntry struct {
	path         string
	mode         uint32
	oid          string // lower-case hex
	stage        uint8
	skipWorktree bool
	assumeValid  bool
	hasStat      bool
	ctimeSec     uint32
	ctimeNsec    uint32
	mtimeSec     uint32
	mtimeNsec    uint32
	size         uint32
	ino          uint32
}

// errIndexUnsupported is an index the Go reader does not handle (split index, sparse
// index, unknown version); the caller falls back to `git ls-files`.
var errIndexUnsupported = errors.New("git index not supported by the reader")

const (
	modeGitlink = 0o160000
	modeTree    = 0o040000

	// maxPathExpansion bounds the bytes of all version 4 paths to this many times the size
	// of the index file (a path is at most 4096 bytes and an entry at least 63).
	maxPathExpansion = 64
)

// readIndex parses the git index file in data (formats 2, 3 and 4, see gitformat-index)
// whose object ids are hashLen bytes long. It stops at the extensions. It never reads
// outside data: a truncated or corrupt index is an error.
func readIndex(data []byte, hashLen int) ([]indexEntry, error) {
	if hashLen != 20 && hashLen != 32 {
		return nil, fmt.Errorf("index: bad hash length %d", hashLen)
	}
	if len(data) < 12 || string(data[:4]) != "DIRC" {
		return nil, errors.New("index: bad header")
	}
	version := binary.BigEndian.Uint32(data[4:8])
	if version < 2 || version > 4 {
		return nil, fmt.Errorf("%w: version %d", errIndexUnsupported, version)
	}
	count := binary.BigEndian.Uint32(data[8:12])
	// An entry is at least 40 bytes of stat, the id, 2 flag bytes and a path byte.
	if uint64(count)*uint64(40+hashLen+3) > uint64(len(data)) {
		return nil, errors.New("index: entry count exceeds file size")
	}
	pos := 12
	var prev []byte
	// A version 4 path is rebuilt from the previous one, so a few small entries after one
	// long path would expand quadratically. Real indexes stay far below this budget.
	pathBudget := uint64(len(data)) * maxPathExpansion
	out := make([]indexEntry, 0, count)
	for i := uint32(0); i < count; i++ {
		start := pos
		if len(data)-pos < 40+hashLen+2 {
			return nil, errors.New("index: truncated entry")
		}
		b := data[pos:]
		e := indexEntry{
			hasStat:   true,
			ctimeSec:  binary.BigEndian.Uint32(b[0:4]),
			ctimeNsec: binary.BigEndian.Uint32(b[4:8]),
			mtimeSec:  binary.BigEndian.Uint32(b[8:12]),
			mtimeNsec: binary.BigEndian.Uint32(b[12:16]),
			ino:       binary.BigEndian.Uint32(b[20:24]),
			mode:      binary.BigEndian.Uint32(b[24:28]),
			size:      binary.BigEndian.Uint32(b[36:40]),
		}
		e.oid = hex.EncodeToString(b[40 : 40+hashLen])
		flags := binary.BigEndian.Uint16(b[40+hashLen:])
		pos += 40 + hashLen + 2
		e.stage = uint8(flags >> 12 & 3)
		e.assumeValid = flags&0x8000 != 0
		if flags&0x4000 != 0 {
			if version < 3 {
				return nil, errors.New("index: extended flags in version 2")
			}
			if len(data)-pos < 2 {
				return nil, errors.New("index: truncated extended flags")
			}
			ext := binary.BigEndian.Uint16(data[pos:])
			pos += 2
			e.skipWorktree = ext&0x4000 != 0
		}
		if version == 4 {
			strip, n := indexVarint(data[pos:])
			if n == 0 || strip > uint64(len(prev)) {
				return nil, errors.New("index: bad path prefix")
			}
			pos += n
			end := bytes.IndexByte(data[pos:], 0)
			if end < 0 {
				return nil, errors.New("index: unterminated path")
			}
			path := append(append([]byte{}, prev[:uint64(len(prev))-strip]...), data[pos:pos+end]...)
			if uint64(len(path)) > pathBudget {
				return nil, fmt.Errorf("%w: paths expand beyond the file size", errIndexUnsupported)
			}
			pathBudget -= uint64(len(path))
			pos += end + 1
			prev = path
			e.path = string(path)
		} else {
			end := bytes.IndexByte(data[pos:], 0)
			if end < 0 {
				return nil, errors.New("index: unterminated path")
			}
			e.path = string(data[pos : pos+end])
			pos += end + 1
			// The entry is padded with NULs to a multiple of 8 bytes.
			for (pos-start)%8 != 0 {
				if pos >= len(data) || data[pos] != 0 {
					return nil, errors.New("index: bad entry padding")
				}
				pos++
			}
		}
		if e.path == "" {
			// The entries of a split index have no path until the shared index fills them.
			return nil, fmt.Errorf("%w: empty path", errIndexUnsupported)
		}
		if e.mode&0o170000 == modeTree {
			return nil, fmt.Errorf("%w: directory entry (sparse index)", errIndexUnsupported)
		}
		out = append(out, e)
	}
	// Extensions: a 4-byte signature and a 4-byte size each, then the trailing checksum.
	for len(data)-pos > hashLen {
		if len(data)-pos < 8+hashLen {
			return nil, errors.New("index: truncated extension")
		}
		sig := string(data[pos : pos+4])
		size := binary.BigEndian.Uint32(data[pos+4 : pos+8])
		if sig == "link" {
			return nil, fmt.Errorf("%w: split index", errIndexUnsupported)
		}
		if uint64(size) > uint64(len(data)-pos-8-hashLen) {
			return nil, errors.New("index: extension exceeds file size")
		}
		pos += 8 + int(size)
	}
	return out, nil
}

// indexVarint reads git's offset encoding (used for v4 path prefixes) and the number of
// bytes it took; 0 bytes when b ends early or the value overflows.
func indexVarint(b []byte) (uint64, int) {
	if len(b) == 0 {
		return 0, 0
	}
	c := b[0]
	n := 1
	val := uint64(c & 127)
	for c&128 != 0 {
		val++
		if n >= len(b) || val > 1<<56 {
			return 0, 0
		}
		c = b[n]
		n++
		val = val<<7 + uint64(c&127)
	}
	return val, n
}
