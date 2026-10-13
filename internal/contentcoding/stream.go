package contentcoding

import (
	"bufio"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/logging"
)

// NewReader returns a streaming decoder for the codings named in a Content-Encoding
// header value, undone in reverse order like Decode. An unknown coding anywhere in
// the list returns a nil reader and Unsupported. Decoders start on first Read, so
// NewReader itself never reads src. Close releases decoder resources; it does not
// close src.
func NewReader(encoding string, src io.Reader) (io.ReadCloser, Status) {
	cs, ok := parse(encoding)
	if !ok {
		return nil, Unsupported
	}
	r := &chain{}
	cur := src
	for i := len(cs) - 1; i >= 0; i-- {
		if cs[i] == identity {
			continue
		}
		l := &lazy{c: cs[i], src: cur}
		r.layers = append(r.layers, l)
		cur = l
	}
	r.top = cur
	return r, Complete
}

type chain struct {
	top    io.Reader
	layers []*lazy
}

func (c *chain) Read(p []byte) (int, error) { return c.top.Read(p) }

func (c *chain) Close() error {
	for _, l := range c.layers {
		if l.closeFn != nil {
			l.closeFn()
		}
	}
	return nil
}

// lazy builds one streaming decoder on first Read, because the constructors of
// gzip, zlib and the zlib sniff read the stream header.
type lazy struct {
	c       coding
	src     io.Reader
	r       io.Reader
	err     error
	closeFn func()
}

func (l *lazy) Read(p []byte) (int, error) {
	if l.r == nil && l.err == nil {
		l.r, l.closeFn, l.err = newStream(l.c, l.src)
		if errors.Is(l.err, io.EOF) {
			l.err = io.ErrUnexpectedEOF
		}
	}
	if l.err != nil {
		return 0, l.err
	}
	return l.r.Read(p)
}

func newStream(c coding, src io.Reader) (io.Reader, func(), error) {
	nop := func() {}
	switch c {
	case gzipCoding:
		r, err := gzip.NewReader(src)
		if err != nil {
			return nil, nop, err
		}
		return r, func() { _ = r.Close() }, nil
	case deflateCoding:
		br := bufio.NewReader(src)
		head, err := br.Peek(2)
		if err != nil && len(head) == 0 {
			return nil, nop, err
		}
		if isZlib(head) {
			r, err := zlib.NewReader(br)
			if err != nil {
				return nil, nop, err
			}
			return r, func() { _ = r.Close() }, nil
		}
		r := flate.NewReader(br)
		return r, func() { _ = r.Close() }, nil
	case brCoding:
		return brotli.NewReader(src), nop, nil
	case zstdCoding:
		r, err := zstd.NewReader(src, zstd.WithDecoderConcurrency(1))
		if err != nil {
			return nil, nop, err
		}
		return r, r.Close, nil
	case identity:
	}
	return nil, nop, errors.New("contentcoding: no reader for coding")
}

const (
	// MaxDecoded caps what a Feeder will decode, so a decompression bomb cannot
	// burn CPU without bound.
	MaxDecoded = 64 << 20
	// DefaultMaxPending is the queue size the callers of NewFeeder are expected to use.
	DefaultMaxPending = 1 << 20
)

// Feeder decodes a side copy of a byte stream without ever blocking the writer.
// Write copies into a bounded queue and returns at once; one goroutine decodes the
// queue and hands the decoded stream to a sink.
type Feeder struct {
	mu         sync.Mutex
	cond       *sync.Cond
	pending    []byte
	maxPending int
	closed     bool
	overflowed bool

	done      chan struct{}
	once      sync.Once
	status    Status
	panicType string
}

// NewFeeder starts the decoding goroutine. sink is called once, from that
// goroutine, with the decoded stream, unless the encoding is unsupported; whatever
// sink leaves unread is drained and discarded. maxPending bounds the compressed
// bytes queued for the decoder. A panic in the decoder or the sink is recovered:
// decoding stops, Close reports Panicked and PanicType names the panic value's type.
func NewFeeder(encoding string, sink func(io.Reader), maxPending int) *Feeder {
	f := &Feeder{maxPending: maxPending, done: make(chan struct{})}
	f.cond = sync.NewCond(&f.mu)
	// run recovers every panic itself, so logging.Go's recover never fires; the nop
	// logger is only its fallback and nothing is logged here.
	_ = logging.Go(zap.NewNop(), "contentcoding.feeder", func() error {
		f.run(encoding, sink)
		return nil
	})
	return f
}

// PanicType is the type of the value a recovered panic carried, e.g. "string", or
// "" when the decoder did not panic. Call it after Close.
func (f *Feeder) PanicType() string {
	<-f.done
	return f.panicType
}

// Write queues a copy of p. It never blocks on the decoder: if the queue is full
// the feeder is marked Overflowed and p, and everything after it, is dropped.
func (f *Feeder) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.overflowed {
		return len(p), nil
	}
	if len(f.pending)+len(p) > f.maxPending {
		f.overflowed = true
		f.cond.Broadcast()
		return len(p), nil
	}
	f.pending = append(f.pending, p...)
	f.cond.Broadcast()
	return len(p), nil
}

// Close ends the input, waits for the decoder to finish and reports how it went.
func (f *Feeder) Close() Status {
	f.once.Do(func() {
		f.mu.Lock()
		f.closed = true
		f.cond.Broadcast()
		f.mu.Unlock()
	})
	<-f.done
	return f.status
}

// Read is the decoder's view of the queue: blocks for data, EOF once closed or
// overflowed and empty.
func (f *Feeder) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for len(f.pending) == 0 && !f.closed && !f.overflowed {
		f.cond.Wait()
	}
	if len(f.pending) == 0 {
		return 0, io.EOF
	}
	n := copy(p, f.pending)
	f.pending = f.pending[n:]
	if len(f.pending) == 0 {
		f.pending = nil
	}
	return n, nil
}

func (f *Feeder) run(encoding string, sink func(io.Reader)) {
	defer close(f.done)
	defer func() {
		if v := recover(); v != nil {
			f.panicType = fmt.Sprintf("%T", v)
			f.status = Panicked
		}
	}()
	r, st := NewReader(encoding, f)
	if st == Unsupported {
		_, _ = io.Copy(io.Discard, f)
		f.status = Unsupported
		return
	}
	defer func() { _ = r.Close() }()
	cr := &cappedReader{r: r, left: MaxDecoded}
	sink(cr)
	_, _ = io.Copy(io.Discard, cr)
	_, _ = io.Copy(io.Discard, f)
	f.mu.Lock()
	overflowed := f.overflowed
	f.mu.Unlock()
	switch {
	case overflowed:
		f.status = Overflowed
	case cr.cut:
		f.status = CutShort
	default:
		f.status = Complete
	}
}

// cappedReader stops after left decoded bytes and remembers a read error or the
// cap, either of which means the decoded stream is a prefix.
type cappedReader struct {
	r    io.Reader
	left int64
	cut  bool
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.cut {
		return 0, io.EOF
	}
	if c.left <= 0 {
		// Probe one byte to tell "exactly at the cap" from "more would follow".
		var one [1]byte
		n, err := c.r.Read(one[:])
		if n > 0 {
			c.cut = true
		} else if err != nil && err != io.EOF {
			c.cut = true
		}
		return 0, io.EOF
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	if err != nil && err != io.EOF {
		c.cut = true
		err = io.EOF
	}
	return n, err
}
