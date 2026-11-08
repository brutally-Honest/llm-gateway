package ui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

const (
	// ActiveWindow: a session is active when its last exchange started less than this ago (D9).
	ActiveWindow = 5 * time.Minute
	// storeTimeout bounds every store call of one request (AC10).
	storeTimeout = 5 * time.Second

	defaultListLimit = 50
	maxListLimit     = 200

	listSessions  = "s"
	listUngrouped = "u"
	listTimeline  = "t"
)

// counterJSON is one summed counter: nil value when no call reported it.
type counterJSON struct {
	Value      *int64 `json:"value"`
	Unreported int    `json:"unreported"`
}

// usageJSON is the usage totals with their flags.
type usageJSON struct {
	Calls      int         `json:"calls"`
	Reporting  int         `json:"reporting"`
	Missing    int         `json:"missing"`
	Errored    int         `json:"errored"`
	Partial    int         `json:"partial"`
	Input      counterJSON `json:"input"`
	Output     counterJSON `json:"output"`
	CacheWrite counterJSON `json:"cache_write"`
	CacheRead  counterJSON `json:"cache_read"`
	Incomplete bool        `json:"incomplete"`
	AllErrored bool        `json:"all_errored"`
}

// sessionSummary is the summary shape of a session and of one ungrouped exchange.
type sessionSummary struct {
	ID            string   `json:"id"`
	SessionKey    string   `json:"session_key"`
	Client        string   `json:"client"`
	Models        []string `json:"models"`
	FirstAt       string   `json:"first_at"`
	LastAt        string   `json:"last_at"`
	ExchangeCount int      `json:"exchange_count"`
	ModelCalls    int      `json:"model_calls"`
	ToolCalls     int      `json:"tool_calls"`
	// Errors counts every error: MainErrors + SideErrors.
	Errors     int `json:"errors"`
	MainErrors int `json:"main_errors"`
	SideErrors int `json:"side_errors"`
	// Title is the generated title or the first prompt outside side calls; "" when none.
	Title      string         `json:"title"`
	ToolStates map[string]int `json:"tool_states"`
	HasCalls   bool           `json:"has_calls"`
	HasHooks   bool           `json:"has_hooks"`
	Usage      usageJSON      `json:"usage"`
	Active     bool           `json:"active"`
	// HooksOnly marks a session known only from hook events (AC25).
	HooksOnly bool `json:"hooks_only"`
	// Version and EventPosition are the store version the summary's digest was read at
	// and how far into the store it reaches; absent for an ungrouped exchange.
	Version       *versionJSON       `json:"version,omitempty"`
	EventPosition *eventPositionJSON `json:"event_position,omitempty"`
}

type listPage struct {
	Items     []sessionSummary `json:"items"`
	NextAfter *string          `json:"next_after"`
}

func usageToJSON(s timeline.Summary) usageJSON {
	g := s.Total
	c := func(v timeline.Sum) counterJSON { return counterJSON{Value: v.Value, Unreported: v.Unreported} }
	return usageJSON{
		Calls: g.Calls, Reporting: g.Reporting, Missing: g.Missing, Errored: g.Errored,
		Partial: g.PartialCalls,
		Input:   c(g.Input), Output: c(g.Output), CacheWrite: c(g.CacheWrite), CacheRead: c(g.CacheRead),
		Incomplete: g.Incomplete(), AllErrored: g.AllErrored(),
	}
}

func nsTime(ns int64) string { return time.Unix(0, ns).UTC().Format(time.RFC3339Nano) }

func buildSummary(id string, row store.SessionRow, st timeline.Stats, dg timeline.Digest, now time.Time) sessionSummary {
	models := make([]string, 0, len(st.Usage.Models))
	for _, m := range st.Usage.Models {
		models = append(models, m.Model)
	}
	last := time.Unix(0, row.Last)
	return sessionSummary{
		ID: id, SessionKey: row.SessionKey, Client: row.Client, Models: models,
		FirstAt: nsTime(row.First), LastAt: nsTime(row.Last),
		ExchangeCount: row.Count, ModelCalls: st.ModelCalls, ToolCalls: st.ToolCalls, Errors: st.Errors,
		MainErrors: dg.MainErrors, SideErrors: dg.SideErrors, Title: dg.Title, ToolStates: dg.ToolStates,
		HasCalls: dg.HasCalls, HasHooks: dg.HasHooks,
		Usage: usageToJSON(st.Usage), Active: now.Sub(last) < ActiveWindow, HooksOnly: row.HooksOnly,
	}
}

// sessionID is the opaque id of a session: its identity, base64url (D3).
func sessionID(r store.SessionRow) string {
	b, _ := json.Marshal([]string{r.PrincipalID, r.Client, r.SessionKey})
	return base64.RawURLEncoding.EncodeToString(b)
}

