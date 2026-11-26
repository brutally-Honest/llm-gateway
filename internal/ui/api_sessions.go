package ui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/cost"
	"github.com/brutally-honest/llm-gateway/internal/logging"
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
	// RowVersion is the store version the row's cheap fields were read at (spec 043 D10);
	// Version is the one its facts were built at. Absent for an ungrouped exchange.
	RowVersion *versionJSON `json:"row_version,omitempty"`
	// Pending marks a list row whose facts are not built yet: it marshals to its own shape,
	// the cheap fields only (spec 043 AC1, D15).
	Pending bool `json:"-"`
}

// pendingJSON is a pending row's shape: the cheap fields and nothing heavy, not even as
// zero values.
type pendingJSON struct {
	ID            string            `json:"id"`
	SessionKey    string            `json:"session_key"`
	Client        string            `json:"client"`
	FirstAt       string            `json:"first_at"`
	LastAt        string            `json:"last_at"`
	ExchangeCount int               `json:"exchange_count"`
	Active        bool              `json:"active"`
	HooksOnly     bool              `json:"hooks_only"`
	HasCalls      bool              `json:"has_calls"`
	Effects       *effectCountsJSON `json:"effects"`
	List          pendingListJSON   `json:"list"`
	RowVersion    *versionJSON      `json:"row_version"`
	Pending       bool              `json:"pending"`
}

type pendingListJSON struct {
	Folder     string `json:"folder"`
	FolderFull string `json:"folder_full"`
}

// plainSummary marshals as the struct's fields, without MarshalJSON.
type plainSummary sessionSummary

// MarshalJSON writes a pending row in its own shape and any other row as its fields.
func (s sessionSummary) MarshalJSON() ([]byte, error) {
	if !s.Pending {
		return json.Marshal(plainSummary(s))
	}
	p := pendingJSON{ID: s.ID, SessionKey: s.SessionKey, Client: s.Client, FirstAt: s.FirstAt, LastAt: s.LastAt,
		ExchangeCount: s.ExchangeCount, Active: s.Active, HooksOnly: s.HooksOnly, HasCalls: s.HasCalls,
		Effects: s.Effects, RowVersion: s.RowVersion, Pending: true}
	if s.List != nil {
		p.List = pendingListJSON{Folder: s.List.Folder, FolderFull: s.List.FolderFull}
	}
	return json.Marshal(p)
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
	Scanned int `json:"scanned"`
	// Checking counts the scanned rows a filter could not decide yet because their facts
	// are not built; their builds are queued (spec 043 AC4). Always present.
	Checking              int `json:"checking"`
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
//
// With a disk cache it first reads the session's effect version, before any data, and
// looks for the memo on disk at (ver, that effect version); a hit is returned as it is.
// ver is the store version the caller asked for.
func (d Deps) digestCompute(row store.SessionRow, ver store.Version) func(context.Context) (digestMemo, error) {
	vd := d.viewDeps()
	return func(bctx context.Context) (digestMemo, error) {
		c := d.Reader.WithContext(bctx)
		var eff store.EffectVersion
		if d.Disk != nil {
			id := sessionIdent(row)
			evs, err := c.EffectVersions([]store.SessionIdent{id})
			if err != nil {
				return digestMemo{}, err
			}
			eff = evs[id]
			if m, ok := d.Disk.GetMemo(id, viewKeyOf(ver, eff)); ok {
				m.eff, m.fromDisk = eff, true
				return m, nil
			}
		}
		m, err := computeMemoFn(c, vd, row)
		m.eff = eff
		return m, err
	}
}

// computeMemoFn is how digestCompute computes a memo. It is a test hook: tests replace it
// to act after the data has been read.
var computeMemoFn = computeMemo

// memoSource is what a memo compute reads. *store.CtxReader satisfies it.
type memoSource interface {
	timeline.Source
	viewReader
	HookEvents(principal, client, key string) ([]store.HookRow, error)
}

// computeMemo computes a session's stats, digest and list-row facts from one read of its
// exchanges, with each exchange's events loaded once for all three. The row facts come
// from the session's view, so the list counts in the session page's terms (spec 017 AC1).
func computeMemo(base memoSource, vd viewDeps, row store.SessionRow) (digestMemo, error) {
	seen := &absentSource{memoSource: base}
	var src memoSource = seen
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
	v, degraded, viewPartial, err := vd.sessionViewPartial(src, who, bs, hooks, false, "")
	if err != nil {
		return digestMemo{}, err
	}
	return digestMemo{digest: dg, stats: st, row: rowFactsOf(v), degraded: degraded,
		partial: viewPartial || seen.absent.Load()}, nil
}

// absentSource notes a content read that found nothing stored or an unavailable blob. The
// digest reads content through timeline, which turns both into "no content", so this is
// where a memo learns its digest was built without some block.
type absentSource struct {
	memoSource
	absent atomic.Bool
}

func (a *absentSource) Content(hash string) ([]byte, error) {
	b, err := a.memoSource.Content(hash)
	if hash != "" && (errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrContentUnavailable)) {
		a.absent.Store(true)
	}
	return b, err
}

