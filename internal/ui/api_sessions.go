package ui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/cost"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

const (
	// ActiveWindow: a session is active when its last exchange started less than this ago (D9).
	ActiveWindow = 5 * time.Minute
	// storeTimeout bounds every store call of one request (AC10).
	storeTimeout = 5 * time.Second

	defaultListLimit = 50
	// maxListLimit caps /api/sessions; maxUngroupedLimit caps /api/ungrouped (D4).
	maxListLimit      = 50
	maxUngroupedLimit = 200
	// maxSearchLen is the longest q, in bytes after trimming.
	maxSearchLen = 200

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
	// Cost is the API-equivalent cost of the calls, side calls included (spec 020).
	Cost cost.TotalJSON `json:"cost"`
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
	// Effects are the session's effect counts (spec 014); absent for an ungrouped
	// exchange. They are read fresh with every summary, never memoised with the digest.
	Effects *effectCountsJSON `json:"effects,omitempty"`
	// List is what the sessions list shows from the session's view (spec 017 AC1, AC2);
	// absent for an ungrouped exchange.
	List *listJSON `json:"list,omitempty"`
	// Version and EventPosition are the store version the summary's digest was read at
	// and how far into the store it reaches; absent for an ungrouped exchange.
	Version       *versionJSON       `json:"version,omitempty"`
	EventPosition *eventPositionJSON `json:"event_position,omitempty"`
}

// listJSON is a sessions-list row's counts in the session page's terms (spec 017 AC1,
// AC2): prompts (numbered turns), model calls with side calls apart, commands, the
// wall-clock time from first to last activity, the folder, and how many items need a
// look, by kind.
type listJSON struct {
	Prompts    int                 `json:"prompts"`
	MainCalls  int                 `json:"main_calls"`
	SideCalls  int                 `json:"side_calls"`
	Commands   int                 `json:"commands"`
	ClockMS    int64               `json:"clock_ms"`
	Folder     string              `json:"folder,omitempty"`
	FolderFull string              `json:"folder_full,omitempty"`
	NeedsLook  int                 `json:"needs_look"`
	LookKinds  []timeline.LookKind `json:"look_kinds"`
}

func listToJSON(f rowFacts) *listJSON {
	kinds := f.lookKinds
	if kinds == nil {
		kinds = []timeline.LookKind{}
	}
	return &listJSON{Prompts: f.prompts, MainCalls: f.mainCalls, SideCalls: f.sideCalls, Commands: f.commands,
		ClockMS: f.clockMS, Folder: f.folder, FolderFull: f.folderFull, NeedsLook: f.look, LookKinds: kinds}
}

type listPage struct {
	Items     []sessionSummary `json:"items"`
	NextAfter *string          `json:"next_after"`
	// UnassignedConnections counts network effects linked to no session (sessions list
	// only); absent means none.
	UnassignedConnections int `json:"unassigned_connections,omitempty"`
}

// sessionsPage is the /api/sessions response: a listPage plus total and scanned.
type sessionsPage struct {
	Items     []sessionSummary `json:"items"`
	NextAfter *string          `json:"next_after"`
	// Total counts every session, ignoring kind and q.
	Total int `json:"total"`
	// Scanned is how many rows this request tested.
	Scanned               int `json:"scanned"`
	UnassignedConnections int `json:"unassigned_connections,omitempty"`
}

func usageToJSON(s timeline.Summary) usageJSON {
	g := s.Total
	c := func(v timeline.Sum) counterJSON { return counterJSON{Value: v.Value, Unreported: v.Unreported} }
	return usageJSON{
		Calls: g.Calls, Reporting: g.Reporting, Missing: g.Missing, Errored: g.Errored,
		Partial: g.PartialCalls,
		Input:   c(g.Input), Output: c(g.Output), CacheWrite: c(g.CacheWrite), CacheRead: c(g.CacheRead),
		Incomplete: g.Incomplete(), AllErrored: g.AllErrored(), Cost: g.Cost.JSON(),
	}
}

func nsTime(ns int64) string { return time.Unix(0, ns).UTC().Format(time.RFC3339Nano) }