func parseSessionID(id string) (principal, client, key string, ok bool) {
	b, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return "", "", "", false
	}
	var parts []string
	if json.Unmarshal(b, &parts) != nil || len(parts) != 3 || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// afterToken is the decoded `after` value; List tags the list it came from (E4).
type afterToken struct {
	List      string `json:"l"`
	At        int64  `json:"t"`
	Principal string `json:"p,omitempty"`
	Client    string `json:"c,omitempty"`
	Key       string `json:"k,omitempty"`
	RequestID string `json:"r,omitempty"`
	// Index is the entry's position within its exchange (timeline lists only).
	Index int `json:"i,omitempty"`
	// Seq and Row are a hook line's sequence number and stored row id (timeline only).
	Seq int64 `json:"q,omitempty"`
	Row int64 `json:"h,omitempty"`
}

func encodeAfter(t afterToken) string {
	b, _ := json.Marshal(t)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeAfter(s, list string) (afterToken, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return afterToken{}, false
	}
	var t afterToken
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if dec.Decode(&t) != nil || t.List != list {
		return afterToken{}, false
	}
	if (list == listUngrouped || list == listTimeline) && t.RequestID == "" {
		return afterToken{}, false
	}
	return t, true
}

// parseLimit reads ?limit=: absent is def, non-numeric or below 1 is an error, above max is max.
func parseLimit(r *http.Request, def, max int) (int, bool) {
	s := r.URL.Query().Get("limit")
	if !r.URL.Query().Has("limit") {
		return def, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return min(n, max), true
}

// storeCtx is the request context bounded by the store timeout.
func storeCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), storeTimeout)
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// singleSource presents one exchange as a one-exchange session, so SessionStats can
// summarise an ungrouped exchange.
type singleSource struct {
	*store.CtxReader
	ex store.ExchangeRow
}

func (s singleSource) SessionExchanges(string, string, string) ([]store.ExchangeRow, error) {
	return []store.ExchangeRow{s.ex}, nil
}

// summarize reads one session's version, stats and digest and builds its summary. stale
// reports that the digest is older than the store, and a recompute was asked for.
func (d Deps) summarize(ctx context.Context, c *store.CtxReader, id string, row store.SessionRow, now time.Time) (sessionSummary, bool, error) {
	out, stale, err := d.summarizeRows(ctx, c, []string{id}, []store.SessionRow{row}, now)
	if err != nil {
		return sessionSummary{}, false, err
	}
	return out[0], len(stale) > 0, nil
}

func sessionIdent(row store.SessionRow) store.SessionIdent {
	return store.SessionIdent{Principal: row.PrincipalID, Client: row.Client, Key: row.SessionKey}
}

// digestCompute computes a session's memo on bctx through the server's reader, so it
// still lands in the cache when the request that asked for it has timed out.
func (d Deps) digestCompute(row store.SessionRow) func(context.Context) (digestMemo, error) {
	rules := d.Rules[row.Client]
	return func(bctx context.Context) (digestMemo, error) {
		return computeMemo(d.Reader.WithContext(bctx), rules, row)
	}
}

// computeMemo computes a session's stats and digest from one read of its exchanges, with
// each exchange's events loaded once for both.
func computeMemo(src timeline.Source, rules ClientRules, row store.SessionRow) (digestMemo, error) {
	exs, err := src.SessionExchanges(row.PrincipalID, row.Client, row.SessionKey)
	if err != nil {
		return digestMemo{}, err
	}
	once := &eventsOnce{Source: src, exs: exs, events: map[string][]store.EventRow{}}
	st, err := timeline.SessionStats(once, row)
	if err != nil {
		return digestMemo{}, err
	}
	dg, err := sessionDigest(once, rules, row)
	if err != nil {
		return digestMemo{}, err
	}
	return digestMemo{digest: dg, stats: st}, nil
}

// summarizeRows builds the summaries of rows (ids[i] is rows[i]'s id). The versions of
// the whole page come from one read. Stats and digests come from the cache and never wait
// for a recompute when the session has them cached: a summary carries the version they
// were computed at, and stale lists the sessions whose memo is older than the store. The
// session row's counts and the clock-dependent values are always fresh.
func (d Deps) summarizeRows(ctx context.Context, c *store.CtxReader, ids []string, rows []store.SessionRow, now time.Time) ([]sessionSummary, []store.SessionIdent, error) {
	idents := make([]store.SessionIdent, len(rows))
	for i, r := range rows {
		idents[i] = sessionIdent(r)
	}
	vers, err := c.SessionVersions(idents)
	if err != nil {
		return nil, nil, err
	}
	out := make([]sessionSummary, 0, len(rows))
	var stale []store.SessionIdent
	for i, row := range rows {
		ver := vers[idents[i]]
		var memo digestMemo
		if d.Digests != nil && d.Reader != nil {
			hit, err := d.Digests.latest(ctx, idents[i], ver, d.digestCompute(row))
			if err != nil {
				return nil, nil, err
			}
			memo = hit.memo
			if hit.ver != ver {
				stale = append(stale, idents[i])
				ver = hit.ver
			}
		} else if memo, err = computeMemo(c, d.Rules[row.Client], row); err != nil {
			return nil, nil, err
		}
		sum := buildSummary(ids[i], row, memo.stats, memo.digest, now)
		vj, pj := versionToJSON(ver), positionOf(ver)
		sum.Version, sum.EventPosition = &vj, &pj
		out = append(out, sum)
	}
	return out, stale, nil
}

