package capture

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/observability"
)

// The stages a capture_failed line names.
const (
	stageStore  = "store"
	stageParse  = "parse"
	stageCommit = "commit"
)

// process is the pipeline a worker runs for one exchange: store it raw, parse it,
// canonicalise the events, store them with the parse status. The raw exchange is
// stored first, so a parse failure never loses it; a failure to store it stops the
// pipeline, since there is no row for events to hang from. Once the row is stored the
// committed callback runs last, even when the parse or SaveParse failed. The
// exchange's memory is released whatever the outcome.
func (s *Sink) process(ex *core.Exchange) {
	defer ex.Release()
	err := s.write(ex, observability.StoreOpExchange, func(ctx context.Context) error {
		return s.store.SaveExchange(ctx, ex)
	})
	if err != nil {
		s.storeFailure(ex.RequestID, err)
		ex.TraceEnd(core.CaptureFailed)
		return
	}

	res, logged := s.parse(ex)
	status, reason := res.Status, ""
	if !knownStatus(status) {
		status, reason = core.ParseFailed, "parser returned an unknown status"
	}
	stored, contents, err := core.Canonicalize(res.Events, ex.Excluded)
	if err != nil {
		// The parser broke the event contract; its events can't be stored.
		status, reason = core.ParseFailed, "parser events are not canonical"
		stored, contents = nil, nil
	}
	if status == core.ParseFailed && !logged {
		if reason == "" {
			reason = "parser reported failure"
		}
		s.parseFailed.Add(1)
		s.log.Error("capture_failed",
			zap.String("request_id", ex.RequestID),
			zap.String("stage", stageParse),
			zap.String("reason", reason))
	}

	err = s.write(ex, observability.StoreOpParse, func(ctx context.Context) error {
		return s.store.SaveParse(ctx, ex.RequestID, ex.PrincipalID, status, stored, contents)
	})
	outcome := core.CaptureStored
	if err != nil {
		s.storeFailure(ex.RequestID, err)
		outcome = core.CaptureFailed
	}
	if ex.Trace != nil {
		ex.TraceEnd(outcome, captureAttrs(status, res.Events)...)
	}
	s.commit(ex)
}

// write runs one store call for ex, timed for the metrics and, when ex is traced, inside
// a store_write span under its capture span (spec 021).
func (s *Sink) write(ex *core.Exchange, op string, call func(context.Context) error) error {
	ctx := s.ctx
	var span trace.Span
	if ex.Trace != nil {
		ctx, span = ex.Trace.TracerProvider().Tracer(tracerName).Start(
			trace.ContextWithSpan(ctx, ex.Trace), core.SpanStoreWrite,
			trace.WithAttributes(attribute.String(core.AttrStoreOp, op)))
	}
	start := time.Now()
	err := call(ctx)
	s.metrics.StoreWrite(op, err == nil, time.Since(start))
	if span != nil {
		if err != nil {
			span.SetStatus(codes.Error, "store failed")
		}
		span.End()
	}
	return err
}

// tracerName names the tracer of the store_write spans; it is the instrumentation scope
// Jaeger shows.
const tracerName = "github.com/brutally-honest/llm-gateway"

// maxModelAttr bounds the model attribute: the value comes from the client.
const maxModelAttr = 128

// captureAttrs are the capture span's parse status and the model the request named,
// read from the canonical request event; never content.
func captureAttrs(status core.ParseStatus, events []core.Event) []attribute.KeyValue {
	attrs := []attribute.KeyValue{attribute.String(core.AttrParseStatus, string(status))}
	for _, e := range events {
		if e.Kind == core.KindRequest && e.Request != nil && e.Request.Model != "" {
			model := e.Request.Model
			if len(model) > maxModelAttr {
				model = model[:maxModelAttr]
			}
			attrs = append(attrs, attribute.String(core.AttrModel, model))
			break
		}
	}
	return attrs
}

// commit calls the committed callback for ex, whose row is stored. A panic in the
// callback is recovered and logged, with the stack but never the panic's value, so it
// never reaches the worker.
func (s *Sink) commit(ex *core.Exchange) {
	if s.onCommit == nil {
		return
	}
	defer func() {
		if v := recover(); v != nil {
			logging.LogPanic(s.log, "capture_failed", v,
				zap.String("request_id", ex.RequestID),
				zap.String("stage", stageCommit),
				zap.String("reason", "committed callback panicked"))
		}
	}()
	s.onCommit(Committed{
		RequestID:   ex.RequestID,
		PrincipalID: ex.PrincipalID,
		Client:      ex.Client,
		SessionKey:  ex.SessionKey,
	})
}

// parse runs the exchange's parser; with none the parse is skipped. A panic in the
// parser is recovered here, so it fails this exchange's parse and not the worker. It
// is logged and counted at once, with the stack but never the panic's value, which
// can hold request data; logged says so.
func (s *Sink) parse(ex *core.Exchange) (res core.ParseResult, logged bool) {
	if ex.Parser == nil {
		return core.ParseResult{Status: core.ParseSkipped}, false
	}
	defer func() {
		if v := recover(); v != nil {
			s.parseFailed.Add(1)
			logging.LogPanic(s.log, "capture_failed", v,
				zap.String("request_id", ex.RequestID),
				zap.String("stage", stageParse),
				zap.String("reason", "parser panicked"))
			res, logged = core.ParseResult{Status: core.ParseFailed}, true
		}
	}()
	return ex.Parser.Parse(core.ParseInput{
		Method:            ex.Method,
		Path:              ex.Path,
		Query:             ex.Query,
		Status:            ex.Status,
		RequestHeader:     ex.RequestHeader,
		ResponseHeader:    ex.ResponseHeader,
		RequestBody:       ex.Request.Bytes(),
		ResponseBody:      ex.Response.Bytes(),
		RequestTruncated:  ex.Request.Truncated,
		ResponseTruncated: ex.Response.Truncated,
		RequestIncomplete: ex.RequestIncomplete,
		Stream:            ex.Stream,
		GatewayResponse:   !ex.HasTTFB,
		DecodeLimit:       s.decodeLimit,
	}), false
}

// storeFailure logs and counts one failed store call. The error names the store's
// path and the request ID, never request data.
func (s *Sink) storeFailure(requestID string, err error) {
	s.storeFailed.Add(1)
	s.log.Error("capture_failed",
		zap.String("request_id", requestID),
		zap.String("stage", stageStore),
		zap.Error(err))
}

func knownStatus(st core.ParseStatus) bool {
	switch st {
	case core.ParseOK, core.ParsePartial, core.ParseSkipped, core.ParseUnsupportedEncoding, core.ParseFailed:
		return true
	}
	return false
}
