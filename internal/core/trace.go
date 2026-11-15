package core

import (
	"context"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// The span names of a proxied call (spec 021 AC11).
const (
	SpanReceive         = "gateway.receive"
	SpanUpstreamRequest = "gateway.upstream_request"
	SpanResponseStream  = "gateway.response_stream"
	SpanCapture         = "gateway.capture"
	SpanStoreWrite      = "gateway.store_write"
)

// The span attribute keys. Every value is a size, a status, a time, a fixed word, the
// exchange id or a model name: never content, never a header (spec 021 AC11).
const (
	AttrExchangeID         = "gateway.exchange_id"
	AttrProtocol           = "gateway.protocol"
	AttrClient             = "gateway.client"
	AttrMethod             = "http.request.method"
	AttrStatus             = "http.response.status_code"
	AttrStream             = "gateway.stream"
	AttrRequestBytes       = "http.request.body.size"
	AttrResponseBytes      = "http.response.body.size"
	AttrTTFBMillis         = "gateway.ttfb_ms"
	AttrGatewayError       = "gateway.error"
	AttrClientDisconnected = "gateway.client_disconnected"
	AttrUpstreamAborted    = "gateway.upstream_aborted"
	AttrCapture            = "gateway.capture"
	AttrParseStatus        = "gateway.parse_status"
	AttrModel              = "gen_ai.request.model"
	AttrStoreOp            = "gateway.store.op"
)

// callTrace is one proxied call's spans. A nil *callTrace is tracing off, and every
// method is a no-op on it, so the hot path pays one nil check.
type callTrace struct {
	tracer   trace.Tracer
	receive  trace.Span
	upstream trace.Span // nil until the call is sent, and again once it is answered
	stream   trace.Span // nil until upstream answers
}

// startCallTrace starts the receive span of a call the proxy just took.
func startCallTrace(t trace.Tracer, m *Meta, r *http.Request) *callTrace {
	attrs := []attribute.KeyValue{
		attribute.String(AttrExchangeID, m.RequestID),
		attribute.String(AttrProtocol, m.Protocol),
		attribute.String(AttrClient, m.Client),
		attribute.String(AttrMethod, r.Method),
	}
	if r.ContentLength >= 0 {
		attrs = append(attrs, attribute.Int64(AttrRequestBytes, r.ContentLength))
	}
	_, span := t.Start(context.Background(), SpanReceive,
		trace.WithSpanKind(trace.SpanKindServer), trace.WithTimestamp(m.start), trace.WithAttributes(attrs...))
	return &callTrace{tracer: t, receive: span}
}

// child starts a span under the receive span.
func (c *callTrace) child(name string, kind trace.SpanKind, at time.Time) trace.Span {
	_, span := c.tracer.Start(trace.ContextWithSpan(context.Background(), c.receive), name,
		trace.WithSpanKind(kind), trace.WithTimestamp(at))
	return span
}

// sent starts the upstream request span: the call is handed to the transport.
func (c *callTrace) sent(at time.Time) {
	if c == nil {
		return
	}
	c.upstream = c.child(SpanUpstreamRequest, trace.SpanKindClient, at)
}

// answered ends the upstream request span at upstream's response headers and starts the
// response stream span.
func (c *callTrace) answered(status int, stream bool) {
	if c == nil {
		return
	}
	now := time.Now()
	if c.upstream != nil {
		c.upstream.SetAttributes(attribute.Int(AttrStatus, status))
		c.upstream.End(trace.WithTimestamp(now))
		c.upstream = nil
	}
	c.stream = c.child(SpanResponseStream, trace.SpanKindInternal, now)
	c.stream.SetAttributes(attribute.Bool(AttrStream, stream))
}

// failed ends the upstream request span of a call that got no response headers.
func (c *callTrace) failed(status int, reason string) {
	if c == nil || c.upstream == nil {
		return
	}
	c.upstream.SetAttributes(attribute.Int(AttrStatus, status))
	if reason != "" {
		c.upstream.SetAttributes(attribute.String(AttrGatewayError, reason))
		c.upstream.SetStatus(codes.Error, reason)
	}
	c.upstream.End()
	c.upstream = nil
}

// capture starts the capture span for an exchange handed to the sink.
func (c *callTrace) capture() trace.Span {
	if c == nil {
		return nil
	}
	return c.child(SpanCapture, trace.SpanKindInternal, time.Now())
}

// finish ends the stream and receive spans once Settle has decided the call's outcome.
func (c *callTrace) finish(m *Meta) {
	if c == nil {
		return
	}
	resBytes := m.responseBytes()
	if c.upstream != nil { // sent, never answered nor failed: the call was cut
		c.upstream.End()
	}
	if c.stream != nil {
		c.stream.SetAttributes(attribute.Int64(AttrResponseBytes, resBytes))
		if m.UpstreamAborted {
			c.stream.SetStatus(codes.Error, "upstream aborted")
		}
		c.stream.End()
	}
	attrs := []attribute.KeyValue{
		attribute.Int(AttrStatus, m.Status),
		attribute.Bool(AttrStream, m.Stream),
		attribute.Int64(AttrResponseBytes, resBytes),
		attribute.Bool(AttrClientDisconnected, m.ClientDisconnected),
		attribute.Bool(AttrUpstreamAborted, m.UpstreamAborted),
		attribute.String(AttrCapture, m.Capture),
	}
	if n := m.requestBytes(); n > 0 {
		attrs = append(attrs, attribute.Int64(AttrRequestBytes, n))
	}
	if m.HasTTFB {
		attrs = append(attrs, attribute.Float64(AttrTTFBMillis, float64(m.TTFB.Microseconds())/1000))
	}
	if m.GatewayError != "" {
		attrs = append(attrs, attribute.String(AttrGatewayError, m.GatewayError))
	}
	c.receive.SetAttributes(attrs...)
	if m.GatewayError != "" || m.UpstreamAborted || m.Status >= 500 {
		c.receive.SetStatus(codes.Error, "")
	}
	c.receive.End()
}

// TraceEnd ends ex's capture span, if it has one, with what became of the capture.
// The sink calls it once the exchange is stored, or given back unstored at shutdown.
func (ex *Exchange) TraceEnd(outcome string, attrs ...attribute.KeyValue) {
	if ex.Trace == nil {
		return
	}
	ex.Trace.SetAttributes(append(attrs, attribute.String(AttrCapture, outcome))...)
	if outcome != CaptureStored {
		ex.Trace.SetStatus(codes.Error, outcome)
	}
	ex.Trace.End()
	ex.Trace = nil
}

// CaptureStored and CaptureUndrained are the capture span's outcomes past the queue:
// stored by a worker, or given back unstored when shutdown ran out of time.
const (
	CaptureStored    = "stored"
	CaptureUndrained = "undrained"
	CaptureFailed    = "store_failed"
)
