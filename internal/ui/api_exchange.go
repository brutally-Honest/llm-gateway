package ui

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/contentcoding"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

type exchangeJSON struct {
	ID        string `json:"id"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Status    int    `json:"status"`
	Protocol  string `json:"protocol"`
	Client    string `json:"client"`
	Stream    bool   `json:"stream"`
	Parse     string `json:"parse"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at"`
	// TTFBMS is nil when no response headers were recorded.
	TTFBMS     *float64 `json:"ttfb_ms"`
	DurationMS float64  `json:"duration_ms"`

	Model string         `json:"model,omitempty"`
	Usage *callUsageJSON `json:"usage"`

	SessionID string `json:"session_id,omitempty"`

	RequestTruncated   bool   `json:"request_truncated"`
	ResponseTruncated  bool   `json:"response_truncated"`
	RequestIncomplete  bool   `json:"request_incomplete"`
	ClientDisconnected bool   `json:"client_disconnected"`
	UpstreamAborted    bool   `json:"upstream_aborted"`
	GatewayError       string `json:"gateway_error,omitempty"`

	RequestBodyStored  bool `json:"request_body_stored"`
	ResponseBodyStored bool `json:"response_body_stored"`

	RequestHeaders  http.Header `json:"request_headers"`
	ResponseHeaders http.Header `json:"response_headers"`

	// Drawer facts (AC22-AC24, AC29, AC40). SideCall is the client rules' verdict.
	// PrevID is the call this one continues; ThreadNew is true when it starts a thread.
	SideCall  bool   `json:"side_call"`
	PrevID    string `json:"prev_id"`
	ThreadNew bool   `json:"thread_new"`
	// Messages are the request's messages, each marked new or carried from PrevID.
	// MessagesUnavailable says why there are none; it is empty when they are listed.
	Messages            []exchangeMessageJSON `json:"messages"`
	MessagesUnavailable string                `json:"messages_unavailable,omitempty"`
	Carried             int                   `json:"carried"`
	NewCount            int                   `json:"new_count"`
	Response            responseJSON          `json:"response"`
	// ResponseToolCallIDs lets the browser join tool states from the loaded timeline.
	ResponseToolCallIDs []string `json:"response_tool_call_ids"`
}

type exchangeMessageJSON struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
	New       bool   `json:"new"`
}

// responseJSON is the reply assembled from the stored events. Unavailable is a reason
// when it could not be built; Text and StopReason are then empty.
type responseJSON struct {
	Text        string `json:"text"`
	StopReason  string `json:"stop_reason"`
	Unavailable string `json:"unavailable,omitempty"`
}

// unavailableReason says why an exchange's events hold nothing to show.
func unavailableReason(ex store.ExchangeRow, body string, bodyStored, truncated bool) string {
	switch {
	case ex.Parse == "failed" || ex.Parse == "skipped" || ex.Parse == "unsupported_encoding":
		return "parse_" + ex.Parse
	case !bodyStored:
		return body + "_body_not_stored"
	case truncated:
		return "capture_truncated"
	}
	return "nothing_parsed"
}

func ms(ns int64) float64 { return float64(ns) / 1e6 }

