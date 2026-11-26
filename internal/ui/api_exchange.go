package ui

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

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
	// StatusWord is the status in words; SessionTitle and CallN name the session and the
	// call's #n in it, for the full exchange page's title and breadcrumb (017 AC12). Both
	// are left out for an exchange no session claims, or when no view of its session is
	// cached at the session's current version; CallN also needs that view to hold the call.
	StatusWord   string `json:"status_word"`
	SessionTitle string `json:"session_title,omitempty"`
	CallN        int    `json:"call_n,omitempty"`

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
	out.StatusWord = timeline.StatusWord(ex.Status)
	if ex.SessionKey != "" {
		out.SessionID = sessionID(store.SessionRow{PrincipalID: ex.PrincipalID, Client: ex.Client, SessionKey: ex.SessionKey})
		d.placeInSession(ctx, ex, &out)
	}
	writeJSON(w, out)
}

// placeInSession names the exchange's session and its #n from the session's view as the
// view cache holds it now. It never builds, waits for a build or asks the timeline cache:
// after a restart nothing is cached and a first call's details must not wait on a whole
// session build for a title. Whatever is not known is left out; the page works without it.
//
// The title and the #n come only from a view built at the session's current version
// (two cheap grouped version queries), never from an older one. The whole view key is
// compared, the effect version included: the fallback title takes its start from a range
// that includes effects, so a view built at an older effect version can name a stale start. Both can change as a
// session grows: a generated title or an earlier-starting call that commits late changes
// the title, and calls are numbered in start order, so such a call shifts every number
// after it.
func (d Deps) placeInSession(ctx context.Context, ex store.ExchangeRow, out *exchangeJSON) {
	if d.Views == nil {
		return
	}
	ident := store.SessionIdent{Principal: ex.PrincipalID, Client: ex.Client, Key: ex.SessionKey}
	v, ok := d.Views.peek(ident)
	if !ok {
		return
	}
	c := d.Reader.WithContext(ctx)
	vers, err := c.SessionVersions([]store.SessionIdent{ident})
	if err != nil {
		return
	}
	evs, err := c.EffectVersions([]store.SessionIdent{ident})
	if err != nil || v.Version != viewKeyOf(vers[ident], evs[ident]) {
		return // the view is older than the store: its title and numbering may have moved
	}
	out.SessionTitle = v.Overview.Title
	if n, ok := timeline.CallNumber(v, ex.RequestID); ok {
		out.CallN = n
	}
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
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrContentUnavailable) {
		if d.Log != nil {
			d.Log.Warn("body unavailable", zap.String("request_id", chi.URLParam(r, "id")), zap.String("part", part),
				zap.String("hash", hash), zap.Error(err))
		}
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

// drawerBuild is the session build a call's detail reads its thread facts from. Like the
// timeline, the digest and the snapshot, it is the cached build when one is, so a slow
// rebuild under live traffic never holds the detail (F9). It waits only when nothing is
// cached, or when this call is newer than the cached build and so has no facts there.
func (d Deps) drawerBuild(ctx context.Context, c *store.CtxReader, who sessIdent, id string) (builtSession, error) {
	rules := d.Rules[who.client]
	if d.Timelines == nil {
		return buildSessionFn(c, who, rules, 1)
	}
	ident := store.SessionIdent{Principal: who.principal, Client: who.client, Key: who.name}
	vers, err := c.SessionVersions([]store.SessionIdent{ident})
	if err != nil {
		return builtSession{}, err
	}
	ver := vers[ident]
	cb, err := d.Timelines.latest(ctx, ident, ver, d.timelineBuild(who))
	if err != nil {
		return builtSession{}, err
	}
	if _, ok := cb.bs.byID[id]; ok || !versionAfter(ver, cb.ver) {
		return cb.bs, nil
	}
	cb, err = d.Timelines.get(ctx, ident, ver, d.timelineBuild(who))
	if err != nil {
		return builtSession{}, err
	}
	return cb.bs, nil
}
