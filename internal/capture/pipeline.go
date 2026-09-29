package capture

import (
	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
)

// The stages a capture_failed line names.
const (
	stageStore = "store"
	stageParse = "parse"
)

// process is the pipeline a worker runs for one exchange: store it raw, parse it,
// canonicalise the events, store them with the parse status. The raw exchange is
// stored first, so a parse failure never loses it; a failure to store it stops the
// pipeline, since there is no row for events to hang from. The exchange's memory is
// released whatever the outcome.
func (s *Sink) process(ex *core.Exchange) {
	defer ex.Release()
	if err := s.store.SaveExchange(s.ctx, ex); err != nil {
		s.storeFailure(ex.RequestID, err)
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

	if err := s.store.SaveParse(s.ctx, ex.RequestID, ex.PrincipalID, status, stored, contents); err != nil {
		s.storeFailure(ex.RequestID, err)
	}
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
		Stream:            ex.Stream,
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