// summarizeRows builds the summaries of rows (ids[i] is rows[i]'s id), waiting for a
// session's memo when nothing is cached for it; summarizeRowsNoWait is the list's. The versions of
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
	// the cheap folder, from the store, wins over the memo's (spec 043 AC1)
	folders, err := c.SessionFolders(idents)
	if err != nil {
		return nil, nil, err
	}
	home, _ := os.UserHomeDir()
	out := make([]sessionSummary, 0, len(rows))
	var stale []store.SessionIdent
	for i, row := range rows {
		ver := vers[idents[i]]
		rv := versionToJSON(ver)
		var memo digestMemo
		if d.Digests != nil && d.Reader != nil {
			hit, err := d.Digests.latest(ctx, idents[i], ver, d.digestCompute(row, ver))
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
		full := folders[idents[i]]
		sum.List.FolderFull, sum.List.Folder = full, timeline.FolderDisplay(full, home)
		vj, pj := versionToJSON(ver), positionOf(ver)
		sum.Version, sum.EventPosition, sum.RowVersion = &vj, &pj, &rv
		n := counts[idents[i]]
		sum.Effects = &effectCountsJSON{Changed: n.Changed, Read: n.Read, Sites: n.Sites}
		out = append(out, sum)
	}
	return out, stale, nil
}

// listRank is where one list request's builds rank in the gate: its tick, and the position
// of the first of its rows in the request.
type listRank struct {
	tick uint64
	base int
	// pos, when set, gives each row's position (one per row of the call) in place of base+i.
	pos []int
}

// at is the position of the i-th row of a call.
func (r listRank) at(i int) int {
	if r.pos != nil {
		return r.pos[i]
	}
	return r.base + i
}

// sessionVersionsFn reads versions for the list; deferred marks the read after a filtered
// answer. It is a test hook, like computeMemoFn.
var sessionVersionsFn = func(c *store.CtxReader, ids []store.SessionIdent, deferred bool) (map[store.SessionIdent]store.Version, error) {
	return c.SessionVersions(ids)
}

// newListRank numbers a list request, so its rows build before an older request's.
func (d Deps) newListRank() listRank {
	if d.Digests == nil {
		return listRank{}
	}
	return listRank{tick: d.Digests.newListTick()}
}

