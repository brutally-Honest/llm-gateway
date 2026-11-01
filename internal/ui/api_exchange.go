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
	writeJSON(w, out)
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
