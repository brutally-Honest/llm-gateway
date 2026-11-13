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
}

type viewEntry struct {
	id     store.SessionIdent
	key    string
	last   int64 // the session's last activity, unix ns
	active bool
	view   timeline.View
}

// NewViewCache returns an empty view memo; the server passes one to Handler and NewHub.
func NewViewCache() *ViewCache {
	return &ViewCache{ll: list.New(), items: map[store.SessionIdent]*list.Element{}}
}

func (c *ViewCache) get(id store.SessionIdent, key string, now time.Time) (timeline.View, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[id]
	if !ok {
		return timeline.View{}, false
	}
	e := el.Value.(*viewEntry)
	if e.key != key || isActive(e.last, now) != e.active {
		return timeline.View{}, false
	}
	c.ll.MoveToFront(el)
	return e.view, true
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

func isActive(last int64, now time.Time) bool {
	return last > 0 && now.Sub(time.Unix(0, last)) < ActiveWindow
}

func (d Deps) viewDeps() viewDeps { return viewDeps{rules: d.Rules, skew: d.AttributionSkew} }

func mountView(api chi.Router, d Deps) {
	api.Get("/sessions/{id}/view", d.getView)
	api.Get("/sessions/{id}/tools/{tid}", d.getTool)
}

// errNoSession is a session with neither exchanges nor hook events.
var errNoSession = errors.New("ui: no such session")

// cachedView is a session's view built from the timeline cache's latest build of it and
// the effects as stored now, memoised per (build version, effect version). fresh waits for
// a build at the store's current version instead of serving an older one.
func (d Deps) cachedView(ctx context.Context, who sessIdent, fresh bool) (timeline.View, error) {
	c := d.Reader.WithContext(ctx)
	ident := store.SessionIdent{Principal: who.principal, Client: who.client, Key: who.name}
	vers, err := c.SessionVersions([]store.SessionIdent{ident})
	if err != nil {
		return timeline.View{}, err
	}
	ver := vers[ident]
	if ver.ExCount == 0 && ver.HookCount == 0 {
		return timeline.View{}, errNoSession
	}
	evs, err := c.EffectVersions([]store.SessionIdent{ident})
	if err != nil {
		return timeline.View{}, err
	}
	get := d.Timelines.latest
	if fresh {
		get = d.Timelines.get
	}
	built, err := get(ctx, ident, ver, d.timelineBuild(who))
	if err != nil {
		return timeline.View{}, err
	}
	ev := evs[ident]
	b := built.ver
	key := fmt.Sprintf("%d.%d.%d.%d.%d/%d.%d", b.ExCount, b.ExMaxRow, b.Parsed, b.HookCount, b.HookMaxID, ev.Count, ev.MaxID)
	now := d.now()
	views := d.Views
	if views != nil {
		if v, ok := views.get(ident, key, now); ok {
			return v, nil
		}
	}
	hooks, err := c.HookEvents(who.principal, who.client, who.name)
	if err != nil {
		return timeline.View{}, err
	}
	last := lastActivity(built.bs, hooks)
	active := isActive(last, now)
	v, err := d.viewDeps().sessionView(c, who, built.bs, hooks, active, key)
	if err != nil {
		return timeline.View{}, err
	}
	if views != nil {
		views.put(&viewEntry{id: ident, key: key, last: last, active: active, view: v})
	}
	return v, nil
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
	writeJSON(w, out)
}