func buildSummary(id string, row store.SessionRow, st timeline.Stats, dg timeline.Digest, now time.Time) sessionSummary {
	models := make([]string, 0, len(st.Usage.Models))
	for _, m := range st.Usage.Models {
		models = append(models, m.Model)
	}
	return sessionSummary{
		ID: id, SessionKey: row.SessionKey, Client: row.Client, Models: models,
		FirstAt: nsTime(row.First), LastAt: nsTime(row.Last),
		ExchangeCount: row.Count, ModelCalls: st.ModelCalls, ToolCalls: st.ToolCalls, Errors: st.Errors,
		MainErrors: dg.MainErrors, SideErrors: dg.SideErrors, Title: dg.Title, ToolStates: dg.ToolStates,
		HasCalls: dg.HasCalls, HasHooks: dg.HasHooks,
		Usage: usageToJSON(st.Usage), Active: isActive(row.Last, row.EndOnly, now), HooksOnly: row.HooksOnly,
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
	vd := d.viewDeps()
	return func(bctx context.Context) (digestMemo, error) {
		return computeMemo(d.Reader.WithContext(bctx), vd, row)
	}
}

// memoSource is what a memo compute reads. *store.CtxReader satisfies it.
type memoSource interface {
	timeline.Source
	viewReader
	HookEvents(principal, client, key string) ([]store.HookRow, error)
}

// computeMemo computes a session's stats, digest and list-row facts from one read of its
// exchanges, with each exchange's events loaded once for all three. The row facts come
// from the session's view, so the list counts in the session page's terms (spec 017 AC1).
func computeMemo(src memoSource, vd viewDeps, row store.SessionRow) (digestMemo, error) {
	rules := vd.rules[row.Client]
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
	who := sessIdent{row.PrincipalID, row.Client, row.SessionKey}
	bs, err := buildFrom(once, who, exs, rules, entryCut)
	if err != nil {
		return digestMemo{}, err
	}
	hooks, err := src.HookEvents(row.PrincipalID, row.Client, row.SessionKey)
	if err != nil {
		return digestMemo{}, err
	}
	// the facts read here do not depend on the active flag
	v, err := vd.sessionView(src, who, bs, hooks, false, "")
	if err != nil {
		return digestMemo{}, err
	}
	return digestMemo{digest: dg, stats: st, row: rowFactsOf(v)}, nil
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
	counts, err := c.EffectCounts(idents)
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
			if versionAfter(ver, hit.ver) {
				stale = append(stale, idents[i])
				ver = hit.ver
			}
		} else if memo, err = computeMemo(c, d.viewDeps(), row); err != nil {
			return nil, nil, err
		}
		sum := buildSummary(ids[i], row, memo.stats, memo.digest, now)
		// a session's title follows 016's rule, so a row never shows its key (017 AC1)
		if memo.row.title != "" {
			sum.Title = memo.row.title
		}
		sum.List = listToJSON(memo.row)
		vj, pj := versionToJSON(ver), positionOf(ver)
		sum.Version, sum.EventPosition = &vj, &pj
		n := counts[idents[i]]
		sum.Effects = &effectCountsJSON{Changed: n.Changed, Read: n.Read, Sites: n.Sites}
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

// Warm builds the sessions list's first page into d's caches (spec 019). The gateway runs
// it once at start, before the UI accepts connections: cold, that page can take longer
// than one request's store timeout, which made the first page load after a restart
// fail. ctx bounds it; on an error the caches keep what was built so far.
func Warm(ctx context.Context, d Deps) error {
	if d.Reader == nil {
		return nil
	}
	c := d.Reader.WithContext(ctx)
	rows, err := c.SessionPage(nil, defaultListLimit+1)
	if err != nil {
		return err
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = sessionID(row)
	}
	_, _, err = d.summarizeRows(ctx, c, ids, rows, d.now())
	return err
}

// sessionMatches tests a built summary against the list filter and search text; q is
// already lowercased and trimmed.
func sessionMatches(s sessionSummary, kind, q string) bool {
	switch kind {
	case "look":
		if s.List == nil || s.List.NeedsLook <= 0 {
			return false
		}
	case "active":
		if !s.Active {
			return false
		}
	case "hooks":
		if !s.HooksOnly && (!s.HasHooks || s.HasCalls) {
			return false
		}
	}
	if q == "" {
		return true
	}
	parts := []string{s.Title, s.Client, s.SessionKey, s.ID}
	if s.List != nil {
		parts = append(parts, s.List.Folder, s.List.FolderFull)
	}
	parts = append(parts, s.Models...)
	// each field alone, so a q holding a newline cannot match across two fields
	for _, p := range parts {
		if strings.Contains(strings.ToLower(p), q) {
			return true
		}
	}
	return false
}

func sessionAfter(row store.SessionRow) *string {
	n := encodeAfter(afterToken{List: listSessions, At: row.Last, Principal: row.PrincipalID, Client: row.Client, Key: row.SessionKey})
	return &n
}

func afterOfRow(row store.SessionRow) *store.SessionAfter {
	return &store.SessionAfter{Last: row.Last, PrincipalID: row.PrincipalID, Client: row.Client, SessionKey: row.SessionKey}
}

// scanCap and scanBudget are vars so tests can lower the cap or exhaust the budget.
var (
	// scanCap is how many rows one filtered or searched request tests (D3).
	scanCap = 500
	// scanBudget is how long one request keeps starting new batches, well under storeTimeout
	// so the response and its counts still fit (D10).
	scanBudget = 2 * time.Second
	// scanNow is the clock the budget reads; tests replace it.
	scanNow = time.Now
)

// scanSessions reads rows from after in batches and tests each against kind and q. It stops
// on the first of three rules: limit rows match and it is known whether another row exists;
// scanCap rows were tested; or elapsed time plus the last batch's duration would pass
// scanBudget, so no new batch starts (the first batch always runs, so every request
// progresses). Or the rows run out. next is the after
// token of the last tested row when an untested row remains.
func (d Deps) scanSessions(ctx context.Context, c *store.CtxReader, after *store.SessionAfter, limit int, kind, q string) (items []sessionSummary, next *string, scanned int, err error) {
	items = []sessionSummary{}
	now := d.now()
	start := scanNow()
	var last time.Duration // how long the previous batch took, the estimate for the next
	for {
		batchStart := scanNow()
		if scanned > 0 && batchStart.Sub(start)+last > scanBudget {
			peek, err := c.SessionPage(after, 1)
			if err != nil {
				return nil, nil, 0, err
			}
			if len(peek) > 0 {
				n := encodeAfter(afterToken{List: listSessions, At: after.Last, Principal: after.PrincipalID, Client: after.Client, Key: after.SessionKey})
				next = &n
			}
			return items, next, scanned, nil
		}
		// a batch of at least 51 rows keeps a small limit from running the session query hundreds of times
		size := min(max(limit+1, defaultListLimit+1), scanCap-scanned)
		rows, err := c.SessionPage(after, size)
		if err != nil {
			return nil, nil, 0, err
		}
		if len(rows) == 0 {
			return items, nil, scanned, nil
		}
		ids := make([]string, len(rows))
		for i, row := range rows {
			ids[i] = sessionID(row)
		}
		sums, _, err := d.summarizeRows(ctx, c, ids, rows, now)
		if err != nil {
			return nil, nil, 0, err
		}
		last = scanNow().Sub(batchStart)
		for i, sum := range sums {
			scanned++
			if sessionMatches(sum, kind, q) {
				items = append(items, sum)
			}
			if len(items) < limit && scanned < scanCap {
				continue
			}
			// stopped at row i: is there an untested row after it?
			more := i < len(rows)-1
			if !more && len(rows) == size {
				peek, err := c.SessionPage(afterOfRow(rows[i]), 1)
				if err != nil {
					return nil, nil, 0, err
				}
				more = len(peek) > 0
			}
			if more {
				next = sessionAfter(rows[i])
			}
			return items, next, scanned, nil
		}
		if len(rows) < size {
			return items, nil, scanned, nil
		}
		after = afterOfRow(rows[len(rows)-1])
	}
}

func (d Deps) listSessions(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(r, defaultListLimit, maxListLimit)
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad_request", "limit must be a positive integer")
		return
	}
	query := r.URL.Query()
	kind := "all"
	if query.Has("kind") {
		kind = query.Get("kind")
		if kind != "all" && kind != "look" && kind != "active" && kind != "hooks" {
			writeErr(w, http.StatusBadRequest, "bad_request", "kind must be all, look, active or hooks")
			return
		}
	}
	q := strings.TrimSpace(query.Get("q"))
	if len(q) > maxSearchLen {
		writeErr(w, http.StatusBadRequest, "bad_request", "q must be at most 200 bytes")
		return
	}
	q = strings.ToLower(q)
	var after *store.SessionAfter
	if query.Has("after") {
		t, ok := decodeAfter(query.Get("after"), listSessions)
		if !ok {
			writeErr(w, http.StatusBadRequest, "bad_request", "after is not a sessions position")
			return
		}
		after = &store.SessionAfter{Last: t.At, PrincipalID: t.Principal, Client: t.Client, SessionKey: t.Key}
	}
	ctx, cancel := storeCtx(r)
	defer cancel()
	c := d.Reader.WithContext(ctx)
	page := sessionsPage{}
	if kind == "all" && q == "" {
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
		page.Items, page.Scanned = items, len(items)
		if more {
			page.NextAfter = sessionAfter(rows[len(rows)-1])
		}
	} else {
		items, next, scanned, err := d.scanSessions(ctx, c, after, limit, kind, q)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		page.Items, page.NextAfter, page.Scanned = items, next, scanned
	}
	if page.Items == nil {
		page.Items = []sessionSummary{}
	}
	total, err := c.SessionCount()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	page.Total = total
	unassigned, err := c.UnassignedConnections()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	page.UnassignedConnections = unassigned
	writeJSON(w, page)
}

func (d Deps) listUngrouped(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(r, defaultListLimit, maxUngroupedLimit)
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
