package core

import (
	"context"
	"io"
	"time"
)

// Test-only doors to the unexported body watchers, for package core_test. The proxy
// wraps the bodies itself; tests reach the same code through these.

func WatchRequestBody(m *Meta, rc io.ReadCloser) io.ReadCloser  { return m.watchRequestBody(rc) }
func WatchResponseBody(m *Meta, rc io.ReadCloser) io.ReadCloser { return m.watchResponseBody(rc) }
func RequestBodyErr(m *Meta) error                              { return m.requestBodyErr() }
func ResponseBodyErr(m *Meta) error                             { return m.responseBodyErr() }

// FlushInterval is the built ReverseProxy's flush interval.
func FlushInterval(p *Proxy) time.Duration { return p.rp.FlushInterval }

// Classify is the error handler's status and reason for err; ctx is the inbound
// request's context.
func Classify(ctx context.Context, m *Meta, err error) (int, string) { return classify(ctx, m, err) }