// summarizeRowsNoWait is summarizeRows for the sessions list and the stream's opening
// snapshot (spec 043): it never waits for a build. Per row, the facts come from the memory
// cache (this version or an older one, as summarizeRows), else from the disk cache at the
// row's current key, which it reads at once and keeps in memory; else the row is pending
// and its build is queued in the gate at the rank of its place in the request. The cheap
// fields, the folder included, come from the store and always win over a memo's.
func (d Deps) summarizeRowsNoWait(ctx context.Context, c *store.CtxReader, ids []string, rows []store.SessionRow, now time.Time, rank listRank) ([]sessionSummary, []store.SessionIdent, error) {
	if d.Digests == nil || d.Reader == nil {
		return d.summarizeRows(ctx, c, ids, rows, now)
	}
	idents := make([]store.SessionIdent, len(rows))
	for i, r := range rows {
		idents[i] = sessionIdent(r)
	}
	vers, err := sessionVersionsFn(c, idents, false)
	if err != nil {
		return nil, nil, err
	}
	counts, err := c.EffectCounts(idents)
	if err != nil {
		return nil, nil, err
	}
	folders, err := c.SessionFolders(idents)
	if err != nil {
		return nil, nil, err
	}
	var effs map[store.SessionIdent]store.EffectVersion // read once, when a row misses memory
	home, _ := os.UserHomeDir()
	out := make([]sessionSummary, 0, len(rows))
	var stale []store.SessionIdent
	for i, row := range rows {
		id := idents[i]
		ver := vers[id]
		rv := versionToJSON(ver)
		full := folders[id]
		n := counts[id]
		fx := &effectCountsJSON{Changed: n.Changed, Read: n.Read, Sites: n.Sites}
		gp := gatePrio{class: classList, tick: rank.tick, pos: rank.at(i)}

		memo, memoVer, ok := d.Digests.memoryMemo(id, ver)
		// disk is read at the row's current key when memory has no memo at this version
		// (none, or an older one: AC2), so a restart does not queue a build for a memo
		// that is on disk
		if (!ok || memoVer != ver) && d.Disk != nil && (!ok || !versionAfter(memoVer, ver)) {
			if effs == nil {
				if effs, err = c.EffectVersions(idents); err != nil {
					return nil, nil, err
				}
			}
			if m, hit := d.Disk.GetMemo(id, viewKeyOf(ver, effs[id])); hit {
				m.eff = effs[id]
				d.Digests.putFromDisk(id, ver, m)
				memo, memoVer, ok = m, ver, true
			}
		}
		// a memo newer than the version just read needs no build at the older one: it
		// would take a slot and its result be dropped. A degraded memo is served but is
		// no answer: it is rebuilt, once per request.
		if ok && (memoVer != ver && !versionAfter(memoVer, ver) || memo.degraded) {
			d.Digests.queue(id, ver, gp, d.digestCompute(row, ver))
			if versionAfter(ver, memoVer) {
				stale = append(stale, id)
			}
		}
		if !ok {
			d.Digests.queue(id, ver, gp, d.digestCompute(row, ver))
			out = append(out, pendingSummary(ids[i], row, full, home, fx, rv, now))
			continue
		}
		sum := buildSummary(ids[i], row, memo.stats, memo.digest, now)
		if memo.row.title != "" {
			sum.Title = memo.row.title
		}
		sum.List = listToJSON(memo.row)
		sum.List.FolderFull, sum.List.Folder = full, timeline.FolderDisplay(full, home)
		vj, pj := versionToJSON(memoVer), positionOf(memoVer)
		sum.Version, sum.EventPosition, sum.RowVersion = &vj, &pj, &rv
		sum.Effects = fx
		out = append(out, sum)
	}
	return out, stale, nil
}