func (d Deps) getExchange(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := storeCtx(r)
	defer cancel()
	c := d.Reader.WithContext(ctx)
	ex, err := c.Exchange(chi.URLParam(r, "id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "no such exchange")
		return
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	evs, err := c.Events(ex.RequestID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	out := exchangeJSON{
		ID: ex.RequestID, Method: ex.Method, Path: ex.Path, Status: ex.Status,
		Protocol: ex.Protocol, Client: ex.Client, Stream: ex.Stream, Parse: ex.Parse,
		StartedAt: nsTime(ex.StartedAt), EndedAt: nsTime(ex.EndedAt), DurationMS: ms(ex.EndedAt - ex.StartedAt),
		RequestTruncated: ex.RequestTruncated, ResponseTruncated: ex.ResponseTruncated,
		RequestIncomplete: ex.RequestIncomplete, ClientDisconnected: ex.ClientDisconnected,
		UpstreamAborted: ex.UpstreamAborted, GatewayError: ex.GatewayError,
		RequestBodyStored: ex.RequestBody != "", ResponseBodyStored: ex.ResponseBody != "",
		RequestHeaders: nonNil(ex.RequestHeaders), ResponseHeaders: nonNil(ex.ResponseHeaders),
	}
	if ex.TTFBNS != nil {
		out.TTFBMS = ptr(ms(*ex.TTFBNS))
	}
	if call := timeline.CallOf(ex, evs); call != nil {
		out.Model, out.Usage = call.Model, callToJSON(call)
	}
	if ex.SessionKey != "" {
		out.SessionID = sessionID(store.SessionRow{PrincipalID: ex.PrincipalID, Client: ex.Client, SessionKey: ex.SessionKey})
	}
	if err := d.fillDrawer(c, ex, &out); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, out)
}

// fillDrawer adds the thread, message and reply facts. It reads the whole session to
// place the call among its threads; a section it cannot build gets a reason instead.
func (d Deps) fillDrawer(c *store.CtxReader, ex store.ExchangeRow, out *exchangeJSON) error {
	out.Messages, out.ResponseToolCallIDs = []exchangeMessageJSON{}, []string{}
	exs := []store.ExchangeRow{ex}
	if ex.SessionKey != "" {
		var err error
		if exs, err = c.SessionExchanges(ex.PrincipalID, ex.Client, ex.SessionKey); err != nil {
			return err
		}
	}
	byID := make(map[string]store.ExchangeRow, len(exs))
	for _, x := range exs {
		byID[x.RequestID] = x
	}
	src := snapshotSource{c, exs}
	session := store.SessionRow{PrincipalID: ex.PrincipalID, Client: ex.Client, SessionKey: ex.SessionKey}
	built, err := timeline.Build(src, session, timeline.Options{Cut: 1})
	if err != nil {
		return err
	}
	facts, err := callFacts(src, built, d.Rules[ex.Client], byID)
	if err != nil {
		return err
	}
	f := facts[ex.RequestID]
	if f != nil {
		out.SideCall, out.PrevID, out.ThreadNew = f.SideCall, f.PrevID, f.Thread >= 0 && f.PrevID == ""
	}
	det, err := timeline.ExchangeDetail(c, ex, entryCut)
	if err != nil {
		return err
	}
	if len(det.Messages) == 0 {
		out.MessagesUnavailable = unavailableReason(ex, "request", out.RequestBodyStored, ex.RequestTruncated)
	}
	if f != nil {
		// The carried count covers the reply too, which the next request resends; only
		// the request's own messages are listed.
		out.Carried = min(f.Carried, len(det.Messages))
	}
	out.NewCount = len(det.Messages) - out.Carried
	for i, m := range det.Messages {
		out.Messages = append(out.Messages, exchangeMessageJSON{Role: m.Role, Text: m.Text, Truncated: m.Truncated, New: i >= out.Carried})
	}
	if det.HasResponse {
		out.Response = responseJSON{Text: det.ResponseText, StopReason: det.StopReason}
		out.ResponseToolCallIDs = append(out.ResponseToolCallIDs, det.ResponseToolCallIDs...)
	} else {
		out.Response = responseJSON{Unavailable: unavailableReason(ex, "response", out.ResponseBodyStored, ex.ResponseTruncated)}
	}
	return nil
}

func nonNil(h http.Header) http.Header {
	if h == nil {
		return http.Header{}
	}
	return h
}

// bodyCSP is the policy of every response from the body endpoint.
const bodyCSP = "default-src 'none'; sandbox"

func (d Deps) getBody(w http.ResponseWriter, r *http.Request) {
	// Stored bodies are data, never a page: nothing in them may run or load. Every answer
	// from this endpoint, its errors included, carries the strict policy (AC33).
	w.Header().Set("Content-Security-Policy", bodyCSP)
	part := r.URL.Query().Get("part")
	if part != "request" && part != "response" {
		writeErr(w, http.StatusBadRequest, "bad_request", "part must be request or response")
		return
	}
	ctx, cancel := storeCtx(r)
	defer cancel()
	c := d.Reader.WithContext(ctx)
	ex, err := c.Exchange(chi.URLParam(r, "id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "no such exchange")
		return
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	hash, hdr := ex.RequestBody, ex.RequestHeaders
	if part == "response" {
		hash, hdr = ex.ResponseBody, ex.ResponseHeaders
	}
	if hash == "" {
		writeErr(w, http.StatusNotFound, "body_not_stored", "this body was not stored")
		return
	}
	raw, err := c.Content(hash)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "body_not_stored", "this body was not stored")
		return
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	// Decode one byte past the cap, so a longer body is seen as longer. A coding
	// we cannot undo is served as stored.
	body, _ := contentcoding.Decode(strings.Join(hdr.Values("Content-Encoding"), ","), raw, maxBodyBytes+1)
	truncated := len(body) > maxBodyBytes
	if truncated {
		body = body[:maxBodyBytes]
	}
	ct := hdr.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	if truncated {
		h.Set("X-Body-Truncated", "true")
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}
