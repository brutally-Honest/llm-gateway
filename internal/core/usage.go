package core

import (
	"net/http"
)

// UsageResult is what the response of one call reported, handed to Meta.OnUsage
// when the call settles. Tokens is meaningful only when OK is true; Status is the
// response status, 0 if none was written.
type UsageResult struct {
	Tokens int64
	OK     bool
	Status int
}

// usageWriter shows the adapter's UsageReader the response bytes as they go to the
// client. Each Write goes to the inner writer first and only what it accepted is
// fed on, so observing never delays a byte. It runs on the handler's goroutine only,
// as Meta does, so it needs no lock. Unwrap lets http.NewResponseController reach the
// inner writer's Flush.
type usageWriter struct {
	http.ResponseWriter

	adapter     RateLimitAdapter
	wroteHeader bool
	status      int
	reader      UsageReader // nil: none made yet, or the status was not a final one
}

func newUsageWriter(w http.ResponseWriter, a RateLimitAdapter) *usageWriter {
	return &usageWriter{ResponseWriter: w, adapter: a}
}

// WriteHeader records the first final status and starts the reader from the headers
// about to be sent. An informational 1xx is forwarded but not recorded.
func (u *usageWriter) WriteHeader(code int) {
	if !u.wroteHeader && code >= http.StatusOK {
		u.start(code)
	}
	u.ResponseWriter.WriteHeader(code)
}

func (u *usageWriter) start(code int) {
	u.wroteHeader = true
	u.status = code
	h := u.Header()
	u.reader = u.newReader(code, h.Clone(), isEventStream(h.Get("Content-Type")))
}

// newReader asks the adapter for a reader; an adapter that panics means no reader.
func (u *usageWriter) newReader(code int, h http.Header, stream bool) (r UsageReader) {
	defer func() {
		if recover() != nil {
			r = nil
		}
	}()
	return u.adapter.UsageReader(code, h, stream)
}

// Write forwards p, then feeds the reader what the inner writer accepted.
func (u *usageWriter) Write(p []byte) (int, error) {
	if !u.wroteHeader {
		u.start(http.StatusOK) // net/http's implicit WriteHeader(200)
	}
	n, err := u.ResponseWriter.Write(p)
	if u.reader != nil && n > 0 {
		u.feed(p[:n])
	}
	return n, err
}

// feed hands b to the reader; a reader that panics is dropped, so the call has no
// usage and the response is untouched.
func (u *usageWriter) feed(b []byte) {
	defer func() {
		if recover() != nil {
			u.reader = nil
		}
	}()
	u.reader.Write(b)
}

// Unwrap returns the inner writer, for http.NewResponseController.
func (u *usageWriter) Unwrap() http.ResponseWriter { return u.ResponseWriter }

// finish ends the reader and returns what it saw. A reader that panics reports no
// usage.
func (u *usageWriter) finish(fallbackStatus int) (res UsageResult) {
	res.Status = u.status
	if res.Status == 0 {
		res.Status = fallbackStatus
	}
	if u.reader == nil {
		return res
	}
	defer func() {
		if recover() != nil {
			res.Tokens, res.OK = 0, false
		}
	}()
	res.Tokens, res.OK = u.reader.Tokens()
	u.reader = nil
	return res
}

// finishUsage calls OnUsage once with what the response reported. It runs from
// Settle, so it also runs when the client left or upstream cut the stream: the
// usage seen so far counts.
func (m *Meta) finishUsage() {
	u := m.usage
	if u == nil {
		return
	}
	m.usage = nil
	if m.OnUsage != nil {
		m.OnUsage(u.finish(m.Status))
	}
}