func sessionDigest(src timeline.Source, rules ClientRules, row store.SessionRow) (timeline.Digest, error) {
	var dr timeline.DigestRules
	if rules != nil {
		dr = rules
	}
	return timeline.SessionDigest(src, row, dr)
}

func summarizeFrom(src timeline.Source, rules ClientRules, id string, row store.SessionRow, now time.Time) (sessionSummary, error) {
	st, err := timeline.SessionStats(src, row)
	if err != nil {
		return sessionSummary{}, err
	}
	dg, err := sessionDigest(src, rules, row)
	if err != nil {
		return sessionSummary{}, err
	}
	return buildSummary(id, row, st, dg, now), nil
}

func (d Deps) ungroupedSummary(c *store.CtxReader, ex store.ExchangeRow, now time.Time) (sessionSummary, error) {
	row := store.SessionRow{PrincipalID: ex.PrincipalID, Client: ex.Client, First: ex.StartedAt, Last: ex.StartedAt, Count: 1}
	s, err := summarizeFrom(singleSource{c, ex}, d.Rules[ex.Client], ex.RequestID, row, now)
	if err != nil {
		return sessionSummary{}, err
	}
	s.SessionKey = ""
	return s, nil
}

func mountSessions(api chi.Router, d Deps) {
	api.Get("/sessions", d.listSessions)
	api.Get("/ungrouped", d.listUngrouped)
	api.Get("/sessions/{id}", d.getSession)
}

func (d Deps) listSessions(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(r, defaultListLimit, maxListLimit)
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad_request", "limit must be a positive integer")
		return
	}
	var after *store.SessionAfter
	if r.URL.Query().Has("after") {
		t, ok := decodeAfter(r.URL.Query().Get("after"), listSessions)
		if !ok {
			writeErr(w, http.StatusBadRequest, "bad_request", "after is not a sessions position")
			return
		}
		after = &store.SessionAfter{Last: t.At, PrincipalID: t.Principal, Client: t.Client, SessionKey: t.Key}
	}
	ctx, cancel := storeCtx(r)
	defer cancel()
	c := d.Reader.WithContext(ctx)
	rows, err := c.SessionPage(after, limit+1)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = sessionID(row)
	}
	items, _, err := d.summarizeRows(ctx, c, ids, rows, d.now())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	page := listPage{Items: items}
	if more {
		last := rows[len(rows)-1]
		n := encodeAfter(afterToken{List: listSessions, At: last.Last, Principal: last.PrincipalID, Client: last.Client, Key: last.SessionKey})
		page.NextAfter = &n
	}
	writeJSON(w, page)
}

func (d Deps) listUngrouped(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(r, defaultListLimit, maxListLimit)
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad_request", "limit must be a positive integer")
		return
	}
	var after *store.UngroupedAfter
	if r.URL.Query().Has("after") {
		t, ok := decodeAfter(r.URL.Query().Get("after"), listUngrouped)
		if !ok {
			writeErr(w, http.StatusBadRequest, "bad_request", "after is not an ungrouped position")
			return
		}
		after = &store.UngroupedAfter{StartedAt: t.At, RequestID: t.RequestID}
	}
	ctx, cancel := storeCtx(r)
	defer cancel()
	c := d.Reader.WithContext(ctx)
	rows, err := c.UngroupedPage(after, limit+1)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	now := d.now()
	page := listPage{Items: []sessionSummary{}}
	for _, ex := range rows {
		s, err := d.ungroupedSummary(c, ex, now)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		page.Items = append(page.Items, s)
	}
	if more {
		last := rows[len(rows)-1]
		n := encodeAfter(afterToken{List: listUngrouped, At: last.StartedAt, RequestID: last.RequestID})
		page.NextAfter = &n
	}
	writeJSON(w, page)
}

func (d Deps) getSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	principal, client, key, ok := parseSessionID(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	ctx, cancel := storeCtx(r)
	defer cancel()
	c := d.Reader.WithContext(ctx)
	row, err := c.SessionByKey(principal, client, key)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	sum, _, err := d.summarize(ctx, c, sessionID(row), row, d.now())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, sum)
}