// pendingSummary is a list row with the cheap fields only (spec 043 D1).
func pendingSummary(id string, row store.SessionRow, folderFull, home string, fx *effectCountsJSON, rv versionJSON, now time.Time) sessionSummary {
	return sessionSummary{
		ID: id, SessionKey: row.SessionKey, Client: row.Client, FirstAt: nsTime(row.First), LastAt: nsTime(row.Last),
		ExchangeCount: row.Count, HasCalls: row.Count > 0, Active: isActive(row.Last, row.EndOnly, now), HooksOnly: row.HooksOnly,
		Effects: fx, List: &listJSON{Folder: timeline.FolderDisplay(folderFull, home), FolderFull: folderFull},
		RowVersion: &rv, Pending: true,
	}
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

// prefillBatch is how many sessions the background pass reads versions for at once.
const prefillBatch = 50

// Prefill is the background pass over the sessions list (spec 030 AC7): after the UI
// serves, it walks every session in list order, one at a time, and builds the memo and
// the view the on-disk cache lacks, through the normal paths, so they coalesce with the
// requests that want them and are written. It then trims the cache to its size cap and
// records how long it took. Without a disk cache it only warms the first page's memos.
// It stops when ctx ends and never fails: a session that cannot be built is left to its
// first request.
func Prefill(ctx context.Context, d Deps) {
	if d.Reader == nil {
		return
	}
	start := time.Now()
	d.Disk.Cleanup()
	var after *store.SessionAfter
	for ctx.Err() == nil {
		rows, vers, effs, err := d.prefillBatchOf(ctx, after)
		if err != nil {
			if ctx.Err() == nil {
				d.logger().Warn("ui background pass: cannot read sessions", zap.Error(err))
			}
			return
		}
		now := d.now()
		for _, row := range rows {
			if ctx.Err() != nil {
				return
			}
			id := sessionIdent(row)
			d.prefillOne(ctx, row, vers[id], effs[id], now)
			d.Disk.WaitRoom(ctx)
		}
		if len(rows) < prefillBatch || d.Disk == nil {
			break
		}
		after = afterOfRow(rows[len(rows)-1])
	}
	if ctx.Err() != nil {
		return
	}
	d.Disk.Trim(ctx)
	d.Metrics.CacheRebuild(time.Since(start))
}

func (d Deps) logger() *zap.Logger {
	if d.Log == nil {
		return zap.NewNop()
	}
	return d.Log
}

// prefillBatchOf reads one batch of sessions after the given position and their versions.
func (d Deps) prefillBatchOf(ctx context.Context, after *store.SessionAfter) ([]store.SessionRow, map[store.SessionIdent]store.Version, map[store.SessionIdent]store.EffectVersion, error) {
	rctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	c := d.Reader.WithContext(rctx)
	rows, err := c.SessionPage(after, prefillBatch)
	if err != nil {
		return nil, nil, nil, err
	}
	idents := make([]store.SessionIdent, len(rows))
	for i, r := range rows {
		idents[i] = sessionIdent(r)
	}
	vers, err := c.SessionVersions(idents)
	if err != nil {
		return nil, nil, nil, err
	}
	effs, err := c.EffectVersions(idents)
	if err != nil {
		return nil, nil, nil, err
	}
	return rows, vers, effs, nil
}

// prefillOne builds one session's memo and view if they are not on disk. Errors are left
// to the session's first request.
func (d Deps) prefillOne(ctx context.Context, row store.SessionRow, ver store.Version, eff store.EffectVersion, now time.Time) {
	id := sessionIdent(row)
	if d.Digests != nil {
		// The pass yields to list work, and its memo run ranks last in the gate. The run's
		// own timeout starts at its slot, so the wait here is not bounded by one.
		d.Digests.waitListIdle(ctx)
		if _, err := d.Digests.getBackground(ctx, id, ver, d.digestCompute(row, ver)); err != nil && ctx.Err() == nil {
			d.logger().Debug("ui background pass: memo not built", zap.Error(err))
		}
	}
	sctx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	// the view of an active session is not written (D18), so it is not built for the cache
	if d.Disk == nil || d.Timelines == nil || isActive(row.Last, row.EndOnly, now) || d.Disk.HasView(sctx, id, viewKeyOf(ver, eff), now) {
		return
	}
	if _, err := d.cachedView(asBackground(sctx), sessIdent{row.PrincipalID, row.Client, row.SessionKey}, true); err != nil && ctx.Err() == nil {
		d.logger().Debug("ui background pass: view not built", zap.Error(err))
	}
}

// sessionMatches is used by tests only; the scan decides with sessionVerdict.
// It tests a built summary against the list filter and search text; q is
// already lowercased and trimmed.
func sessionMatches(s sessionSummary, kind, q string) bool {
	return sessionVerdict(s, kind, q) == verdictYes
}

// verdict is what a filter says of a row: it matches, it does not, or its facts are not
// built yet and it cannot say.
type verdict int

const (
	verdictNo verdict = iota
	verdictYes
	verdictMaybe
)

// sessionVerdict tests a summary, pending or built, against kind and q (lowercased,
// trimmed). Active and hooks are decided from the cheap fields; look needs the facts; q
// first tries the cheap fields (client, key, id, folder), and a row with facts then its
// whole text. The row matches when both parts match, and is undecided when a part is and
// none is a definite no.
func sessionVerdict(s sessionSummary, kind, q string) verdict {
	kv := verdictYes
	switch kind {
	case "look":
		switch {
		case s.Pending:
			kv = verdictMaybe
		case s.List == nil || s.List.NeedsLook <= 0:
			kv = verdictNo
		}
	case "active":
		if !s.Active {
			kv = verdictNo
		}
	case "hooks":
		// the digest's HasCalls is "has an exchange", so a built row's rule is exactly
		// the hooks-only rows (spec 043 D5), which a pending row has to go by
		if !s.HooksOnly && (s.Pending || !s.HasHooks || s.HasCalls) {
			kv = verdictNo
		}
	}
	qv := verdictYes
	if q != "" {
		qv = searchVerdict(s, q)
	}
	switch {
	case kv == verdictNo || qv == verdictNo:
		return verdictNo
	case kv == verdictMaybe || qv == verdictMaybe:
		return verdictMaybe
	}
	return verdictYes
}

func searchVerdict(s sessionSummary, q string) verdict {
	parts := []string{s.Client, s.SessionKey, s.ID}
	if s.List != nil {
		parts = append(parts, s.List.Folder, s.List.FolderFull)
	}
	if containsAny(parts, q) {
		return verdictYes
	}
	if s.Pending {
		return verdictMaybe
	}
	if containsAny(append([]string{s.Title}, s.Models...), q) {
		return verdictYes
	}
	return verdictNo
}

// containsAny: each field alone, so a q holding a newline cannot match across two fields.
func containsAny(parts []string, q string) bool {
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

// scanSessions reads up to scanCap rows from after in one list query, then resolves their
// facts row by row in batches of defaultListLimit+1 and tests each against kind and q
// (spec 043 AC4, D14). It never waits for a build: a row whose facts are missing is tested
// on its cheap fields, and when that cannot decide it, it is left out, counted in checking
// and its build queued. It stops on the first of three rules: limit rows match and it is
// known whether another row exists; scanCap rows were tested; or elapsed time plus the
// last batch's duration would pass scanBudget, so no new batch starts (the first batch
// always runs, so every request progresses). Or the rows run out. next is the after token
// of the last tested row when an untested row remains.
func (d Deps) scanSessions(ctx context.Context, c *store.CtxReader, after *store.SessionAfter, limit int, kind, q string) (items []sessionSummary, next *string, scanned, checking, total int, err error) {
	items = []sessionSummary{}
	now := d.now()
	start := scanNow()
	rows, total, err := c.SessionPageTotal(after, scanCap)
	if err != nil {
		return nil, nil, 0, 0, 0, err
	}
	rank := d.newListRank()
	// the undecided rows' versions are read and their builds queued once the scan is done,
	// off the request (spec 043 D18)
	var undecided []deferredRow
	defer func() { d.queueDeferred(undecided) }()
	// moreAfter: is there a row after rows[i]? rows holds all that the query returned, so
	// only a full read can have more behind it.
	moreAfter := func(i int) (bool, error) {
		if i < len(rows)-1 {
			return true, nil
		}
		if len(rows) < scanCap {
			return false, nil
		}
		peek, err := c.SessionPage(afterOfRow(rows[i]), 1)
		return len(peek) > 0, err
	}
	var last time.Duration // how long the previous batch took, the estimate for the next
	for off := 0; off < len(rows); off += defaultListLimit + 1 {
		batchStart := scanNow()
		if scanned > 0 && batchStart.Sub(start)+last > scanBudget {
			more, err := moreAfter(off - 1)
			if err != nil {
				return nil, nil, 0, 0, 0, err
			}
			if more {
				next = sessionAfter(rows[off-1])
			}
			return items, next, scanned, checking, total, nil
		}
		batch := rows[off:min(off+defaultListLimit+1, len(rows))]
		sums, und, err := d.scanBatch(ctx, c, batch, off, kind, q, now, rank, limit-len(items))
		if err != nil {
			return nil, nil, 0, 0, 0, err
		}
		undecided = append(undecided, und...)
		last = scanNow().Sub(batchStart)
		for i, row := range batch {
			scanned++
			switch sessionVerdict(sums[i], kind, q) {
			case verdictYes:
				items = append(items, sums[i])
			case verdictMaybe:
				checking++
			}
			if len(items) < limit && scanned < scanCap {
				continue
			}
			more, err := moreAfter(off + i)
			if err != nil {
				return nil, nil, 0, 0, 0, err
			}
			if more {
				next = sessionAfter(row)
			}
			return items, next, scanned, checking, total, nil
		}
	}
	return items, nil, scanned, checking, total, nil
}

// deferredRow is an undecided row of a scan: its facts are not in memory or on disk and
// its cheap fields cannot decide it, so its version is read, and its build queued, after
// the answer.
type deferredRow struct {
	row store.SessionRow
	gp  gatePrio
}

// scanBatch is the summaries of one batch of a scan, in row order. A row that kind alone
// rules out is not read for facts. Every row known to match counts against room: a cheap
// match at once, a row with a memo in memory or on disk once its facts are read. Memo rows
// are read in groups, only while the rows still to come could reach room (spec 043 D18).
// Rows after the room-th match are neither read nor queued (AC7). Undecided rows are counted
// in checking, and only those up to the scan's stop row are handed back to be queued.
func (d Deps) scanBatch(ctx context.Context, c *store.CtxReader, batch []store.SessionRow, off int, kind, q string, now time.Time, rank listRank, room int) ([]sessionSummary, []deferredRow, error) {
	out := make([]sessionSummary, len(batch))
	var cand []int // the rows kind does not rule out
	for i, row := range batch {
		if (kind == "active" && !isActive(row.Last, row.EndOnly, now)) || (kind == "hooks" && !row.HooksOnly) {
			out[i] = sessionSummary{Pending: true} // decided "no" by the kind; no fact is read
			continue
		}
		cand = append(cand, i)
	}
	if len(cand) == 0 {
		return out, nil, nil
	}
	// read resolves the facts of the given rows of the batch, in one read
	read := func(rows []int) error {
		if len(rows) == 0 {
			return nil
		}
		want := make([]store.SessionRow, len(rows))
		wantIDs := make([]string, len(rows))
		pos := make([]int, len(rows))
		for j, i := range rows {
			want[j], wantIDs[j], pos[j] = batch[i], sessionID(batch[i]), off+i
		}
		// the rank positions follow the request's row order, whichever rows are wanted
		sums, _, err := d.summarizeRowsNoWait(ctx, c, wantIDs, want, now, listRank{tick: rank.tick, pos: pos})
		if err != nil {
			return err
		}
		for j, i := range rows {
			out[i] = sums[j]
		}
		return nil
	}
	if d.Digests == nil || d.Reader == nil {
		return out, nil, read(cand)
	}
	idents := make([]store.SessionIdent, len(cand))
	for j, i := range cand {
		idents[j] = sessionIdent(batch[i])
	}
	folders, err := c.SessionFolders(idents)
	if err != nil {
		return nil, nil, err
	}
	home, _ := os.UserHomeDir()
	// The rows are taken in order until the scan's stop row, the room-th row it returns.
	// matched counts the rows known to be returned: a cheap match is returned whatever its
	// facts say, a row with a memo only once its facts are read. Rows with a memo are read
	// in groups, each as soon as the rows still to be read could reach the room, so a row
	// is read only when fewer than room rows before it are known to match. A row after
	// the stop is neither read nor queued (AC7).
	var (
		pending []int // rows to read, in order
		unsure  []int // rows of pending whose verdict is not known before the read
		matched int
		undec   []int // undecided rows (batch indexes), queued if they are before the stop
	)
	flush := func() error {
		if err := read(pending); err != nil {
			return err
		}
		for _, i := range unsure {
			if sessionVerdict(out[i], kind, q) == verdictYes {
				matched++
			}
		}
		pending, unsure = nil, nil
		return nil
	}
	for j, i := range cand {
		id := idents[j]
		cheap := pendingSummary(sessionID(batch[i]), batch[i], folders[id], home, nil, versionJSON{}, now)
		out[i] = cheap
		if matched >= room {
			continue // after the stop row
		}
		switch {
		case sessionVerdict(cheap, kind, q) == verdictYes:
			matched++
			pending = append(pending, i)
		default:
			if _, inMem := d.Digests.peek(id); inMem || d.Disk.HasMemoFile(id) {
				pending = append(pending, i)
				unsure = append(unsure, i)
			} else {
				undec = append(undec, i)
			}
		}
		if len(unsure) > 0 && matched+len(unsure) >= room {
			if err := flush(); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, nil, err
	}
	// the stop row: the room-th row the scan returns; undecided rows after it are dropped
	stop, n := len(batch), 0
	for i := range batch {
		if sessionVerdict(out[i], kind, q) == verdictYes {
			if n++; n >= room {
				stop = i
				break
			}
		}
	}
	var und []deferredRow
	for _, i := range undec {
		if i < stop {
			und = append(und, deferredRow{row: batch[i], gp: gatePrio{class: classList, tick: rank.tick, pos: off + i}})
		}
	}
	return out, und, nil
}

// queueDeferred reads the versions of the undecided rows of a scan and queues their builds
// at the rank they would have had, on one goroutine off the request, on the cache's
// lifetime. A failure is logged and dropped; the next request queues them again.
func (d Deps) queueDeferred(rows []deferredRow) {
	if len(rows) == 0 || d.Digests == nil || d.Reader == nil {
		return
	}
	logging.Go(d.logger(), "ui_queue_undecided", func() error {
		ctx, cancel := context.WithTimeout(d.Digests.base, storeTimeout)
		defer cancel()
		idents := make([]store.SessionIdent, len(rows))
		for i, r := range rows {
			idents[i] = sessionIdent(r.row)
		}
		vers, err := sessionVersionsFn(d.Reader.WithContext(ctx), idents, true)
		if err != nil {
			if ctx.Err() == nil {
				d.logger().Debug("ui list: cannot read versions of undecided rows", zap.Error(err))
			}
			return nil
		}
		for i, r := range rows {
			ver := vers[idents[i]]
			d.Digests.queue(idents[i], ver, r.gp, d.digestCompute(r.row, ver))
		}
		return nil
	})
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
	offset := 0
	if query.Has("offset") {
		n, err := strconv.Atoi(query.Get("offset"))
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "bad_request", "offset must be an integer, 0 or more")
			return
		}
		if after != nil || kind != "all" || q != "" {
			writeErr(w, http.StatusBadRequest, "bad_request", "offset cannot be combined with after, kind or q")
			return
		}
		offset = n
	}
	ctx, cancel := storeCtx(r)
	defer cancel()
	// the rows and their versions are read in one snapshot, so a row's version is never
	// newer than the cheap fields it labels (spec 043 review P3)
	c, done, err := d.Reader.WithContext(ctx).Snapshot()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	defer done()
	page := sessionsPage{}
	if kind == "all" && q == "" {
		var rows []store.SessionRow
		var err error
		if query.Has("offset") {
			rows, page.Total, err = c.SessionPageAtTotal(offset, limit+1)
		} else {
			rows, page.Total, err = c.SessionPageTotal(after, limit+1)
		}
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
		items, _, err := d.summarizeRowsNoWait(ctx, c, ids, rows, d.now(), d.newListRank())
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		page.Items, page.Scanned = items, len(items)
		if more {
			page.NextAfter = sessionAfter(rows[len(rows)-1])
		}
	} else {
		items, next, scanned, checking, total, err := d.scanSessions(ctx, c, after, limit, kind, q)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		page.Items, page.NextAfter, page.Scanned, page.Checking, page.Total = items, next, scanned, checking, total
	}
	if page.Items == nil {
		page.Items = []sessionSummary{}
	}
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
	c, done, err := d.Reader.WithContext(ctx).Snapshot()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	defer done()
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
