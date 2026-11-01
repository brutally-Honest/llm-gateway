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
	ID            string    `json:"id"`
	SessionKey    string    `json:"session_key"`
	Client        string    `json:"client"`
	Models        []string  `json:"models"`
	FirstAt       string    `json:"first_at"`
	LastAt        string    `json:"last_at"`
	ExchangeCount int       `json:"exchange_count"`
	ModelCalls    int       `json:"model_calls"`
	ToolCalls     int       `json:"tool_calls"`
	Errors        int       `json:"errors"`
	Usage         usageJSON `json:"usage"`
	Active        bool      `json:"active"`
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

func buildSummary(id string, row store.SessionRow, st timeline.Stats, now time.Time) sessionSummary {
	models := make([]string, 0, len(st.Usage.Models))
	for _, m := range st.Usage.Models {
		models = append(models, m.Model)
	}
	last := time.Unix(0, row.Last)
	return sessionSummary{
		ID: id, SessionKey: row.SessionKey, Client: row.Client, Models: models,
		FirstAt: nsTime(row.First), LastAt: nsTime(row.Last),
		ExchangeCount: row.Count, ModelCalls: st.ModelCalls, ToolCalls: st.ToolCalls, Errors: st.Errors,
		Usage: usageToJSON(st.Usage), Active: now.Sub(last) < ActiveWindow,
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

func ungroupedSummary(c *store.CtxReader, ex store.ExchangeRow, now time.Time) (sessionSummary, error) {
	row := store.SessionRow{PrincipalID: ex.PrincipalID, Client: ex.Client, First: ex.StartedAt, Last: ex.StartedAt, Count: 1}
	st, err := timeline.SessionStats(singleSource{c, ex}, row)
	if err != nil {
		return sessionSummary{}, err
	}
	s := buildSummary(ex.RequestID, row, st, now)
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
	now := d.now()
	page := listPage{Items: []sessionSummary{}}
	for _, row := range rows {
		st, err := timeline.SessionStats(c, row)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		page.Items = append(page.Items, buildSummary(sessionID(row), row, st, now))
	}
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
		s, err := ungroupedSummary(c, ex, now)
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
	st, err := timeline.SessionStats(c, row)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, buildSummary(sessionID(row), row, st, d.now()))
}
