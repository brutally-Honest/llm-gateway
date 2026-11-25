package ui

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// viewCacheSessions is the most sessions whose last view the memo keeps.
const viewCacheSessions = 4

// ViewCache memoises each session's last view per (timeline build version, effect
// version). A view also depends on whether the session is still active, so an entry
// keeps the session's last activity and is rebuilt when the active flag flips.
type ViewCache struct {
	mu    sync.Mutex
	ll    *list.List
	items map[store.SessionIdent]*list.Element
	// jobs runs the background full builds that write disk views (040 D20)
	jobs viewJobs
}

type viewEntry struct {
	id     store.SessionIdent
	key    string
	last   int64 // the session's last activity, unix ns
	active bool
	// endOnly is the session's end-only fact (B12) that active was decided with.
	endOnly bool
	view    timeline.View
	// from and to are the activity range the view read unassigned connections over, and
	// unMax the largest effect id stored before the build read them: a connection with no
	// session stored after it, inside the range, makes the entry a miss (spec 030 D15).
	from, to, unMax int64
}

// NewViewCache returns an empty view memo; the server passes one to Handler and NewHub.
func NewViewCache() *ViewCache {
	return &ViewCache{ll: list.New(), items: map[store.SessionIdent]*list.Element{}}
}

// getEntry returns the session's entry when it is at key and its active flag at now is the
// one it was built with. The caller runs the checks that need the store.
func (c *ViewCache) getEntry(id store.SessionIdent, key string, now time.Time) (*viewEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[id]
	if !ok {
		return nil, false
	}
	e := el.Value.(*viewEntry)
	if e.key != key || isActive(e.last, e.endOnly, now) != e.active {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return e, true
}

// peek returns the session's last view whatever its version, without building anything and
// without moving the entry in the LRU order.
func (c *ViewCache) peek(id store.SessionIdent) (timeline.View, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[id]
	if !ok {
		return timeline.View{}, false
	}
	return el.Value.(*viewEntry).view, true
}

func (c *ViewCache) put(e *viewEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[e.id]; ok {
		el.Value = e
		c.ll.MoveToFront(el)
		return
	}
	c.items[e.id] = c.ll.PushFront(e)
	for c.ll.Len() > viewCacheSessions {
		old := c.ll.Back()
		c.ll.Remove(old)
		delete(c.items, old.Value.(*viewEntry).id)
	}
}

// isActive is the one rule for "active": the last activity is under ActiveWindow old. A
// session whose only hook events are session ends (endOnly, B12) is never active.
func isActive(last int64, endOnly bool, now time.Time) bool {
	return !endOnly && last > 0 && now.Sub(time.Unix(0, last)) < ActiveWindow
}

// endOnlyHooks reports whether a session has no exchange and every hook event is a
// session end; it is the view-side twin of store.SessionRow.EndOnly.
func endOnlyHooks(bs builtSession, hooks []store.HookRow) bool {
	if len(bs.byID) > 0 || len(hooks) == 0 {
		return false
	}
	for _, h := range hooks {
		if h.Phase != string(core.PhaseSessionEnd) {
			return false
		}
	}
	return true
}

func (d Deps) viewDeps() viewDeps { return viewDeps{rules: d.Rules, skew: d.AttributionSkew} }

func mountView(api chi.Router, d Deps) {
	api.Get("/sessions/{id}/view", d.getView)
	api.Get("/sessions/{id}/tools/{tid}", d.getTool)
	api.Get("/sessions/{id}/calls/{eid}", d.getCall)
}

// getCall serves one model call's details (spec 017 AC5–AC11): the session's view places
// it, and its own exchange, usage, thread fact and stored events fill it in. A call that is
// not in the session is a 404 (E3). Nothing here carries a header or a credential: the raw
// links point at the endpoints that serve them.
func (d Deps) getCall(w http.ResponseWriter, r *http.Request) {
	principal, client, key, ok := parseSessionID(chi.URLParam(r, "id"))
	eid := chi.URLParam(r, "eid")
	if !ok || eid == "" {
		writeErr(w, http.StatusNotFound, "not_found", "no such call")
		return
	}
	ctx, cancel := storeCtx(r)
	defer cancel()
	c := d.Reader.WithContext(ctx)
	ex, err := c.Exchange(eid)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (ex.PrincipalID != principal || ex.Client != client || ex.SessionKey != key)) {
		writeErr(w, http.StatusNotFound, "not_found", "no such call in this session")
		return
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	who := sessIdent{principal, client, key}
	v, err := d.cachedView(ctx, who, false)
	if err == nil && !timeline.HasCall(v, eid) {
		// the call is newer than the view served so far: wait for one that has it
		v, err = d.cachedView(ctx, who, true)
	}
	if errors.Is(err, errNoSession) {
		writeErr(w, http.StatusNotFound, "not_found", "no such session")
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
	bs, err := d.drawerBuild(ctx, c, who, ex.RequestID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	in := timeline.CallInput{Exchange: ex, Usage: timeline.CallOf(ex, evs), Fact: bs.calls[ex.RequestID]}
	if det, err := timeline.ExchangeDetail(c, ex, entryCut); err == nil {
		in.Detail = &det
	}
	out, ok := timeline.CallDetailsOf(v, eid, in)
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such call in this session")
		return
	}
	writeJSON(w, out)
}

// errNoSession is a session with neither exchanges nor hook events.
var errNoSession = errors.New("ui: no such session")

// cachedView is a session's view built from the timeline cache's latest build of it and
// the effects as stored now, memoised per (build version, effect version). fresh waits for
// a build at the store's current version instead of serving an older one.
func (d Deps) cachedView(ctx context.Context, who sessIdent, fresh bool) (timeline.View, error) {
	start := time.Now()
	v, cached, err := d.buildView(ctx, who, fresh)
	if err == nil {
		d.Metrics.ViewBuild(cached, time.Since(start))
	}
	return v, err
}

// buildKey is the part of a view's key that names its timeline build version, up to and
// including the "/" before the effect version.
func buildKey(b store.Version) string {
	return fmt.Sprintf("%d.%d.%d.%d.%d/", b.ExCount, b.ExMaxRow, b.Parsed, b.HookCount, b.HookMaxID)
}

// viewHit looks for a view at exactly key: first in memory, then (when disk is true) on disk. Either way no
// unassigned connection may have been stored since the view was built inside its range.
func (d Deps) viewHit(ctx context.Context, c *store.CtxReader, ident store.SessionIdent, key string, now time.Time, disk bool) (timeline.View, bool) {
	if d.Views != nil {
		if e, ok := d.Views.getEntry(ident, key, now); ok {
			if quiet, err := unassignedQuiet(c, e.from, e.to, e.unMax); err == nil && quiet {
				return e.view, true
			}
		}
	}
	if disk && d.Disk != nil {
		if e, ok := d.Disk.GetView(ctx, ident, key, now); ok {
			if d.Views != nil {
				d.Views.put(e)
			}
			return e.view, true
		}
	}
	return timeline.View{}, false
}

// buildView is cachedView without the timing; cached is true for a view cache hit.
func (d Deps) buildView(ctx context.Context, who sessIdent, fresh bool) (_ timeline.View, cached bool, _ error) {
	c := d.Reader.WithContext(ctx)
	ident := store.SessionIdent{Principal: who.principal, Client: who.client, Key: who.name}
	vers, err := c.SessionVersions([]store.SessionIdent{ident})
	if err != nil {
		return timeline.View{}, false, err
	}
	ver := vers[ident]
	if ver.ExCount == 0 && ver.HookCount == 0 {
		return timeline.View{}, false, errNoSession
	}
	evs, err := c.EffectVersions([]store.SessionIdent{ident})
	if err != nil {
		return timeline.View{}, false, err
	}
	// read before the build reads the effects, so a connection stored later has a larger id
	unMax, err := c.MaxEffectID()
	if err != nil {
		return timeline.View{}, false, err
	}
	ev := evs[ident]
	now := d.now()
	// a view at exactly the current version, from memory or disk, needs no build at all
	if v, ok := d.viewHit(ctx, c, ident, viewKeyOf(ver, ev), now, true); ok {
		return v, true, nil
	}
	get := d.Timelines.latest
	if fresh {
		get = d.Timelines.get
		if isBackground(ctx) {
			get = d.Timelines.getBackground
		}
	}
	built, err := get(ctx, ident, ver, d.timelineBuild(who))
	if err != nil {
		return timeline.View{}, false, err
	}
	key := viewKeyOf(built.ver, ev)
	if built.ver != ver { // an older build the timeline cache holds: its own view may be cached
		if v, ok := d.viewHit(ctx, c, ident, key, now, false); ok {
			return v, true, nil
		}
	}
	hooks, err := c.HookEvents(who.principal, who.client, who.name)
	if err != nil {
		return timeline.View{}, false, err
	}
	last := lastActivity(built.bs, hooks)
	endOnly := endOnlyHooks(built.bs, hooks)
	active := isActive(last, endOnly, now)
	v, degraded, err := d.viewDeps().sessionViewChecked(c, who, built.bs, hooks, active, key)
	if err != nil {
		return timeline.View{}, false, err
	}
	if degraded {
		return v, false, nil // served, kept nowhere (D16)
	}
	from, to := activityRange(built.bs, hooks)
	e := &viewEntry{id: ident, key: key, last: last, active: active, endOnly: endOnly, view: v,
		from: from, to: to, unMax: unMax}
	if d.Views != nil {
		d.Views.put(e)
	}
	// only a view at the version this request read, of a session that is not active (D18),
	// built from a full build: a drift of an incremental one never survives a restart (040 D20)
	if built.ver == ver && !active && built.bs.kind == buildFull {
		d.Disk.PutView(e, ver, ev)
	}
	// an inactive session whose build is incremental gets its disk view from a full build in
	// the background, so a session watched live is not cold after a restart (040 D20, D30)
	if built.ver == ver && !active && built.bs.kind == buildIncremental && d.Disk != nil && d.Views != nil &&
		!d.Disk.HasView(ctx, ident, key, now) {
		d.Views.jobs.submit(d.Log, ident, key, func() { d.fullViewToDisk(who, ident, ver, ev, key) })
	}
	return v, false, nil
}

// fullBuildViewTimeout bounds one background full build for the disk view.
const fullBuildViewTimeout = buildTimeout

// viewJobs runs full builds for disk views one at a time, process-wide, with at most one
// queued per session: a newer version replaces a queued older one. A key that is running,
// queued or was the last one run for the session is not started again.
type viewJobs struct {
	mu      sync.Mutex
	busy    bool
	order   []store.SessionIdent
	pending map[store.SessionIdent]viewJob
	running map[store.SessionIdent]string
	done    map[store.SessionIdent]string
}

type viewJob struct {
	key string
	run func()
}

// submit queues run for the session at key; it starts the runner when none is busy. The
// jobs run on the server's lifetime context base, never on a request's.
func (j *viewJobs) submit(log *zap.Logger, id store.SessionIdent, key string, run func()) {
	if log == nil {
		log = zap.NewNop()
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.running[id] == key || j.done[id] == key || j.pending[id].key == key {
		return
	}
	if j.pending == nil {
		j.pending, j.running, j.done = map[store.SessionIdent]viewJob{}, map[store.SessionIdent]string{}, map[store.SessionIdent]string{}
	}
	if _, queued := j.pending[id]; !queued {
		j.order = append(j.order, id)
	}
	j.pending[id] = viewJob{key: key, run: run}
	if !j.busy {
		j.busy = true
		logging.Go(log, "ui_view_job", func() error { j.loop(); return nil })
	}
}

func (j *viewJobs) loop() {
	for {
		j.mu.Lock()
		if len(j.order) == 0 {
			j.busy = false
			j.mu.Unlock()
			return
		}
		id := j.order[0]
		j.order = j.order[1:]
		job := j.pending[id]
		delete(j.pending, id)
		j.running[id] = job.key
		j.mu.Unlock()
		func() {
			defer func() { _ = recover() }()
			job.run()
		}()
		j.mu.Lock()
		delete(j.running, id)
		j.done[id] = job.key
		j.mu.Unlock()
	}
}

// fullViewToDisk builds the session in full, outside the timeline cache, and writes its
// view to disk when the store has not moved on and the view is whole (040 D20): only a view
// from a full build reaches the disk. A failure is logged at Debug.
func (d Deps) fullViewToDisk(who sessIdent, ident store.SessionIdent, ver store.Version, ev store.EffectVersion, key string) {
	base := d.Ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, fullBuildViewTimeout)
	defer cancel()
	fail := func(err error) {
		if err != nil && base.Err() == nil {
			d.logger().Debug("ui view job: view not written", zap.Error(err))
		}
	}
	c := d.Reader.WithContext(ctx)
	unMax, err := c.MaxEffectID()
	if err != nil {
		fail(err)
		return
	}
	bs, err := buildSessionFn(c, who, d.Rules[who.client], entryCut)
	if err != nil {
		fail(err)
		return
	}
	// the store must still be at the version the request read, or the view is not the one asked for
	vers, err := c.SessionVersions([]store.SessionIdent{ident})
	if err != nil || vers[ident] != ver {
		fail(err)
		return
	}
	hooks, err := c.HookEvents(who.principal, who.client, who.name)
	if err != nil {
		fail(err)
		return
	}
	last := lastActivity(bs, hooks)
	endOnly := endOnlyHooks(bs, hooks)
	if isActive(last, endOnly, d.now()) {
		return // woke up meanwhile (D18)
	}
	v, degraded, err := d.viewDeps().sessionViewChecked(c, who, bs, hooks, false, key)
	if err != nil || degraded {
		fail(err)
		return
	}
	evs, err := c.EffectVersions([]store.SessionIdent{ident})
	if err != nil || evs[ident] != ev {
		fail(err)
		return
	}
	from, to := activityRange(bs, hooks)
	d.Disk.PutView(&viewEntry{id: ident, key: key, last: last, endOnly: endOnly, view: v,
		from: from, to: to, unMax: unMax}, ver, ev)
}

func (d Deps) getView(w http.ResponseWriter, r *http.Request) {
	principal, client, key, ok := parseSessionID(chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	ctx, cancel := storeCtx(r)
	defer cancel()
	v, err := d.cachedView(ctx, sessIdent{principal, client, key}, false)
	if errors.Is(err, errNoSession) {
		writeErr(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, v)
}

// hookLineJSON is one hook event of a tool call, as the tool details show it.
type hookLineJSON struct {
	Phase string `json:"phase"`
	Name  string `json:"name"`
	At    string `json:"at"`
}

// toolJSON is one tool call's details (spec 016 AC11): its input and result in full width
// (cut like the timeline, with the asking exchange for the raw), its state, reason and
// outcome, and its hook lines.
type toolJSON struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	ExchangeID      string         `json:"exchange_id,omitempty"`
	Input           string         `json:"input"`
	InputTruncated  bool           `json:"input_truncated"`
	Result          *string        `json:"result"`
	ResultTruncated bool           `json:"result_truncated"`
	ResultExchange  string         `json:"result_exchange_id,omitempty"`
	IsError         bool           `json:"is_error"`
	State           string         `json:"state,omitempty"`
	Reason          string         `json:"reason,omitempty"`
	Outcome         string         `json:"outcome,omitempty"`
	Chip            string         `json:"chip"`
	InputDiffers    bool           `json:"input_differs,omitempty"`
	HookInput       string         `json:"hook_input,omitempty"`
	HookOmitted     int            `json:"hook_omitted_bytes,omitempty"`
	Hooks           []hookLineJSON `json:"hooks"`
	// Questions are the questions the call put to the user with the answers, when the
	// client's profile can read them (017, 2026-11-13); omitted otherwise.
	Questions []questionJSON `json:"questions,omitempty"`
}

// questionJSON is one question a tool asked, its options and the user's answer and notes.
type questionJSON struct {
	Header  string               `json:"header,omitempty"`
	Text    string               `json:"text"`
	Multi   bool                 `json:"multi,omitempty"`
	Options []questionOptionJSON `json:"options"`
	Answer  string               `json:"answer"`
	Notes   string               `json:"notes,omitempty"`
}

type questionOptionJSON struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// toolQuestions reads a tool call's questions and answers through the client's profile;
// nil when the profile cannot or the call has no result yet.
func toolQuestions(rules ClientRules, out toolJSON) []questionJSON {
	qr, ok := rules.(core.QuestionReader)
	if !ok || out.Result == nil || out.InputTruncated {
		return nil
	}
	qs, ok := qr.Questions(out.Name, []byte(out.Input), *out.Result)
	if !ok {
		return nil
	}
	res := make([]questionJSON, 0, len(qs))
	for _, q := range qs {
		j := questionJSON{Header: q.Header, Text: q.Text, Multi: q.Multi, Answer: q.Answer, Notes: q.Notes, Options: []questionOptionJSON{}}
		for _, o := range q.Options {
			j.Options = append(j.Options, questionOptionJSON{Label: o.Label, Description: o.Description})
		}
		res = append(res, j)
	}
	return res
}

func (d Deps) getTool(w http.ResponseWriter, r *http.Request) {
	principal, client, key, ok := parseSessionID(chi.URLParam(r, "id"))
	tid := chi.URLParam(r, "tid")
	if !ok || tid == "" {
		writeErr(w, http.StatusNotFound, "not_found", "no such tool call")
		return
	}
	ctx, cancel := storeCtx(r)
	defer cancel()
	c := d.Reader.WithContext(ctx)
	ident := store.SessionIdent{Principal: principal, Client: client, Key: key}
	vers, err := c.SessionVersions([]store.SessionIdent{ident})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	who := sessIdent{principal, client, key}
	built, err := d.Timelines.latest(ctx, ident, vers[ident], d.timelineBuild(who))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	hooks, err := c.HookEvents(principal, client, key)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	out := toolJSON{ID: tid, Hooks: []hookLineJSON{}}
	found := false
	for _, e := range built.bs.entries {
		if e.ToolCallID != tid {
			continue
		}
		switch e.Kind {
		case timeline.KindToolCall:
			if found && out.ExchangeID != "" {
				continue
			}
			found = true
			out.Name, out.ExchangeID, out.Input, out.InputTruncated = e.Name, e.RequestID, e.Text, e.Omitted > 0
			out.State, out.Reason, out.Outcome, out.Chip = e.State, e.Reason, e.Outcome, e.Chip
			out.InputDiffers, out.HookInput, out.HookOmitted = e.InputDiffers, e.HookInput, e.HookOmitted
		case timeline.KindToolResult:
			if out.Result == nil {
				text := e.Text
				out.Result, out.ResultTruncated, out.ResultExchange, out.IsError = &text, e.Omitted > 0, e.RequestID, e.IsError
			}
		case timeline.KindHarness:
			if !found {
				found = true
				out.Name, out.State, out.Chip = e.Name, e.State, e.Chip
				out.Reason, out.Outcome = e.HookReason, e.HookOutcome
			}
		}
	}
	for _, h := range hooks {
		if h.ToolUseID != tid {
			continue
		}
		found = true
		if out.Name == "" {
			out.Name = h.ToolName
		}
		if out.ExchangeID == "" && out.Input == "" && h.InputHash != "" {
			if b, err := c.Content(h.InputHash); err == nil {
				out.Input = string(b)
			}
		}
		out.Hooks = append(out.Hooks, hookLineJSON{Phase: h.Phase, Name: h.SourceName, At: nsTime(h.ReceivedAt)})
	}
	if !found {
		writeErr(w, http.StatusNotFound, "not_found", "no such tool call")
		return
	}
	if out.Chip == "" {
		out.Chip = timeline.ChipNoHooks
	}
	out.Questions = toolQuestions(d.Rules[client], out)
	writeJSON(w, out)
}

type backgroundKey struct{}

// asBackground marks ctx as the background pass's: the builds it asks of the timeline
// cache are not stored at the cost of another session's entry (spec 040 D30).
func asBackground(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundKey{}, true)
}

func isBackground(ctx context.Context) bool { return ctx.Value(backgroundKey{}) != nil }
