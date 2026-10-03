// Package contentcoding decodes a captured body's HTTP Content-Encoding. It is
// generic HTTP: it knows no provider and no wire format, and it only ever reads the
// captured copy, never forwarded bytes.
package contentcoding

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// Status says how far Decode got.
type Status int

const (
	// Complete means every coding was decoded in full, within the limit.
	Complete Status = iota
	// CutShort means the body was truncated or the output reached the limit; what
	// came out is a prefix of the decoded body.
	CutShort
	// Unsupported means the header names a coding Decode does not know; the body is
	// returned as given.
	Unsupported
)

func (s Status) String() string {
	switch s {
	case Complete:
		return "complete"
	case CutShort:
		return "cut_short"
	case Unsupported:
		return "unsupported"
	}
	return "unknown"
}

// Decode undoes the codings named in a Content-Encoding header value. Codings are
// listed in the order they were applied, so they are undone in reverse. Output is
// capped at limit bytes; anything past it is dropped and the status is CutShort.
// An unknown coding anywhere in the list returns the body unchanged, Unsupported.
func Decode(encoding string, body []byte, limit int64) ([]byte, Status) {
	codings, ok := parse(encoding)
	if !ok {
		return body, Unsupported
	}
	status := Complete
	out := body
	for i := len(codings) - 1; i >= 0; i-- {
		var cut bool
		out, cut = decodeOne(codings[i], out, limit)
		if cut {
			status = CutShort
		}
	}
	if int64(len(out)) > limit {
		out, status = out[:max(limit, 0)], CutShort
	}
	return out, status
}

type coding int

const (
	identity coding = iota
	gzipCoding
	deflateCoding
	brCoding
	zstdCoding
)

var codings = map[string]coding{
	"identity": identity,
	"gzip":     gzipCoding,
	"deflate":  deflateCoding,
	"br":       brCoding,
	"zstd":     zstdCoding,
}

// parse splits a header value into codings. Empty list elements are skipped, as
// HTTP list syntax allows them; any unknown token makes the whole value unsupported.
func parse(encoding string) ([]coding, bool) {
	var out []coding
	for _, tok := range strings.Split(encoding, ",") {
		tok = strings.ToLower(strings.TrimSpace(tok))
		if tok == "" {
			continue
		}
		c, ok := codings[tok]
		if !ok {
			return nil, false
		}
		out = append(out, c)
	}
	return out, true
}

// decodeOne undoes one coding, reading at most limit+1 bytes of output. The bool
// reports a cut: a read error part-way or output past limit.
func decodeOne(c coding, in []byte, limit int64) ([]byte, bool) {
	if c == identity {
		return in, false
	}
	r, closeFn, err := reader(c, in)
	if err != nil {
		return nil, true
	}
	defer closeFn()
	out, err := io.ReadAll(io.LimitReader(r, max(limit, 0)+1))
	if int64(len(out)) > limit {
		return out[:max(limit, 0)], true
	}
	return out, err != nil
}

func reader(c coding, in []byte) (io.Reader, func(), error) {
	src := bytes.NewReader(in)
	nop := func() {}
	switch c {
	case gzipCoding:
		r, err := gzip.NewReader(src)
		if err != nil {
			return nil, nil, err
		}
		return r, nop, nil
	case deflateCoding:
		if isZlib(in) {
			r, err := zlib.NewReader(src)
			if err != nil {
				return nil, nil, err
			}
			return r, nop, nil
		}
		r := flate.NewReader(src)
		return r, func() { _ = r.Close() }, nil
	case brCoding:
		return brotli.NewReader(src), nop, nil
	case zstdCoding:
		r, err := zstd.NewReader(src, zstd.WithDecoderConcurrency(1))
		if err != nil {
			return nil, nil, err
		}
		return r, r.Close, nil
	case identity:
	}
	return nil, nil, errors.New("contentcoding: no reader for coding")
}

// isZlib reports whether in starts with a zlib header: compression method 8
// (deflate) and a header checksum that makes CMF<<8|FLG a multiple of 31.
func isZlib(in []byte) bool {
	if len(in) < 2 {
		return false
	}
	cmf, flg := uint16(in[0]), uint16(in[1])
	return cmf&0x0f == 8 && (cmf<<8|flg)%31 == 0
}
